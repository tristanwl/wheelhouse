import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch } from "./lib/http";
import { ADMIN_SESSION, ALL_FEATURES, USER_SESSION, mockApi, renderApp } from "./test-utils";

afterEach(() => vi.unstubAllGlobals());

const base = {
  "GET /api/features": ALL_FEATURES,
  "GET /api/status": { instances: [] },
  "GET /api/storage": { entries: [], instances: [] },
  "GET /api/downloads": { instances: [], items: [] },
  "GET /api/library": { items: [], instances: [] },
  "GET /api/add-meta": { instanceId: "radarr", instanceName: "Radarr", qualityProfiles: [] },
};

const unauth = { status: 401, body: { error: "authentication required" } };
const setupRequired = { status: 503, body: { error: "setup required" } };
const PASSWORD = "example-passphrase-1";

function where(router: { state: { location: { pathname: string; search: string } } }) {
  return router.state.location.pathname + router.state.location.search;
}

describe("route guards", () => {
  it("sends an anonymous visitor to /login, remembering the page", async () => {
    mockApi({ ...base, "GET /api/auth/session": unauth });
    const { router } = renderApp("/library?path=%2Fmovies");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
    expect(where(router)).toBe("/login?redirect=%2Flibrary%3Fpath%3D%252Fmovies");
  });

  it("sends everyone to /setup while the server needs setup", async () => {
    mockApi({ ...base, "GET /api/auth/session": setupRequired });
    const { router } = renderApp("/codecs");
    expect(await screen.findByRole("heading", { name: "Set up Wheelhouse" })).toBeInTheDocument();
    expect(where(router)).toBe("/setup");
  });

  it("sends /setup to /login once setup is done", async () => {
    mockApi({ ...base, "GET /api/auth/session": unauth });
    const { router } = renderApp("/setup");
    await screen.findByRole("heading", { name: "Sign in" });
    expect(where(router)).toBe("/login");
  });

  it("shows nothing of a page while the session loads", () => {
    mockApi({ ...base, "GET /api/auth/session": () => new Promise(() => {}) });
    renderApp("/");
    expect(screen.getByRole("status")).toHaveTextContent("Loading");
    expect(screen.queryByRole("heading", { name: "Service status" })).not.toBeInTheDocument();
  });

  it("lets a user see the app but shows the not-authorised page for /admin", async () => {
    const { calls } = mockApi({ ...base, "GET /api/auth/session": USER_SESSION });
    renderApp("/admin/users");
    expect(await screen.findByRole("heading", { name: "Not authorised" })).toBeInTheDocument();
    expect(calls.some((c) => c.url.startsWith("/api/users"))).toBe(false);
  });

  it("lets an admin through /admin (placeholder until the admin pages exist)", async () => {
    mockApi({ ...base });
    renderApp("/admin");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Not authorised" })).not.toBeInTheDocument();
  });

  it("goes to the login page when a request is answered 401 mid-session", async () => {
    let expired = false;
    mockApi({ ...base, "GET /api/status": () => (expired ? unauth : { instances: [] }) });
    const { router } = renderApp("/library");
    await screen.findByText(/Library \(/);
    expired = true;
    await apiFetch("/api/status");
    await screen.findByRole("heading", { name: "Sign in" });
    expect(where(router)).toBe("/login?redirect=%2Flibrary");
  });
});

describe("login", () => {
  it("has labelled fields with the right autocomplete and signs in, then follows ?redirect=", async () => {
    let signedIn = false;
    const { calls } = mockApi({
      ...base,
      "GET /api/auth/session": () => (signedIn ? ADMIN_SESSION : unauth),
      "POST /api/auth/login": () => {
        signedIn = true;
        return ADMIN_SESSION;
      },
    });
    const { router } = renderApp("/login?redirect=%2Fcodecs");
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Username");
    expect(name).toHaveAttribute("autocomplete", "username");
    expect(name).toHaveFocus();
    expect(screen.getByLabelText("Password")).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByLabelText("Password")).toHaveAttribute("type", "password");

    await user.type(name, "admin");
    await user.type(screen.getByLabelText("Password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/codecs"));

    const login = calls.find((c) => c.url === "/api/auth/login")!;
    expect(login.body).toEqual({ username: "admin", password: PASSWORD });
    // No credentials in any URL.
    expect(calls.some((c) => c.url.includes(PASSWORD))).toBe(false);
  });

  it("ignores an off-site redirect", async () => {
    let signedIn = false;
    mockApi({
      ...base,
      "GET /api/auth/session": () => (signedIn ? ADMIN_SESSION : unauth),
      "POST /api/auth/login": () => {
        signedIn = true;
        return ADMIN_SESSION;
      },
    });
    const { router } = renderApp("/login?redirect=%2F%2Fexample.com");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("shows one generic message for a 401, clears the password and is disabled while submitting", async () => {
    let release: (v: unknown) => void = () => {};
    mockApi({
      ...base,
      "GET /api/auth/session": unauth,
      "POST /api/auth/login": () =>
        new Promise((r) => {
          release = r;
        }).then(() => ({ status: 401, body: { error: "invalid username or password" } })),
    });
    renderApp("/login");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Username"), "someone");
    await user.type(screen.getByLabelText("Password"), "wrong-password-1");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("button", { name: "Signing in…" })).toBeDisabled();
    expect(screen.getByLabelText("Username")).toBeDisabled();
    release(null);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/Invalid username or password/);
    expect(alert).toHaveTextContent(/locked/);
    expect(alert.textContent).not.toMatch(/user (does not|doesn't) exist|wrong password/i);
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveFocus();
    // The 401 of a login attempt must not bounce the page.
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  it("explains throttling on a 429", async () => {
    mockApi({
      ...base,
      "GET /api/auth/session": unauth,
      "POST /api/auth/login": { status: 429, body: { error: "too many attempts" } },
    });
    renderApp("/login");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Username"), "x");
    await user.type(screen.getByLabelText("Password"), "y");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Too many sign-in attempts/);
  });

  it("leaves /login for a signed-in user", async () => {
    mockApi({ ...base });
    const { router } = renderApp("/login?redirect=%2Fcodecs");
    await waitFor(() => expect(router.state.location.pathname).toBe("/codecs"));
  });
});

describe("setup wizard", () => {
  it("creates the administrator, signs in and lands on the dashboard with the banner", async () => {
    let state: "setup" | "in" = "setup";
    const { calls } = mockApi({
      ...base,
      "GET /api/auth/session": () => (state === "in" ? ADMIN_SESSION : setupRequired),
      "POST /api/auth/setup": () => ({ status: 201, body: { user: ADMIN_SESSION.user } }),
      "POST /api/auth/login": () => {
        state = "in";
        return ADMIN_SESSION;
      },
    });
    const { router } = renderApp("/");
    const user = userEvent.setup();
    const token = await screen.findByLabelText("Setup token");
    await waitFor(() => expect(token).toHaveFocus());
    expect(screen.getByText(/\/config\/setup-token/)).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute(
      "autocomplete",
      "new-password"
    );

    await user.type(token, "example-setup-token");
    await user.type(screen.getByLabelText("Administrator username"), "admin");
    await user.type(screen.getByLabelText("Password"), "short");
    expect(screen.getByText(/Too short: 7 more characters needed/)).toBeInTheDocument();
    await user.clear(screen.getByLabelText("Password"));
    await user.type(screen.getByLabelText("Password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Create administrator" }));

    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("Add your services");
    expect(within(banner).getByRole("link")).toHaveAttribute("href", "/admin");
    expect(router.state.location.pathname).toBe("/");
    const setup = calls.find((c) => c.url === "/api/auth/setup")!;
    expect(setup.body).toEqual({
      token: "example-setup-token",
      username: "admin",
      password: PASSWORD,
    });
  });

  it("refuses a short or mismatched password without calling the server", async () => {
    const { calls } = mockApi({ ...base, "GET /api/auth/session": setupRequired });
    renderApp("/setup");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Setup token"), "t");
    await user.type(screen.getByLabelText("Administrator username"), "admin");
    await user.type(screen.getByLabelText("Password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm password"), "different-passphrase");
    await user.click(screen.getByRole("button", { name: "Create administrator" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/do not match/);
    expect(calls.some((c) => c.url === "/api/auth/setup")).toBe(false);
  });

  it("says when the token is wrong", async () => {
    mockApi({
      ...base,
      "GET /api/auth/session": setupRequired,
      "POST /api/auth/setup": { status: 403, body: { error: "invalid setup token" } },
    });
    renderApp("/setup");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Setup token"), "nope");
    await user.type(screen.getByLabelText("Administrator username"), "admin");
    await user.type(screen.getByLabelText("Password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Create administrator" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/setup token is not right/);
  });
});

describe("forced password change", () => {
  const forced = {
    ...USER_SESSION,
    user: { ...USER_SESSION.user, mustChangePassword: true },
  };

  it("sends the account to the change screen before anything else, then on to the page", async () => {
    let changed = false;
    const { calls } = mockApi({
      ...base,
      "GET /api/auth/session": () => (changed ? USER_SESSION : forced),
      "POST /api/auth/password": () => {
        changed = true;
        return { ...USER_SESSION, csrfToken: "rotated" };
      },
    });
    const { router } = renderApp("/codecs");
    const user = userEvent.setup();
    expect(
      await screen.findByRole("heading", { name: "Choose a new password" })
    ).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/change-password");
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
    // The app behind it never asked for anything.
    expect(calls.some((c) => c.url.startsWith("/api/features"))).toBe(false);

    await user.type(screen.getByLabelText("Current password"), "temporary-password-1");
    await user.type(screen.getByLabelText("New password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm new password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Change password" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/codecs"));
    const post = calls.find((c) => c.url === "/api/auth/password")!;
    expect(post.body).toEqual({ currentPassword: "temporary-password-1", newPassword: PASSWORD });
    expect(post.headers["x-csrf-token"]).toBe("csrf-test-token");
  });

  it("switches to the change screen when the server answers password-change-required", async () => {
    let blocked = false;
    mockApi({
      ...base,
      "GET /api/status": () =>
        blocked
          ? { status: 403, body: { error: "you must change", code: "password-change-required" } }
          : { instances: [] },
    });
    const { router } = renderApp("/");
    await screen.findByRole("heading", { name: "Service status" });
    blocked = true;
    // Any further request that is refused sends the person to the change screen.
    const { apiFetch } = await import("./lib/http");
    await apiFetch("/api/status");
    await waitFor(() => expect(router.state.location.pathname).toBe("/change-password"));
  });
});

describe("local-network bypass", () => {
  const bypass = { user: { username: "local", role: "user" }, bypass: true };

  it("skips the login, shows no sign-out, and offers an administrator sign-in", async () => {
    mockApi({ ...base, "GET /api/auth/session": bypass });
    renderApp("/");
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Service status" });
    await user.click(screen.getByRole("button", { name: /Local network/ }));
    expect(screen.getByRole("menuitem", { name: "Account" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Sign out" })).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Sign in as administrator" })).toHaveAttribute(
      "href",
      "/login?redirect=%2Fadmin"
    );
  });

  it("shows the local-network message instead of password and session management", async () => {
    const { calls } = mockApi({ ...base, "GET /api/auth/session": bypass });
    renderApp("/account");
    expect(await screen.findByText(/Signed in from the local network/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(calls.some((c) => c.url === "/api/auth/sessions")).toBe(false);
  });

  it("treats a missing bypass flag as false and a bypass answer as never admin", async () => {
    mockApi({
      ...base,
      "GET /api/auth/session": { user: { username: "alice", role: "admin" } },
    });
    renderApp("/account");
    expect(await screen.findByLabelText("Current password")).toBeInTheDocument();

    mockApi({
      ...base,
      "GET /api/auth/session": { user: { username: "x", role: "admin" }, bypass: true },
    });
    renderApp("/admin");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  it("asks for an admin login on /admin, and shows the form despite the bypass", async () => {
    mockApi({ ...base, "GET /api/auth/session": bypass });
    const { router } = renderApp("/admin/users");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
    expect(where(router)).toBe("/login?redirect=%2Fadmin%2Fusers");
    expect(screen.getByText(/without signing in/)).toBeInTheDocument();
  });
});

describe("user menu and account page", () => {
  it("shows the name and role, links to Account and signs out", async () => {
    let out = false;
    const { calls } = mockApi({
      ...base,
      "GET /api/auth/session": () => (out ? unauth : ADMIN_SESSION),
      "POST /api/auth/logout": () => {
        out = true;
        return { ok: true };
      },
    });
    const { router } = renderApp("/");
    const user = userEvent.setup();
    const button = await screen.findByRole("button", { name: /admin/ });
    expect(button).toHaveTextContent("admin");
    await user.click(button);
    expect(screen.getByRole("menuitem", { name: "Account" })).toHaveAttribute("href", "/account");
    await user.click(screen.getByRole("menuitem", { name: "Sign out" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    const logout = calls.find((c) => c.url === "/api/auth/logout")!;
    expect(logout.headers["x-csrf-token"]).toBe("csrf-test-token");
  });

  it("closes on Escape and returns focus to the button", async () => {
    mockApi({ ...base });
    renderApp("/");
    const user = userEvent.setup();
    const button = await screen.findByRole("button", { name: /admin/ });
    await user.click(button);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(button).toHaveFocus();
  });

  it("lists sessions, revokes another one and changes the password", async () => {
    const sessions = [
      {
        id: "a".repeat(32),
        createdAt: "2026-01-01T00:00:00Z",
        lastSeenAt: "2026-01-01T01:00:00Z",
        expiresAt: "2026-02-01T00:00:00Z",
        ip: "192.0.2.10",
        userAgent: "Example Browser",
        current: true,
      },
      {
        id: "b".repeat(32),
        createdAt: "2026-01-02T00:00:00Z",
        lastSeenAt: "2026-01-02T01:00:00Z",
        expiresAt: "2026-02-02T00:00:00Z",
        ip: "192.0.2.11",
        userAgent: "Other Browser",
        current: false,
      },
    ];
    let list = sessions;
    const { calls } = mockApi({
      ...base,
      "GET /api/auth/sessions": () => ({ sessions: list }),
      [`DELETE /api/auth/sessions/${"b".repeat(32)}`]: () => {
        list = [sessions[0]];
        return { ok: true };
      },
      "POST /api/auth/password": { ...ADMIN_SESSION, csrfToken: "rotated" },
    });
    renderApp("/account");
    const user = userEvent.setup();
    expect(await screen.findByText("Other Browser")).toBeInTheDocument();
    expect(screen.getByText("This session")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Account" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^Revoke(?! and)/ }));
    await waitFor(() => expect(screen.queryByText("Other Browser")).not.toBeInTheDocument());
    expect(calls.find((c) => c.method === "DELETE")!.headers["x-csrf-token"]).toBe(
      "csrf-test-token"
    );

    await user.type(screen.getByLabelText("Current password"), "old-passphrase-123");
    await user.type(screen.getByLabelText("New password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm new password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText(/Password changed/)).toBeInTheDocument();
    expect(screen.getByLabelText("Current password")).toHaveValue("");
  });

  it("shows the server's message when the current password is wrong", async () => {
    mockApi({
      ...base,
      "GET /api/auth/sessions": { sessions: [] },
      "POST /api/auth/password": {
        status: 400,
        body: { error: "current password is incorrect" },
      },
    });
    renderApp("/account");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Current password"), "nope-nope-nope");
    await user.type(screen.getByLabelText("New password"), PASSWORD);
    await user.type(screen.getByLabelText("Confirm new password"), PASSWORD);
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("current password is incorrect");
  });
});
