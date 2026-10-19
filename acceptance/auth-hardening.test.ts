import http from "node:http";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { ADMIN, startServer, type RunningServer } from "./harness";

// Security review findings (Phase 2 item 10): the login throttle holds against a
// parallel burst, the bypass needs an acceptable Host (DNS rebinding), and a few
// header and file exposures are closed.

const json = (body: unknown) => ({
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

/** A raw request where the test chooses Host (fetch may not let it). */
function raw(
  server: RunningServer,
  path: string,
  headers: Record<string, string>
): Promise<{ status: number; body: string; headers: http.IncomingHttpHeaders }> {
  const url = new URL(server.baseUrl);
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: url.hostname, port: url.port, path, method: "GET", headers },
      (res) => {
        let body = "";
        res.on("data", (c) => (body += c));
        res.on("end", () => resolve({ status: res.statusCode ?? 0, body, headers: res.headers }));
      }
    );
    req.on("error", reject);
    req.end();
  });
}

describe("login throttle against a parallel burst", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer();
  });
  afterAll(async () => {
    await server?.stop();
  });

  it("verifies only the allowed number of guesses, then refuses the right password", async () => {
    const guesses = Array.from({ length: 99 }, (_, i) =>
      server
        .newClient()
        .request(
          "/api/auth/login",
          json({ username: ADMIN.username, password: `wrong-guess-${i}-xxxx` })
        )
    );
    const right = server.newClient().request("/api/auth/login", json(ADMIN));
    const results = await Promise.all([...guesses, right]);
    expect(results.every((r) => r.status === 401 || r.status === 200)).toBe(true);
    // At most 5 verifications were possible, so at most 5 of the 100 could even
    // have been checked; the right one succeeds only if it was one of them.
    const ok = results.filter((r) => r.status === 200).length;
    expect(ok).toBeLessThanOrEqual(1);
    // After the burst the account is locked: the right password is refused.
    const after = await server.newClient().request("/api/auth/login", json(ADMIN));
    if (ok === 0) expect(after.status).toBe(401);
    // One failure row per verified failure and one lockout row, not one per guess.
    const audit = await server.admin.request("/api/audit?limit=200");
    if (audit.status === 200) {
      const fails = audit.json.entries.filter((e: any) => e.action === "login.fail");
      const locks = audit.json.entries.filter((e: any) => e.action === "login.lockout");
      expect(fails.length).toBeLessThanOrEqual(5);
      expect(locks.length).toBeLessThanOrEqual(2);
    }
  });
});

describe("DNS rebinding and the local-network bypass", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer();
    const res = await server.admin.request("/api/admin/network", {
      method: "PUT",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ enabled: true, cidrs: ["127.0.0.1/32"] }),
    });
    expect(res.status, res.text).toBe(200);
    // The admin reached the server by IP, which is always allowed: nothing to add.
    expect(res.json.addedHost).toBe("");
  });
  afterAll(async () => {
    await server?.stop();
  });

  it("refuses a spoofed Host and Origin pair with the plain anonymous 401", async () => {
    const port = new URL(server.baseUrl).port;
    const evil = `rebind.attacker.example:${port}`;
    const spoofed = await raw(server, "/api/auth/session", {
      host: evil,
      origin: `http://${evil}`,
    });
    const plain = await server.anon.request("/api/status", {
      headers: {},
    });
    expect(spoofed.status).toBe(401);
    expect(JSON.parse(spoofed.body)).toEqual({ error: "authentication required" });
    expect(plain.status).toBe(200); // by IP, the bypass works
  });

  it("works for IP, localhost, single-label and listed names", async () => {
    const port = new URL(server.baseUrl).port;
    for (const host of [`127.0.0.1:${port}`, `localhost:${port}`, `nas:${port}`]) {
      const res = await raw(server, "/api/status", { host });
      expect(res.status, host).toBe(200);
    }
    expect((await raw(server, "/api/status", { host: `nas.example.lan:${port}` })).status).toBe(
      401
    );
    const put = await server.admin.request("/api/admin/network", {
      method: "PUT",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        enabled: true,
        cidrs: ["127.0.0.1/32"],
        allowedHosts: ["nas.example.lan"],
      }),
    });
    expect(put.status, put.text).toBe(200);
    expect((await raw(server, "/api/status", { host: `nas.example.lan:${port}` })).status).toBe(
      200
    );
    const refused = await server.admin.request("/api/admin/network", {
      method: "PUT",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ enabled: true, cidrs: ["127.0.0.1/32"], allowedHosts: ["*"] }),
    });
    expect(refused.status).toBe(400);
  });
});

describe("headers and embedded files", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer();
  });
  afterAll(async () => {
    await server?.stop();
  });

  it("serves no dot files from the embedded UI", async () => {
    for (const p of ["/.gitkeep", "/.gitignore"]) {
      expect((await server.anon.request(p)).status, p).toBe(404);
    }
  });

  it("sends HSTS only for HTTPS requests (not on plain HTTP, not from an untrusted proxy header)", async () => {
    const plain = await server.anon.request("/healthz");
    expect(plain.headers.get("strict-transport-security")).toBeNull();
    const spoof = await server.anon.request("/healthz", {
      headers: { "x-forwarded-proto": "https" },
    });
    expect(spoof.headers.get("strict-transport-security")).toBeNull();
  });

  it("does not let an account take the name of an audit actor", async () => {
    for (const username of ["local-network", "CLI", "system", "Anonymous"]) {
      const res = await server.admin.request(
        "/api/users",
        json({ username, password: "reserved-name-passphrase-01", role: "user" })
      );
      expect(res.status, username).toBe(400);
    }
  });
});
