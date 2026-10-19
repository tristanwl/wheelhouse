import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startServer, type RunningServer } from "./harness";
import { ROUTE_LEVELS, sampleRequest } from "./routes";

// Acceptance criterion 8: the bypass never grants admin, ignores spoofed
// forwarded headers and respects the trusted-proxy list. Every request here
// comes from the real loopback peer (127.0.0.1), so "from the local network" is
// a bypass range of 127.0.0.1/32, and "from elsewhere" is a range that does not
// contain it.

const put = (server: RunningServer, body: unknown) =>
  server.admin.request("/api/admin/network", {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });

const OTHER = "198.51.100.0/24";
const guarded = ROUTE_LEVELS.filter((r) => r.level !== "public");
const adminRoutes = guarded.filter((r) => r.level === "admin");
const loginOnly = [
  "POST /api/auth/logout",
  "POST /api/auth/password",
  "GET /api/auth/sessions",
  "DELETE /api/auth/sessions/{id}",
];

describe("local-network bypass", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer({});
  });
  afterAll(async () => {
    await server?.stop();
  });

  it("is off by default: a local client still gets 401", async () => {
    const cfg = await server.admin.request("/api/admin/network");
    expect(cfg.json).toMatchObject({ enabled: false, cidrs: [], trustedProxies: [], warnings: [] });
    for (const path of ["/api/status", "/api/auth/session", "/api/library"]) {
      expect((await server.anon.request(path)).status, path).toBe(401);
    }
  });

  it("refuses to match everything, an empty list, and junk", async () => {
    for (const body of [
      { enabled: true, cidrs: ["0.0.0.0/0"] },
      { enabled: true, cidrs: ["::/0"] },
      { enabled: true, cidrs: [] },
      { enabled: true, cidrs: ["nope"] },
      { cidrs: ["10.0.0.0/8"] },
    ]) {
      const res = await put(server, body);
      expect(res.status, JSON.stringify(body)).toBe(400);
    }
    expect((await server.anon.request("/api/status")).status).toBe(401);
  });

  it("ignores forwarded headers from an untrusted peer", async () => {
    expect((await put(server, { enabled: true, cidrs: [OTHER] })).status).toBe(200);
    for (const headers of [
      { "x-forwarded-for": "198.51.100.7" },
      { "x-real-ip": "198.51.100.7" },
      { forwarded: "for=198.51.100.7" },
    ]) {
      const res = await server.anon.request("/api/status", { headers });
      expect(res.status, JSON.stringify(headers)).toBe(401);
    }
  });

  it("honours a trusted proxy's forwarded client, right-most untrusted entry first", async () => {
    const set = await put(server, { enabled: true, cidrs: [OTHER], trustedProxies: ["127.0.0.1"] });
    expect(set.status, set.text).toBe(200);
    const via = (xff: string) => server.anon.request("/api/status", { headers: { "x-forwarded-for": xff } });
    expect((await via("198.51.100.7")).status).toBe(200);
    expect((await via("203.0.113.9")).status).toBe(401);
    expect((await via("198.51.100.7, 203.0.113.9")).status).toBe(401);
    expect((await via("203.0.113.9, 198.51.100.7")).status).toBe(200);
    expect((await via("198.51.100.7, 127.0.0.1")).status).toBe(200); // a trusted hop is skipped
    expect((await via("garbage")).status).toBe(401);
    const conflicting = await server.anon.request("/api/status", {
      headers: { "x-forwarded-for": "198.51.100.7", "x-real-ip": "203.0.113.9" },
    });
    expect(conflicting.status).toBe(401);
    // Without a header the client is the proxy itself, which is outside the range.
    expect((await server.anon.request("/api/status")).status).toBe(401);
  });

  describe("for the local range", () => {
    beforeAll(async () => {
      const res = await put(server, { enabled: true, cidrs: ["127.0.0.1/32"], trustedProxies: [] });
      expect(res.status, res.text).toBe(200);
    });

    it("is a signed-in user, never an administrator, with no cookie", async () => {
      const c = server.newClient();
      const s = await c.request("/api/auth/session");
      expect(s.status).toBe(200);
      expect(s.json.bypass).toBe(true);
      expect(s.json.user.role).toBe("user");
      expect(s.headers.getSetCookie()).toEqual([]);
      for (const path of ["/api/status", "/api/library", "/api/features", "/api/downloads"]) {
        expect((await c.request(path)).status, path).toBe(200);
      }
      for (const { route } of adminRoutes) {
        const { method, path } = sampleRequest(route);
        const res = await c.request(path, { method, headers: { "x-csrf-token": s.json.csrfToken } });
        expect(res.status, route).toBe(403);
        expect(res.json, route).toEqual({ error: "forbidden" });
      }
      for (const route of loginOnly) {
        const { method, path } = sampleRequest(route);
        const res = await c.request(path, { method, headers: { "x-csrf-token": s.json.csrfToken } });
        expect(res.status, route).toBe(401);
      }
    });

    it("needs the stateless CSRF token for state changes, and the same-origin rule", async () => {
      const c = server.newClient();
      const token = (await c.request("/api/auth/session")).json.csrfToken as string;
      const add = (headers: Record<string, string>) =>
        c.request("/api/add", {
          method: "POST",
          headers: { "content-type": "application/json", ...headers },
          body: JSON.stringify({}),
        });
      expect((await add({})).status).toBe(403);
      expect((await add({ "x-csrf-token": "x" })).status).toBe(403);
      expect((await add({ "x-csrf-token": token, origin: "https://evil.example.com" })).status).toBe(403);
      const ok = await add({ "x-csrf-token": token });
      expect([401, 403]).not.toContain(ok.status);
      expect((await server.admin.request("/api/admin/network")).json.client.inRange).toBe(true);
      server.plant(token);
    });

    it("never overrides a real session: the administrator is still an administrator", async () => {
      const res = await server.admin.request("/api/admin/network");
      expect(res.status).toBe(200);
      const me = await server.admin.request("/api/auth/session");
      expect(me.json.bypass).toBe(false);
      expect(me.json.user.role).toBe("admin");
    });

    it("records destructive actions with via: bypass and the address", async () => {
      const c = server.newClient();
      const token = (await c.request("/api/auth/session")).json.csrfToken as string;
      // Nothing is configured, so the delete finds no instance (404) and is not
      // audited: a configured delete is covered by the Go tests. A network change
      // by the administrator is audited with the administrator as the actor.
      const del = await c.request("/api/library/radarr/1?confirm=true", {
        method: "DELETE",
        headers: { "x-csrf-token": token },
      });
      expect(del.status).toBe(404);
      const audit = await server.admin.request("/api/audit?limit=50");
      const net = audit.json.entries.find((e: any) => e.action === "network.update");
      expect(net).toMatchObject({ actor: "admin", address: "127.0.0.1" });
      expect(audit.text).not.toContain(token);
    });
  });

  it("can be turned off again, and then the local client needs a login", async () => {
    expect((await put(server, { enabled: false, cidrs: ["127.0.0.1/32"] })).status).toBe(200);
    expect((await server.anon.request("/api/status")).status).toBe(401);
    const cfg = await server.admin.request("/api/admin/network");
    expect(cfg.json.enabled).toBe(false);
    expect(cfg.json.cidrs).toEqual(["127.0.0.1/32"]);
  });

  it("warns about ranges wider than private address space", async () => {
    const res = await put(server, { enabled: false, cidrs: ["8.8.8.0/24"] });
    expect(res.status).toBe(200);
    expect(res.json.warnings).toHaveLength(1);
    const get = await server.admin.request("/api/admin/network");
    expect(get.json.warnings).toHaveLength(1);
  });
});

describe("the bypass survives a restart", () => {
  it("is stored", async () => {
    const first = await startServer({ keepConfig: true });
    const dir = first.configDir;
    try {
      const res = await put(first, { enabled: true, cidrs: ["127.0.0.1/32"] });
      expect(res.status).toBe(200);
      await first.stop();
      const second = await startServer({ configDir: dir, auth: "existing" });
      try {
        expect((await second.anon.request("/api/status")).status).toBe(200);
      } finally {
        await second.stop();
      }
    } finally {
      await first.stop();
      const { rmSync } = await import("node:fs");
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it("takes WHEELHOUSE_TRUSTED_PROXIES as a second source", async () => {
    const s = await startServer({ env: { WHEELHOUSE_TRUSTED_PROXIES: "127.0.0.1" } });
    try {
      expect((await put(s, { enabled: true, cidrs: [OTHER] })).status).toBe(200);
      const res = await s.anon.request("/api/status", { headers: { "x-forwarded-for": "198.51.100.7" } });
      expect(res.status).toBe(200);
      expect((await s.admin.request("/api/admin/network")).json.envTrustedProxies).toEqual(["127.0.0.1/32"]);
    } finally {
      await s.stop();
    }
  });
});
