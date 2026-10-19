import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startStack, type Stack } from "./fakes";
import { ADMIN, startServer, type Client, type RunningServer } from "./harness";
import { ROUTE_LEVELS, sampleRequest } from "./routes";

// Acceptance criteria 2, 3 and 5: every registered route has a level; an
// unauthenticated request to any non-public route is 401; a `user` gets 403 on
// every admin route; every mutating route refuses a request without the CSRF
// token and one from a foreign origin. The route table is routes.ts (a Go test
// keeps it equal to the server's own router table).

const USER = { username: "plain-user", password: "plain-user-passphrase-01" };

const guarded = ROUTE_LEVELS.filter((r) => r.level !== "public");
const mutating = guarded.filter((r) => !/^(GET|HEAD|OPTIONS) /.test(r.route));
const adminRoutes = guarded.filter((r) => r.level === "admin");

describe("route levels, enforced", () => {
  let stack: Stack;
  let server: RunningServer;
  let user: Client;
  beforeAll(async () => {
    stack = await startStack();
    server = await startServer({ env: stack.env });
    const created = await server.admin.request("/api/users", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ...USER, role: "user", mustChangePassword: false }),
    });
    expect(created.status, created.text).toBe(201);
    server.plant(USER.password);
    user = server.newClient();
    expect((await user.login(USER.username, USER.password)).status).toBe(200);
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  /** A fresh session per request, because logout and password change end the one they use. */
  async function fresh(who: "admin" | "user"): Promise<Client> {
    const c = server.newClient();
    const [name, pw] =
      who === "admin" ? [ADMIN.username, ADMIN.password] : [USER.username, USER.password];
    const res = await c.login(name, pw);
    expect(res.status, res.text).toBe(200);
    return c;
  }

  it("lists the table it enforces", () => {
    expect(guarded.length).toBeGreaterThan(30);
    expect(adminRoutes.length).toBeGreaterThanOrEqual(9);
    expect(mutating.length).toBeGreaterThanOrEqual(14);
    expect(new Set(ROUTE_LEVELS.map((r) => r.route)).size).toBe(ROUTE_LEVELS.length);
  });

  it("(a) answers 401 JSON to an unauthenticated request on every non-public route", async () => {
    for (const { route } of guarded) {
      const { method, path } = sampleRequest(route);
      for (const headers of [
        {},
        { cookie: "wheelhouse=" + "A".repeat(43) },
        { cookie: "wheelhouse=x", "x-csrf-token": "x" },
      ] as Record<string, string>[]) {
        const res = await server.anon.request(path, { method, headers });
        expect(res.status, `${route} ${JSON.stringify(headers)}`).toBe(401);
        expect(res.json, route).toEqual({ error: "authentication required" });
        expect(res.headers.get("location"), route).toBeNull(); // never a redirect
        expect(res.headers.get("content-type")).toMatch(/application\/json/);
      }
    }
  });

  it("(b) answers 403 to a user on every admin route, and not to an administrator", async () => {
    for (const { route } of adminRoutes) {
      const { method, path } = sampleRequest(route);
      const u = await fresh("user");
      const refused = await u.request(path, { method });
      expect(refused.status, route).toBe(403);
      expect(refused.json, route).toEqual({ error: "forbidden" });

      const a = await fresh("admin");
      const allowed = await a.request(path, { method });
      expect(allowed.status, `${route}: ${allowed.text}`).not.toBe(403);
      expect(allowed.status, route).not.toBe(401);
    }
  });

  it("lets a user through every route that is not for administrators", async () => {
    for (const { route } of guarded.filter((r) => r.level === "authenticated")) {
      const { method, path } = sampleRequest(route);
      const u = await fresh("user");
      const res = await u.request(path, { method });
      expect([401, 403], `${route}: ${res.status} ${res.text}`).not.toContain(res.status);
    }
  });

  it("(c) refuses every mutating route without the CSRF token, with a wrong one, and from a foreign origin", async () => {
    for (const { route } of mutating) {
      const { method, path } = sampleRequest(route);
      // Two sessions per route (a refused request never reaches the handler, so
      // they stay valid across the attempts).
      const a = await fresh("admin");
      const b = await fresh("admin");
      const cookie = `${a.cookie!.name}=${a.cookie!.value}`;
      const attempt = (extra: Record<string, string>) =>
        server.anon.request(path, { method, headers: { cookie, ...extra } });
      const attempts: [string, Awaited<ReturnType<typeof attempt>>][] = [
        ["no token", await attempt({})],
        ["wrong token", await attempt({ "x-csrf-token": "wrong" })],
        ["another session's token", await attempt({ "x-csrf-token": b.csrf! })],
        ["the cookie as token", await attempt({ "x-csrf-token": a.cookie!.value })],
        [
          "foreign origin",
          await attempt({ "x-csrf-token": a.csrf!, origin: "https://evil.example" }),
        ],
        ["null origin", await attempt({ "x-csrf-token": a.csrf!, origin: "null" })],
        [
          "cross-site fetch",
          await attempt({ "x-csrf-token": a.csrf!, "sec-fetch-site": "cross-site" }),
        ],
        [
          "same-site fetch",
          await attempt({ "x-csrf-token": a.csrf!, "sec-fetch-site": "same-site" }),
        ],
      ];
      for (const [name, res] of attempts) {
        expect(res.status, `${route} (${name}): ${res.text}`).toBe(403);
      }
    }
  }, 120_000);

  it("(c) login and setup have no session but still refuse a foreign origin", async () => {
    for (const path of ["/api/auth/login", "/api/auth/setup"]) {
      const res = await server.anon.request(path, {
        method: "POST",
        headers: { "content-type": "application/json", origin: "https://evil.example" },
        body: JSON.stringify({ username: ADMIN.username, password: ADMIN.password, token: "x" }),
      });
      expect(res.status, path).toBe(403);
      expect(res.headers.getSetCookie(), path).toEqual([]);
    }
  });

  it("accepts a same-origin browser request that carries the token", async () => {
    const a = await fresh("admin");
    const res = await a.request("/api/users", {
      headers: { origin: server.baseUrl, "sec-fetch-site": "same-origin" },
    });
    expect(res.status).toBe(200);
    const post = await a.request("/api/codec-analysis/convert/nope/cancel", {
      method: "POST",
      headers: { origin: server.baseUrl, "sec-fetch-site": "same-origin" },
    });
    expect(post.status).toBe(400); // past the guard: the job does not exist
  });

  it("never changes state on a GET, whoever asks", async () => {
    const snapshot = async () => {
      const a = await fresh("admin");
      const users = (await a.request("/api/users")).json;
      const audit = (await a.request("/api/audit?limit=200")).json.entries.filter(
        (e: { action: string }) => e.action !== "login.ok"
      );
      const sessions = (await a.request("/api/users/" + users.users[0].id + "/sessions")).json;
      return JSON.stringify({ users, audit, sessions: sessions.sessions.length });
    };
    const before = await snapshot();
    const a = await fresh("admin");
    const u = await fresh("user");
    for (const { route } of ROUTE_LEVELS) {
      const { method, path } = sampleRequest(route);
      if (method !== "GET") continue;
      for (const who of [a, u, server.anon]) await who.request(path);
    }
    // The walks themselves added sessions (the fresh logins); compare the rest.
    const strip = (s: string) => {
      const o = JSON.parse(s);
      o.audit = o.audit.filter((e: { action: string }) => e.action !== "login.ok");
      delete o.sessions;
      return JSON.stringify(o);
    };
    expect(strip(await snapshot())).toBe(strip(before));
  });
});
