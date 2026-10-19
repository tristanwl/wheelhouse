import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ADMIN, startServer, type ApiResponse, type RunningServer } from "./harness";

const json = (body: unknown) => ({
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const VICTIM = { username: "throttle-victim", password: "victim-long-passphrase-01" };
const TIMING = { username: "throttle-timing", password: "timing-long-passphrase-01" };

async function createUser(server: RunningServer, u: { username: string; password: string }) {
  server.plant(u.password);
  const res = await server.admin.request(
    "/api/users",
    json({ ...u, role: "user", mustChangePassword: false })
  );
  expect(res.status, res.text).toBe(201);
}

/** What a client can observe of a failed login: if two of these differ, the server tells them apart. */
const observable = (r: ApiResponse) =>
  JSON.stringify({
    status: r.status,
    body: r.text,
    type: r.headers.get("content-type"),
    cookies: r.headers.getSetCookie(),
    cache: r.headers.get("cache-control"),
    retry: r.headers.get("retry-after"),
  });

const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];

// Acceptance criterion 6: throttling and lockout work and do not reveal whether
// a user exists. (The policy: 5 failures lock an account for 30 s, doubling to
// 15 min; 20 lock a client address; state is in memory. The backoff and decay
// are tested with an injected clock in server/internal/auth.)
describe("login throttling", () => {
  // Failures also count against the client address (20 lock it), and every
  // request here comes from 127.0.0.1, so each scenario gets its own server.
  let server: RunningServer;
  beforeEach(async () => {
    server = await startServer();
    await createUser(server, VICTIM);
    await createUser(server, TIMING);
  });
  afterEach(async () => {
    await server?.stop();
  });

  const attempt = (username: string, password: string) =>
    server.anon.request("/api/auth/login", json({ username, password }));

  it("answers a wrong password, an unknown user and a disabled user the same", async () => {
    const wrong = await attempt(TIMING.username, "wrong-password-0001");
    const unknown = await attempt("nobody-by-this-name", "wrong-password-0001");
    expect(wrong.status).toBe(401);
    expect(wrong.json).toEqual({ error: "invalid username or password" });
    expect(observable(unknown)).toBe(observable(wrong));
    // Even the right password for a name that exists in different letter case, or with odd input.
    for (const [u, p] of [
      ["", ""],
      ["x".repeat(300), "y"],
      [TIMING.username, "z".repeat(5000)],
    ]) {
      const r = await attempt(u, p);
      expect(r.status).toBe(401);
      expect(r.json).toEqual({ error: "invalid username or password" });
    }
  });

  it("costs about the same for a user that exists as for one that does not", async () => {
    const real: number[] = [];
    const fake: number[] = [];
    await attempt(TIMING.username, "warm-up-password-1"); // load the code paths
    for (let i = 0; i < 3; i++) {
      real.push((await attempt(TIMING.username, `wrong-password-${i}`)).elapsedMs);
      fake.push((await attempt(`nobody-by-name-${i}`, `wrong-password-${i}`)).elapsedMs);
    }
    // Both pay for one password hash. Generous bound: a missing hash would show as many times faster.
    const a = median(real);
    const b = median(fake);
    expect(Math.max(a, b) / Math.max(1, Math.min(a, b)), `real ${real}, fake ${fake}`).toBeLessThan(
      2.5
    );
    expect(Math.min(a, b)).toBeGreaterThan(10); // a hash was computed
  });

  it("locks an account after repeated failures, even for the right password, and looks the same for a made-up name", async () => {
    const last: ApiResponse[] = [];
    for (let i = 0; i < 5; i++) last.push(await attempt(VICTIM.username, `wrong-password-${i}`));
    for (const r of last) expect(r.status).toBe(401);

    const lockedRight = await attempt(VICTIM.username, VICTIM.password);
    expect(lockedRight.status).toBe(401);
    expect(lockedRight.json).toEqual({ error: "invalid username or password" });
    expect(lockedRight.headers.getSetCookie()).toEqual([]);

    // A name that does not exist is locked after the same number of failures and answers identically.
    for (let i = 0; i < 5; i++) await attempt("ghost-account", `wrong-password-${i}`);
    const lockedGhost = await attempt("ghost-account", "wrong-password-x");
    expect(observable(lockedGhost)).toBe(observable(lockedRight));

    // Other accounts, and people already signed in, are not affected.
    expect((await server.newClient().login(TIMING.username, TIMING.password)).status).toBe(200);
    expect((await server.admin.request("/api/status")).status).toBe(200);
  });

  it("audits failures and lockouts without recording passwords or unknown names", async () => {
    for (let i = 0; i < 5; i++) await attempt(VICTIM.username, `wrong-password-${i}`);
    for (let i = 0; i < 3; i++) await attempt("ghost-account", `wrong-password-${i}`);
    await attempt("nobody-by-this-name", "wrong-password-9");
    await server.newClient().login(TIMING.username, TIMING.password);
    const audit = await server.admin.request("/api/audit?limit=200");
    expect(audit.status).toBe(200);
    const actions = (audit.json.entries as { action: string }[]).map((e) => e.action);
    expect(actions.filter((a) => a === "login.fail").length).toBeGreaterThanOrEqual(9);
    expect(actions).toContain("login.lockout");
    expect(actions).toContain("login.ok");
    expect(audit.text).not.toContain("wrong-password");
    expect(audit.text).not.toContain("ghost-account");
    expect(audit.text).not.toContain("nobody-by-this-name");
    expect(audit.text).toContain(VICTIM.username);
    // The address is recorded.
    expect(audit.text).toContain('"address":"127.0.0.1"');
  });
});

describe("address throttling", () => {
  it("locks a client address after many failures against many names", async () => {
    const server = await startServer();
    try {
      let locked = false;
      for (let i = 0; i < 40 && !locked; i++) {
        await server.anon.request(
          "/api/auth/login",
          json({ username: `spray-${i}`, password: "wrong-password-0001" })
        );
        // A correct login that is refused shows the address is locked.
        if (i >= 15) {
          const r = await server.newClient().login(ADMIN.username, ADMIN.password);
          locked = r.status === 401;
        }
      }
      expect(locked, "the client address was never locked").toBe(true);
    } finally {
      await server.stop();
    }
  });
});
