import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import fixture from "@/fixtures/providers.json";
import { fieldsOf, type ProvidersResponse, type ServiceInstance } from "@/lib/admin";
import { ALL_FEATURES, USER_SESSION, mockApi, renderApp } from "@/test-utils";

afterEach(() => vi.unstubAllGlobals());

const providers = fixture as unknown as ProvidersResponse;
const base = {
  "GET /api/features": ALL_FEATURES,
  "GET /api/status": { instances: [] },
  "GET /api/storage": { entries: [], instances: [] },
  "GET /api/downloads": { instances: [], items: [] },
  "GET /api/admin/providers": providers,
  "GET /api/admin/instances": { instances: [] },
};

const KEY = "example-api-key-0001";

function radarr(over: Partial<ServiceInstance> = {}): ServiceInstance {
  return {
    id: "radarr",
    providerId: "radarr",
    role: "mediaManager",
    name: "Radarr",
    enabled: true,
    source: "database",
    readOnly: false,
    config: { url: "http://192.0.2.10:7878" },
    secrets: { apiKey: { set: true } },
    status: "ready",
    ...over,
  };
}

function where(router: { state: { location: { pathname: string } } }) {
  return router.state.location.pathname;
}

describe("services: forms generated from each provider's schema", () => {
  for (const p of providers.providers) {
    it(`${p.displayName}: every setting has a label, secrets are masked and empty, required ones are checked`, async () => {
      const user = userEvent.setup();
      const { calls } = mockApi({ ...base });
      renderApp("/admin/services/new");
      await user.selectOptions(await screen.findByLabelText("Type of service"), p.id);

      const fields = fieldsOf(p.configSchema);
      for (const f of fields) {
        const input = screen.getByLabelText(new RegExp(`^${f.label}( \\(|$)`));
        expect(input, f.name).toHaveValue("");
        if (f.kind === "secret") {
          expect(input).toHaveAttribute("type", "password");
          expect(input).toHaveAttribute("autocomplete", "off");
          // Show and Hide toggles the masking, and nothing was filled in.
          await user.click(screen.getByRole("button", { name: `Show ${f.label}` }));
          expect(input).toHaveAttribute("type", "text");
          await user.click(screen.getByRole("button", { name: `Hide ${f.label}` }));
          expect(input).toHaveAttribute("type", "password");
        }
      }

      await user.click(screen.getByRole("button", { name: "Add service" }));
      for (const f of fields.filter((f) => f.required)) {
        expect(await screen.findByText(`${f.label} is required.`)).toBeInTheDocument();
      }
      expect(screen.getByRole("alert")).toHaveTextContent("Fix the problems marked below");
      expect(calls.filter((c) => c.method !== "GET")).toEqual([]);
    });
  }
});

describe("services: adding", () => {
  it("adds a second Radarr: derived id, a test on unsaved values, then the save", async () => {
    const user = userEvent.setup();
    let saved = false;
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/instances": () => ({
        instances: saved ? [radarr(), radarr({ id: "radarr-4k", name: "Radarr 4K" })] : [radarr()],
      }),
      "POST /api/admin/instances/test": { ok: true, version: "5.0.1", error: "", durationMs: 12 },
      "POST /api/admin/instances": () => {
        saved = true;
        return { status: 201, body: { instance: radarr({ id: "radarr-4k", name: "Radarr 4K" }) } };
      },
    });
    const { router } = renderApp("/admin/services/new");
    await user.selectOptions(await screen.findByLabelText("Type of service"), "radarr");

    // The default name is taken, so the id will be derived from the name.
    const name = screen.getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Radarr 4K");
    expect(screen.getByText(/Left empty, Wheelhouse uses/)).toHaveTextContent("radarr-radarr-4k");
    await user.type(screen.getByLabelText(/^ID/), "radarr-4k");
    await user.type(screen.getByLabelText(/^URL/), "http://192.0.2.10:7879");
    await user.type(screen.getByLabelText(/^API key/), KEY);

    await user.click(screen.getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Connection works, version 5.0.1.")).toBeInTheDocument();
    const test = calls.find((c) => c.url === "/api/admin/instances/test")!;
    expect(test.body).toEqual({
      providerId: "radarr",
      config: { url: "http://192.0.2.10:7879", apiKey: KEY },
    });
    expect(test.headers["x-csrf-token"]).toBe("csrf-test-token");
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/admin/instances")).toBe(false);

    await user.click(screen.getByRole("button", { name: "Add service" }));
    await waitFor(() => expect(where(router)).toBe("/admin/services"));
    const post = calls.find((c) => c.method === "POST" && c.url === "/api/admin/instances")!;
    expect(post.body).toEqual({
      providerId: "radarr",
      id: "radarr-4k",
      name: "Radarr 4K",
      enabled: true,
      config: { url: "http://192.0.2.10:7879", apiKey: KEY },
    });
    expect(await screen.findByText("Radarr 4K was added.")).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: "Radarr 4K" })).toBeInTheDocument();
    // Nothing secret remains in the page or in browser storage.
    expect(document.body.textContent).not.toContain(KEY);
    expect(JSON.stringify({ ...localStorage })).not.toContain(KEY);
    expect(sessionStorage.length + localStorage.length).toBe(0);
  });

  it("shows a failed test with the redacted reason, and a wrong key", async () => {
    const user = userEvent.setup();
    mockApi({
      ...base,
      "POST /api/admin/instances/test": { ok: false, error: "API key incorrect", durationMs: 3 },
    });
    renderApp("/admin/services/new?provider=sonarr");
    await user.type(await screen.findByLabelText(/^URL/), "http://192.0.2.11:8989");
    await user.type(screen.getByLabelText(/^API key/), "wrong-key-0001");
    await user.click(screen.getByRole("button", { name: "Test connection" }));
    const result = await screen.findByText("Connection failed: API key incorrect");
    expect(result.closest("[aria-live]")).toHaveAttribute("aria-live", "polite");
    expect(document.body.textContent).not.toContain("wrong-key-0001");
  });

  it("checks the id rules before sending", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({ ...base });
    renderApp("/admin/services/new?provider=radarr");
    await user.type(await screen.findByLabelText(/^ID/), "sonarr-x");
    await user.type(screen.getByLabelText(/^URL/), "http://192.0.2.10:7878");
    await user.type(screen.getByLabelText(/^API key/), KEY);
    await user.click(screen.getByRole("button", { name: "Add service" }));
    expect(
      await screen.findByText(/The ID must be "radarr" or start with "radarr-"/)
    ).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("shows the server's per-setting errors next to the setting, and an id clash as a form error", async () => {
    const user = userEvent.setup();
    let answer: unknown = {
      status: 400,
      body: {
        error: "invalid configuration",
        fields: [{ field: "url", message: "URL must be an http or https URL with a host" }],
      },
    };
    mockApi({ ...base, "POST /api/admin/instances": () => answer });
    renderApp("/admin/services/new?provider=radarr");
    await user.type(await screen.findByLabelText(/^URL/), "http://192.0.2.10:7878");
    await user.type(screen.getByLabelText(/^API key/), KEY);
    await user.click(screen.getByRole("button", { name: "Add service" }));
    const message = await screen.findByText("URL must be an http or https URL with a host");
    expect(screen.getByLabelText(/^URL/)).toHaveAttribute("aria-describedby", message.id);
    expect(screen.getByLabelText(/^URL/)).toHaveAttribute("aria-invalid", "true");

    answer = { status: 409, body: { error: "an instance with that id already exists" } };
    await user.click(screen.getByRole("button", { name: "Add service" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("already exists");
  });
});

describe("services: editing and the list", () => {
  it("never shows a stored secret: it reads set, is kept when omitted and sent when replaced", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/instances": { instances: [radarr()] },
      "PUT /api/admin/instances/radarr": { instance: radarr() },
      "POST /api/admin/instances/test": { ok: true, version: "5.0.1" },
    });
    renderApp("/admin/services/radarr");
    expect(await screen.findByRole("heading", { name: "Edit Radarr" })).toBeInTheDocument();
    expect(screen.getByLabelText(/^URL/)).toHaveValue("http://192.0.2.10:7878");
    expect(screen.getByTestId("secret-state-apiKey")).toHaveTextContent("Set (hidden)");
    expect(screen.queryByLabelText(/^API key/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Test connection" }));
    await screen.findByText("Connection works, version 5.0.1.");
    expect(calls.find((c) => c.url === "/api/admin/instances/test")!.body).toEqual({
      id: "radarr",
      config: { url: "http://192.0.2.10:7878" },
    });

    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({
      name: "Radarr",
      enabled: true,
      config: { url: "http://192.0.2.10:7878" },
    });
  });

  it("replacing a secret shows an empty masked field and sends the new value", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/instances": { instances: [radarr()] },
      "PUT /api/admin/instances/radarr": { instance: radarr() },
    });
    renderApp("/admin/services/radarr");
    await user.click(await screen.findByRole("button", { name: "Replace API key" }));
    const field = screen.getByLabelText(/^API key/);
    expect(field).toHaveValue("");
    expect(field).toHaveAttribute("type", "password");
    await user.type(field, "replacement-key-0002");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(
      (calls.find((c) => c.method === "PUT")!.body as { config: Record<string, string> }).config
        .apiKey
    ).toBe("replacement-key-0002");
  });

  it("lists sources, status and last test; environment services are read-only and explained", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/instances": {
        instances: [
          radarr({ lastTest: { ok: true, at: new Date().toISOString() } }),
          radarr({
            id: "sonarr",
            providerId: "sonarr",
            name: "Sonarr",
            source: "environment",
            readOnly: true,
            status: "invalid-config",
            error: "API key is required",
          }),
        ],
      },
      "POST /api/admin/instances/sonarr/import": {
        instance: radarr({ id: "sonarr", providerId: "sonarr", name: "Sonarr" }),
      },
    });
    renderApp("/admin/services");
    const db = await screen.findByRole("listitem", { name: "Radarr" });
    expect(within(db).getByText("Source: database")).toBeInTheDocument();
    expect(within(db).getByText("Ready")).toBeInTheDocument();
    expect(within(db).getByText(/Last test just now: connection worked/)).toBeInTheDocument();
    expect(within(db).getByRole("link", { name: "Edit Radarr" })).toHaveAttribute(
      "href",
      "/admin/services/radarr"
    );

    const env = screen.getByRole("listitem", { name: "Sonarr" });
    expect(within(env).getByText("Source: environment")).toBeInTheDocument();
    expect(within(env).getByText(/cannot be edited here/)).toBeInTheDocument();
    expect(within(env).getByText("API key is required")).toBeInTheDocument();
    expect(within(env).queryByRole("link", { name: /Edit/ })).not.toBeInTheDocument();
    expect(within(env).queryByRole("button", { name: /Delete/ })).not.toBeInTheDocument();
    await user.click(
      within(env).getByRole("button", { name: "Import Sonarr from the environment" })
    );
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/sonarr/import"))).toBe(true));
    expect(await screen.findByText(/is now managed here/)).toBeInTheDocument();
  });

  it("tests a saved service and says the result in a live region", async () => {
    const user = userEvent.setup();
    mockApi({
      ...base,
      "GET /api/admin/instances": { instances: [radarr()] },
      "POST /api/admin/instances/test": { ok: false, error: "could not connect" },
    });
    renderApp("/admin/services");
    await user.click(await screen.findByRole("button", { name: "Test connection to Radarr" }));
    const text = await screen.findByText("Connection failed: could not connect");
    expect(text.closest("[aria-live]")).not.toBeNull();
  });

  it("deletes only after the confirm dialog, and not when it is cancelled", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/instances": { instances: [radarr()] },
      "DELETE /api/admin/instances/radarr": { ok: true, environmentAgain: false },
    });
    renderApp("/admin/services");
    await user.click(await screen.findByRole("button", { name: "Delete Radarr" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus()
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    await user.click(screen.getByRole("button", { name: "Delete Radarr" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete service" })
    );
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(await screen.findByText("Radarr was removed.")).toBeInTheDocument();
  });
});

describe("features", () => {
  const list = {
    features: [
      { id: "dashboard", label: "Dashboard", route: "/", enabled: true, canDisable: false },
      { id: "library", label: "Library", route: "/library", enabled: true, canDisable: true },
      { id: "codecs", label: "Codecs", route: "/codecs", enabled: false, canDisable: true },
    ],
  };

  it("toggles a feature, locks the dashboard on and explains 'disabled by admin'", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/features": list,
      "PUT /api/admin/features": {
        features: list.features.map((f) => (f.id === "library" ? { ...f, enabled: false } : f)),
      },
    });
    renderApp("/admin/features");
    expect(await screen.findByRole("switch", { name: "Dashboard" })).toBeDisabled();
    expect(screen.getByText(/disabled by admin/)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Dashboard" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Codecs" })).not.toBeChecked();
    await user.click(screen.getByRole("switch", { name: "Library" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ features: { library: false } });
    expect(await screen.findByText("Library is now off for everyone.")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Library" })).not.toBeChecked();
  });

  it("shows a refusal from the server", async () => {
    const user = userEvent.setup();
    mockApi({
      ...base,
      "GET /api/admin/features": list,
      "PUT /api/admin/features": { status: 400, body: { error: "unknown feature" } },
    });
    renderApp("/admin/features");
    await user.click(await screen.findByRole("switch", { name: "Library" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("unknown feature");
  });
});

describe("users", () => {
  const users = [
    {
      id: "a1",
      username: "admin",
      role: "admin",
      disabled: false,
      mustChangePassword: false,
      createdAt: "2026-01-01T10:00:00Z",
      passwordChangedAt: "2026-01-01T10:00:00Z",
    },
    {
      id: "b2",
      username: "alice",
      role: "user",
      disabled: false,
      mustChangePassword: true,
      createdAt: "2026-02-01T10:00:00Z",
      passwordChangedAt: "2026-02-01T10:00:00Z",
    },
  ];

  it("creates a user with a generated password, shown once, with a copy button", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/users": { users },
      "POST /api/users": { status: 201, body: { user: { ...users[1], username: "bob" } } },
    });
    renderApp("/admin/users");
    await user.click(await screen.findByRole("button", { name: "Add user" }));
    await user.type(screen.getByLabelText("Username"), "bob");
    await user.click(screen.getByRole("button", { name: "Generate" }));
    const generated = (screen.getByLabelText("Password") as HTMLInputElement).value;
    expect(generated).toHaveLength(20);
    await user.selectOptions(screen.getByLabelText("Role"), "admin");
    expect(screen.getByLabelText(/Must choose a new password/)).toBeChecked();
    await user.click(screen.getByRole("button", { name: "Create user" }));

    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({
      username: "bob",
      password: generated,
      role: "admin",
      mustChangePassword: true,
    });
    const panel = await screen.findByRole("region", { name: "Account created" });
    expect(within(panel).getByTestId("one-time-password")).toHaveTextContent(generated);
    await user.click(within(panel).getByRole("button", { name: "Copy password" }));
    expect(await navigator.clipboard.readText()).toBe(generated);
    expect(await within(panel).findByText("Copied.")).toBeInTheDocument();

    await user.click(within(panel).getByRole("button", { name: "I have passed it on" }));
    expect(screen.queryByTestId("one-time-password")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain(generated);
  });

  it("checks a typed password's length before sending", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({ ...base, "GET /api/users": { users } });
    renderApp("/admin/users");
    await user.click(await screen.findByRole("button", { name: "Add user" }));
    await user.type(screen.getByLabelText("Username"), "bob");
    await user.type(screen.getByLabelText("Password"), "short");
    await user.click(screen.getByRole("button", { name: "Create user" }));
    expect(await screen.findByText(/at least 12 characters/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("surfaces the last-administrator refusal for a role change, a disable and a delete", async () => {
    const user = userEvent.setup();
    const lastAdmin = {
      status: 409,
      body: { error: "there must be at least one enabled administrator" },
    };
    const { calls } = mockApi({
      ...base,
      "GET /api/users": { users },
      "PATCH /api/users/a1": lastAdmin,
      "DELETE /api/users/a1": lastAdmin,
    });
    renderApp("/admin/users");
    const row = await screen.findByRole("row", { name: /admin/ });
    expect(within(row).getByText("You")).toBeInTheDocument();

    // Demoting asks first, then the server says no, and the role shown stays.
    await user.selectOptions(within(row).getByLabelText("Role of admin"), "user");
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Remove administrator role",
      })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "at least one enabled administrator"
    );
    expect(within(row).getByLabelText("Role of admin")).toHaveValue("admin");

    await user.click(within(row).getByRole("button", { name: "Disable admin" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Disable account" })
    );
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(2));
    expect(calls.filter((c) => c.method === "PATCH")[1].body).toEqual({ disabled: true });

    await user.click(within(row).getByRole("button", { name: "Delete admin" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete user" })
    );
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "at least one enabled administrator"
    );
  });

  it("resets a password only after confirming, and shows the one-time password once", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/users": { users },
      "POST /api/users/b2/password": { ok: true },
    });
    renderApp("/admin/users");
    const row = await screen.findByRole("row", { name: /alice/ });
    await user.click(within(row).getByRole("button", { name: "Reset password of alice" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Reset password" })
    );
    const panel = await screen.findByRole("region", { name: "New password for alice" });
    const sent = (
      calls.find((c) => c.url === "/api/users/b2/password")!.body as { password: string }
    ).password;
    expect(sent).toHaveLength(20);
    expect(within(panel).getByTestId("one-time-password")).toHaveTextContent(sent);
  });

  it("changes a role to admin without a confirm, signs a user out everywhere and enables an account", async () => {
    const user = userEvent.setup();
    const disabled = { ...users[1], disabled: true };
    const { calls } = mockApi({
      ...base,
      "GET /api/users": { users: [users[0], disabled] },
      "PATCH /api/users/b2": { user: users[1] },
      "DELETE /api/users/b2/sessions": { revoked: 2 },
    });
    renderApp("/admin/users");
    const row = await screen.findByRole("row", { name: /alice/ });
    expect(within(row).getByText("Disabled")).toBeInTheDocument();
    await user.selectOptions(within(row).getByLabelText("Role of alice"), "admin");
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(
      calls[calls.length - 1].body ?? calls.find((c) => c.method === "PATCH")!.body
    ).toBeTruthy();
    expect(calls.find((c) => c.method === "PATCH")!.body).toEqual({ role: "admin" });
    await user.click(within(row).getByRole("button", { name: "Enable alice" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(2));
    await user.click(within(row).getByRole("button", { name: "Sign out everywhere for alice" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Sign out everywhere" })
    );
    expect(await screen.findByText("alice was signed out of 2 sessions.")).toBeInTheDocument();
  });
});

describe("sessions", () => {
  const users = {
    users: [
      {
        id: "a1",
        username: "admin",
        role: "admin",
        disabled: false,
        mustChangePassword: false,
        createdAt: "2026-01-01T10:00:00Z",
        passwordChangedAt: "2026-01-01T10:00:00Z",
      },
      {
        id: "b2",
        username: "alice",
        role: "user",
        disabled: false,
        mustChangePassword: false,
        createdAt: "2026-02-01T10:00:00Z",
        passwordChangedAt: "2026-02-01T10:00:00Z",
      },
    ],
  };
  const session = (id: string, current = false) => ({
    id,
    createdAt: "2026-03-01T10:00:00Z",
    lastSeenAt: "2026-03-01T11:00:00Z",
    expiresAt: "2026-03-08T10:00:00Z",
    ip: "192.0.2.50",
    userAgent: "Example Browser",
    current,
  });

  it("shows every user's sessions and ends one or all after a confirm", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/users": users,
      "GET /api/users/a1/sessions": { sessions: [session("s0", true)] },
      "GET /api/users/b2/sessions": { sessions: [session("s1"), session("s2")] },
      "DELETE /api/users/b2/sessions/s1": { ok: true },
      "DELETE /api/users/b2/sessions": { revoked: 1 },
    });
    renderApp("/admin/sessions");
    const alice = await screen.findByRole("region", { name: "Sessions of alice" });
    await within(alice).findAllByText("Example Browser");
    expect(screen.getByRole("region", { name: "Sessions of admin" })).toHaveTextContent(
      "This session"
    );

    await user.click(within(alice).getAllByRole("button", { name: /^End session/ })[0]);
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "End session" })
    );
    await waitFor(() =>
      expect(calls.some((c) => c.url === "/api/users/b2/sessions/s1")).toBe(true)
    );
    expect(await screen.findByText("One session of alice was ended.")).toBeInTheDocument();

    await user.click(within(alice).getByRole("button", { name: /^End all sessions/ }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "End session" })
    );
    await waitFor(() =>
      expect(calls.some((c) => c.url === "/api/users/b2/sessions" && c.method === "DELETE")).toBe(
        true
      )
    );
  });
});

describe("network", () => {
  const network = {
    enabled: false,
    cidrs: ["192.168.1.0/24"],
    trustedProxies: [],
    envTrustedProxies: [],
    warnings: [],
    allowedHosts: [],
    envAllowedHosts: [],
    client: {
      address: "192.168.1.20",
      forwarded: false,
      inRange: true,
      host: "nas.example.lan:3050",
      hostAllowed: false,
    },
  };

  it("lets the admin edit allowed host names, shows env ones read-only and the current Host", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/network": {
        ...network,
        allowedHosts: ["nas.example.lan"],
        envAllowedHosts: ["env.example.lan"],
      },
      "PUT /api/admin/network": {
        ...network,
        enabled: true,
        allowedHosts: ["nas.example.lan", "other.example.lan"],
        addedHost: "other.example.lan",
      },
    });
    renderApp("/admin/network");
    expect(await screen.findByText(/DNS rebinding/)).toBeInTheDocument();
    expect(screen.getByText("nas.example.lan:3050")).toBeInTheDocument();
    expect(screen.getByText("This name is not allowed yet")).toBeInTheDocument();
    expect(screen.getByText(/env\.example\.lan/)).toBeInTheDocument();
    expect(screen.getByLabelText("Allowed host names, entry 1")).toHaveValue("nas.example.lan");

    await user.click(screen.getByRole("button", { name: "Add a host name" }));
    await user.type(screen.getByLabelText("Allowed host names, entry 2"), "192.0.2.1");
    await user.click(screen.getByRole("switch", { name: /without a login/ }));
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    expect(await screen.findByText(/IP addresses are always allowed/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    const entry = screen.getByLabelText("Allowed host names, entry 2");
    await user.clear(entry);
    await user.type(entry, "other.example.lan");
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toMatchObject({
      allowedHosts: ["nas.example.lan", "other.example.lan"],
    });
    expect(
      await screen.findByText(/other\.example\.lan was added to the allowed host names/)
    ).toBeInTheDocument();
  });

  it("shows the caller's address and whether it is in range, and explains the user-role rule", async () => {
    mockApi({ ...base, "GET /api/admin/network": network });
    renderApp("/admin/network");
    expect(await screen.findByText("192.168.1.20")).toBeInTheDocument();
    expect(screen.getByText("Inside the ranges above")).toBeInTheDocument();
    expect(screen.getByText(/never administrators/)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /without a login/ })).not.toBeChecked();
  });

  it("validates entries, saves, and shows the server's warnings", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/network": network,
      "PUT /api/admin/network": {
        ...network,
        enabled: true,
        cidrs: ["192.168.1.0/24", "203.0.113.0/24"],
        warnings: ["The bypass range 203.0.113.0/24 is wider than private address space."],
      },
    });
    renderApp("/admin/network");
    await user.click(await screen.findByRole("button", { name: "Add a range" }));
    const entry = screen.getByLabelText("Local network ranges, entry 2");
    await user.type(entry, "not a range");
    await user.click(screen.getByRole("switch", { name: /without a login/ }));
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    expect(await screen.findByText(/That is not an address or range/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.clear(entry);
    await user.type(entry, "203.0.113.0/24");
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({
      enabled: true,
      cidrs: ["192.168.1.0/24", "203.0.113.0/24"],
      trustedProxies: [],
      allowedHosts: [],
    });
    expect(await screen.findByLabelText("Warnings")).toHaveTextContent(
      "wider than private address space"
    );
    expect(
      await screen.findByText("Network settings saved. They apply right away.")
    ).toBeInTheDocument();
  });

  it("will not turn the bypass on with no range, and shows a server refusal", async () => {
    const user = userEvent.setup();
    mockApi({
      ...base,
      "GET /api/admin/network": { ...network, cidrs: [] },
      "PUT /api/admin/network": {
        status: 400,
        body: { error: "trustedProxies: 0.0.0.0/0 would match every address and is refused" },
      },
    });
    renderApp("/admin/network");
    await user.click(await screen.findByRole("switch", { name: /without a login/ }));
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Add at least one address range");

    await user.click(screen.getByRole("button", { name: "Add a proxy" }));
    await user.type(screen.getByLabelText("Trusted proxy addresses, entry 1"), "172.18.0.2");
    await user.click(screen.getByRole("button", { name: "Add a range" }));
    await user.type(screen.getByLabelText("Local network ranges, entry 1"), "10.0.0.0/8");
    await user.click(screen.getByRole("button", { name: "Save network settings" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("would match every address");
  });
});

describe("media and converter", () => {
  it("hides the settings with a clear message while the server does not have them", async () => {
    mockApi({
      ...base,
      "GET /api/admin/converter": { status: 404, body: { error: "not found" } },
    });
    renderApp("/admin/converter");
    expect(await screen.findByText(/not available in this version/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("edits the media roots and the HDR switch against the contract", async () => {
    const user = userEvent.setup();
    const stored = {
      mediaRoots: ["/movies"],
      includeHdr: false,
      effectiveRoots: ["/movies"],
      source: "default",
    };
    const updated = {
      mediaRoots: ["/movies", "/tv"],
      includeHdr: true,
      effectiveRoots: ["/movies", "/tv"],
      source: "database",
    };
    let saved = false;
    const { calls } = mockApi({
      ...base,
      "GET /api/admin/converter": () => (saved ? updated : stored),
      "PUT /api/admin/converter": () => {
        saved = true;
        return updated;
      },
    });
    renderApp("/admin/converter");
    expect(
      await screen.findByText("In use now: /movies (the default folders).")
    ).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /HDR/ })).not.toBeChecked();
    await user.click(screen.getByRole("button", { name: "Add a folder" }));
    await user.type(screen.getByLabelText("Media folders, entry 2"), "tv");
    await user.click(screen.getByRole("switch", { name: /HDR/ }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(/starting with a slash/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    await user.type(screen.getByLabelText("Media folders, entry 2"), "{Home}/");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({
      mediaRoots: ["/movies", "/tv"],
      includeHdr: true,
    });
    expect(await screen.findByText("In use now: /movies, /tv (saved here).")).toBeInTheDocument();
  });
});

describe("audit log", () => {
  const entry = (id: number, action: string, actor: string, detail: unknown = {}) => ({
    id,
    at: "2026-03-01T10:00:00Z",
    actorId: "a1",
    actor,
    action,
    target: "radarr",
    address: "192.0.2.5",
    detail,
  });

  it("says how long entries are kept, and shows pruning as a plain entry", async () => {
    mockApi({
      ...base,
      "GET /api/audit?limit=50": {
        entries: [
          entry(7, "audit.prune", "system", {
            deleted: 12,
            cutoff: "2026-04-01T00:00:00Z",
            retentionDays: 180,
          }),
        ],
        nextBefore: null,
        retention: { days: 180, floorDays: 30 },
      },
    });
    renderApp("/admin/audit");
    expect(await screen.findByText("Entries are kept for 180 days, then removed.")).toBeVisible();
    const row = await screen.findByRole("row", { name: /Old entries removed/ });
    expect(row).toHaveTextContent("entries removed: 12; older than: 2026-04-01T00:00:00Z");
    expect(screen.queryByText(/WHEELHOUSE/)).not.toBeInTheDocument();
  });

  it("shows nothing about retention when the server does not say", async () => {
    mockApi({ ...base, "GET /api/audit?limit=50": { entries: [], nextBefore: null } });
    renderApp("/admin/audit");
    await screen.findByText("Nothing recorded yet.");
    expect(screen.queryByText(/Entries are kept/)).not.toBeInTheDocument();
  });

  it("shows readable rows, filters by action and who, and pages back", async () => {
    const user = userEvent.setup();
    const first = Array.from({ length: 50 }, (_, i) =>
      i === 0
        ? entry(1000, "instance.update", "admin", {
            provider: "radarr",
            fields: ["URL"],
            secrets: ["API key"],
          })
        : entry(1000 - i, "login.ok", i % 2 ? "alice" : "admin")
    );
    const { calls } = mockApi({
      ...base,
      "GET /api/audit?limit=50": { entries: first, nextBefore: 951 },
      "GET /api/audit?limit=50&before=951": {
        entries: [entry(950, "user.create", "admin")],
        nextBefore: null,
      },
    });
    renderApp("/admin/audit");
    const row = await screen.findByRole("row", { name: /Service changed/ });
    expect(row).toHaveTextContent(
      "type: radarr; settings changed: URL; credentials replaced: API key"
    );
    expect(row).toHaveTextContent("instance.update");

    await user.selectOptions(screen.getByLabelText("Action"), "instance.update");
    expect(screen.getAllByRole("row")).toHaveLength(2); // header and the one entry
    await user.selectOptions(screen.getByLabelText("Action"), "");
    await user.type(screen.getByLabelText("Who"), "ALI");
    expect(
      screen
        .getAllByRole("row")
        .every((r) => !/\badmin\b/.test(r.textContent ?? "") || r.querySelector("th"))
    ).toBe(true);

    await user.clear(screen.getByLabelText("Who"));
    await user.click(screen.getByRole("button", { name: "Load older entries" }));
    expect(await screen.findByRole("row", { name: /User created/ })).toBeInTheDocument();
    expect(calls.some((c) => c.url === "/api/audit?limit=50&before=951")).toBe(true);
    expect(screen.queryByRole("button", { name: "Load older entries" })).not.toBeInTheDocument();
  });
});

describe("who can see the administration pages", () => {
  it("keeps a user out of every page and sends no admin request", async () => {
    for (const path of [
      "/admin",
      "/admin/services",
      "/admin/services/new",
      "/admin/features",
      "/admin/users",
      "/admin/sessions",
      "/admin/network",
      "/admin/converter",
      "/admin/audit",
    ]) {
      const { calls } = mockApi({ ...base, "GET /api/auth/session": USER_SESSION });
      const view = renderApp(path);
      expect(
        await screen.findByRole("heading", { name: "Not authorised" }),
        path
      ).toBeInTheDocument();
      expect(
        calls.filter(
          (c) =>
            c.url.startsWith("/api/admin") ||
            c.url.startsWith("/api/users") ||
            c.url.startsWith("/api/audit")
        ),
        path
      ).toEqual([]);
      view.unmount();
      vi.unstubAllGlobals();
    }
  });

  it("shows the Admin link in the nav for administrators only", async () => {
    mockApi({ ...base });
    const admin = renderApp("/");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Admin" })).toHaveAttribute("href", "/admin");
    admin.unmount();
    vi.unstubAllGlobals();

    mockApi({ ...base, "GET /api/auth/session": USER_SESSION });
    renderApp("/");
    const userNav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(userNav).queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
  });

  it("sub-navigation lists every page and marks the current one", async () => {
    mockApi({ ...base, "GET /api/admin/features": { features: [] } });
    renderApp("/admin/features");
    const nav = await screen.findByRole("navigation", { name: "Administration" });
    for (const label of [
      "Services",
      "Features",
      "Users",
      "Sessions",
      "Network",
      "Media and converter",
      "Audit log",
    ]) {
      expect(within(nav).getByRole("link", { name: label })).toBeInTheDocument();
    }
    expect(within(nav).getByRole("link", { name: "Features" })).toHaveAttribute(
      "aria-current",
      "page"
    );
  });

  // Issue #86: at phone width the tabs used to sit in a row that scrolled
  // sideways with the last ones cut off and no hint. They wrap instead, so none is
  // hidden; the order (which is the keyboard focus order) and the semantics stay.
  it("sub-navigation wraps instead of scrolling, in a fixed order", async () => {
    mockApi({ ...base, "GET /api/admin/features": { features: [] } });
    renderApp("/admin/features");
    const nav = await screen.findByRole("navigation", { name: "Administration" });
    const list = within(nav).getByRole("list");
    expect(list.className).toMatch(/\bflex-wrap\b/);
    expect(list.className).not.toMatch(/overflow-/);
    for (const item of within(list).getAllByRole("listitem")) {
      expect(item.className).not.toMatch(/overflow-|\bshrink-0\b/);
    }
    expect(
      within(list)
        .getAllByRole("link")
        .map((a) => a.textContent)
    ).toEqual([
      "Services",
      "Features",
      "Users",
      "Sessions",
      "Network",
      "Media and converter",
      "Audit log",
    ]);
    // The current page is the only one marked, with aria-current (not by colour alone).
    expect(
      within(list)
        .getAllByRole("link")
        .filter((a) => a.hasAttribute("aria-current"))
    ).toHaveLength(1);
  });
});

describe("first-run checklist on the dashboard", () => {
  it("shows for an administrator until a service exists", async () => {
    mockApi({ ...base });
    renderApp("/");
    const banner = await screen.findByRole("complementary", { name: "Getting started" });
    expect(within(banner).getByRole("link", { name: "Add your services" })).toHaveAttribute(
      "href",
      "/admin/services/new"
    );
  });

  it("is gone once there is a service, and never shown to a user", async () => {
    mockApi({ ...base, "GET /api/admin/instances": { instances: [radarr()] } });
    const first = renderApp("/");
    await screen.findByRole("heading", { name: "Service status" });
    await waitFor(() =>
      expect(
        screen.queryByRole("complementary", { name: "Getting started" })
      ).not.toBeInTheDocument()
    );
    first.unmount();
    vi.unstubAllGlobals();

    const { calls } = mockApi({ ...base, "GET /api/auth/session": USER_SESSION });
    renderApp("/");
    await screen.findByRole("heading", { name: "Service status" });
    expect(
      screen.queryByRole("complementary", { name: "Getting started" })
    ).not.toBeInTheDocument();
    expect(calls.some((c) => c.url.startsWith("/api/admin"))).toBe(false);
  });
});

describe("source rules", () => {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) walk(path);
      else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) files.push(path);
    }
  };
  walk(resolve(process.cwd(), "src/pages/admin"));

  it("every administration page that deletes or ends something goes through the confirm dialog", () => {
    expect(files.length).toBeGreaterThan(5);
    for (const file of files) {
      const text = readFileSync(file, "utf8");
      if (/method:\s*"DELETE"/.test(text)) {
        expect(text, file).toContain("<ConfirmModal");
      }
    }
  });

  it("no administration code touches browser storage or logs", () => {
    for (const file of files) {
      const text = readFileSync(file, "utf8");
      expect(text, file).not.toMatch(/localStorage|sessionStorage|console\./);
    }
  });
});
