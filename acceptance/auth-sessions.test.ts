import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { ADMIN, startServer, type Client, type RunningServer } from "./harness";

const json = (method: string, body: unknown) => ({
  method,
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const NEW_PASSWORD = "harness-admin-passphrase-002";

/** The attributes of a Set-Cookie header, lower-cased, without the value. */
function attrs(setCookie: string): string[] {
  return setCookie
    .split(";")
    .slice(1)
    .map((a) => a.trim().toLowerCase());
}

// Acceptance criterion 4: cookies carry the flags; logout and password change
// invalidate sessions. (Idle and absolute timeouts need a clock, which a
// black-box server does not offer; they are tested in server/internal/auth and
// server/internal/app with an injected clock. Here the expiry the server
// reports is checked.)
describe("sessions", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer();
  });
  afterAll(async () => {
    await server?.stop();
  });

  const loginNew = async (): Promise<Client> => {
    const c = server.newClient();
    const res = await c.login(ADMIN.username, ADMIN.password);
    expect(res.status, res.text).toBe(200);
    return c;
  };

  it("sets a HttpOnly, SameSite=Strict, Path=/ cookie named wheelhouse over plain HTTP", async () => {
    const c = server.newClient();
    const res = await c.login(ADMIN.username, ADMIN.password);
    const set = res.headers.getSetCookie();
    expect(set).toHaveLength(1);
    expect(set[0].startsWith("wheelhouse=")).toBe(true);
    const a = attrs(set[0]);
    expect(a).toContain("httponly");
    expect(a).toContain("samesite=strict");
    expect(a).toContain("path=/");
    expect(a).not.toContain("secure"); // plain HTTP: the browser would drop a Secure cookie
    expect(a.some((x) => x.startsWith("domain="))).toBe(false);
    const maxAge = Number(a.find((x) => x.startsWith("max-age="))!.slice(8));
    expect(maxAge).toBeGreaterThan(29 * 86400);
    expect(maxAge).toBeLessThanOrEqual(30 * 86400);
    // 256 bits, base64url.
    expect(c.cookie!.value).toMatch(/^[A-Za-z0-9_-]{43}$/);
    // The token is only ever in the cookie.
    expect(res.text).not.toContain(c.cookie!.value);
    expect(c.csrf).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(c.csrf).not.toBe(c.cookie!.value);
  });

  it("reports the idle (12 h) and absolute (30 d) expiry", async () => {
    const c = await loginNew();
    const res = await c.request("/api/auth/session");
    expect(res.status).toBe(200);
    const now = Date.now();
    const idle = Date.parse(res.json.expiresAt) - now;
    const absolute = Date.parse(res.json.absoluteExpiresAt) - now;
    expect(Math.abs(idle - 12 * 3600_000)).toBeLessThan(120_000);
    expect(Math.abs(absolute - 30 * 86400_000)).toBeLessThan(120_000);
    expect(res.json.csrfToken).toBe(c.csrf);
    expect(res.json.user).toMatchObject({ username: ADMIN.username, role: "admin" });
    expect(res.text).not.toMatch(/argon2|password_hash|passwordHash/);
  });

  it("makes a new session with a new token and CSRF token at every login", async () => {
    const a = await loginNew();
    const b = await loginNew();
    expect(a.cookie!.value).not.toBe(b.cookie!.value);
    expect(a.csrf).not.toBe(b.csrf);
    // Both work: logging in elsewhere does not end this session.
    expect((await a.request("/api/status")).status).toBe(200);
    expect((await b.request("/api/status")).status).toBe(200);
    // A token chosen by the client is never adopted (no fixation).
    const attacker = "A".repeat(43);
    const c = server.newClient();
    c.cookie = { name: "wheelhouse", value: attacker };
    const res = await c.login(ADMIN.username, ADMIN.password);
    expect(res.status).toBe(200);
    expect(c.cookie!.value).not.toBe(attacker);
  });

  it("lists the own sessions without tokens and ends one on request", async () => {
    const a = await loginNew();
    const b = await loginNew();
    const list = await a.request("/api/auth/sessions");
    expect(list.status).toBe(200);
    const sessions = list.json.sessions as { id: string; current: boolean; userAgent: string }[];
    expect(sessions.length).toBeGreaterThanOrEqual(2);
    expect(sessions.filter((s) => s.current)).toHaveLength(1);
    expect(list.text).not.toContain(a.cookie!.value);
    expect(list.text).not.toContain(b.cookie!.value);
    const mine = a.session.sessionId;
    expect(sessions.find((s) => s.current)!.id).toBe(mine);

    const other = sessions.find((s) => !s.current && s.id !== b.session.sessionId)!;
    expect(other).toBeTruthy();
    const del = await a.request(`/api/auth/sessions/${b.session.sessionId}`, { method: "DELETE" });
    expect(del.status, del.text).toBe(200);
    expect((await b.request("/api/status")).status).toBe(401);
    expect((await a.request("/api/status")).status).toBe(200);
    expect(
      (await a.request(`/api/auth/sessions/${"0".repeat(32)}`, { method: "DELETE" })).status
    ).toBe(404);
  });

  it("logout ends the session and clears the cookie", async () => {
    const c = await loginNew();
    expect((await c.request("/api/status")).status).toBe(200);
    const res = await c.request("/api/auth/logout", { method: "POST" });
    expect(res.status).toBe(200);
    const set = res.headers.getSetCookie();
    expect(set).toHaveLength(1);
    expect(set[0]).toMatch(/^wheelhouse=;/);
    expect(attrs(set[0])).toContain("max-age=0");
    // The old cookie is dead server-side, not just deleted in the browser.
    expect((await c.request("/api/status")).status).toBe(401);
    expect((await c.request("/api/auth/session")).status).toBe(401);
  });

  it("changing the password ends every session and issues a new one", async () => {
    const keep = await loginNew();
    const other = await loginNew();
    const changing = await loginNew();
    const oldToken = changing.cookie!.value;

    const wrong = await changing.request(
      "/api/auth/password",
      json("POST", { currentPassword: "not-the-password-1", newPassword: NEW_PASSWORD })
    );
    expect(wrong.status).toBe(400);
    expect((await changing.request("/api/status")).status).toBe(200);

    const weak = await changing.request(
      "/api/auth/password",
      json("POST", { currentPassword: ADMIN.password, newPassword: "short" })
    );
    expect(weak.status).toBe(400);

    const res = await changing.request(
      "/api/auth/password",
      json("POST", { currentPassword: ADMIN.password, newPassword: NEW_PASSWORD })
    );
    expect(res.status, res.text).toBe(200);
    server.plant(NEW_PASSWORD);
    const set = res.headers.getSetCookie();
    expect(set).toHaveLength(1);
    const fresh = set[0].split(";")[0].split("=")[1];
    expect(fresh).not.toBe(oldToken);
    expect(res.json.csrfToken).not.toBe(changing.csrf);

    // Old sessions, the changing one included, are gone.
    for (const c of [keep, other]) expect((await c.request("/api/status")).status).toBe(401);
    expect((await changing.request("/api/status")).status).toBe(401);
    // The rotated one works.
    changing.cookie = { name: "wheelhouse", value: fresh };
    changing.csrf = res.json.csrfToken;
    expect((await changing.request("/api/status")).status).toBe(200);

    // And the passwords: the old one no longer logs in, the new one does.
    expect((await server.newClient().login(ADMIN.username, ADMIN.password)).status).toBe(401);
    const c = server.newClient();
    expect((await c.login(ADMIN.username, NEW_PASSWORD)).status).toBe(200);
    // Put it back so later tests (and the shared admin client) keep working.
    const back = await c.request(
      "/api/auth/password",
      json("POST", { currentPassword: NEW_PASSWORD, newPassword: ADMIN.password })
    );
    expect(back.status, back.text).toBe(200);
    const login = await server.admin.login(ADMIN.username, ADMIN.password);
    expect(login.status).toBe(200);
  });
});

// Secure cookies: only when the request arrived over HTTPS, and a forwarded
// header counts only from a proxy the operator listed.
describe("the Secure cookie and the trusted proxy list", () => {
  it("ignores X-Forwarded-Proto when no proxy is trusted", async () => {
    const server = await startServer();
    try {
      const res = await server
        .newClient()
        .login(ADMIN.username, ADMIN.password, { headers: { "x-forwarded-proto": "https" } });
      const set = res.headers.getSetCookie()[0];
      expect(set.startsWith("wheelhouse=")).toBe(true);
      expect(attrs(set)).not.toContain("secure");
    } finally {
      await server.stop();
    }
  });

  it("ignores X-Forwarded-Proto from a peer that is not in the list", async () => {
    const server = await startServer({ env: { WHEELHOUSE_TRUSTED_PROXIES: "192.0.2.0/24" } });
    try {
      const res = await server
        .newClient()
        .login(ADMIN.username, ADMIN.password, { headers: { "x-forwarded-proto": "https" } });
      const set = res.headers.getSetCookie()[0];
      expect(set.startsWith("wheelhouse=")).toBe(true);
      expect(attrs(set)).not.toContain("secure");
    } finally {
      await server.stop();
    }
  });

  it("uses a Secure __Host- cookie when a trusted proxy says HTTPS, and only reads that cookie then", async () => {
    // The harness connects from 127.0.0.1, the peer the server sees.
    const server = await startServer({ env: { WHEELHOUSE_TRUSTED_PROXIES: "127.0.0.1" } });
    try {
      const c = server.newClient();
      const https = { "x-forwarded-proto": "https" };
      const res = await c.login(ADMIN.username, ADMIN.password, { headers: https });
      const set = res.headers.getSetCookie()[0];
      expect(set.startsWith("__Host-wheelhouse=")).toBe(true);
      const a = attrs(set);
      for (const flag of ["secure", "httponly", "samesite=strict", "path=/"])
        expect(a).toContain(flag);
      expect(a.some((x) => x.startsWith("domain="))).toBe(false);
      expect(c.cookie!.name).toBe("__Host-wheelhouse");

      // Over "HTTPS" the __Host- cookie works and a plain-named one does not.
      const ok = await c.request("/api/auth/session", { headers: https });
      expect(ok.status).toBe(200);
      const plain = await server.anon.request("/api/auth/session", {
        headers: { ...https, cookie: `wheelhouse=${c.cookie!.value}` },
      });
      expect(plain.status).toBe(401);
      // Over plain HTTP the __Host- cookie is not read.
      const insecure = await c.request("/api/auth/session");
      expect(insecure.status).toBe(401);
    } finally {
      await server.stop();
    }
  });
});
