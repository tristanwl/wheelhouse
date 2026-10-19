import { randomBytes } from "node:crypto";
import { chmodSync, existsSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import {
  ADMIN,
  runCommand,
  startExpectingFailure,
  startServer,
  type RunningServer,
} from "./harness";
import { PAGES, ROUTE_LEVELS, sampleRequest } from "./routes";

const json = (body: unknown) => ({
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

// Acceptance criterion 1: a fresh container serves only setup until an admin
// exists, and the setup token is required.
describe("a fresh server", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer({ auth: "none" });
  });
  afterAll(async () => {
    await server?.stop();
  });

  it("answers health, the pages and the setup status, and nothing else", async () => {
    expect((await server.anon.request("/healthz")).status).toBe(200);
    for (const { path: p } of PAGES) {
      const res = await server.anon.request(p);
      expect(res.status, p).toBe(200);
      expect(res.headers.get("content-type")).toMatch(/text\/html/);
    }
    const status = await server.anon.request("/api/auth/setup");
    expect(status.status).toBe(200);
    expect(status.json).toEqual({ setupRequired: true });
    // Unknown API paths are still a JSON 404.
    expect((await server.anon.request("/api/nope")).status).toBe(404);
  });

  it("answers 503 setup required on every other route, with or without credentials", async () => {
    const guarded = ROUTE_LEVELS.filter((r) => r.level !== "public");
    expect(guarded.length).toBeGreaterThan(30);
    for (const { route } of guarded) {
      const { method, path: p } = sampleRequest(route);
      const res = await server.anon.request(p, {
        method,
        headers: { cookie: "wheelhouse=" + "A".repeat(43), "x-csrf-token": "x" },
      });
      expect(res.status, route).toBe(503);
      expect(res.json, route).toEqual({ error: "setup required" });
    }
    // Login has nobody to log in.
    expect((await server.anon.login(ADMIN.username, ADMIN.password)).status).toBe(503);
  });

  it("leaves a mode 0600 setup token file, and logs the token exactly once", async () => {
    const file = path.join(server.configDir, "setup-token");
    expect(existsSync(file)).toBe(true);
    expect(statSync(file).mode & 0o777).toBe(0o600);
    const token = readFileSync(file, "utf8").trim();
    expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(server.output().split(token).length - 1).toBe(1);
  });

  it("refuses setup without the token, with a wrong one, and with a hostile origin", async () => {
    const body = { username: "mallory", password: "mallory-long-passphrase-9" };
    for (const [name, extra] of Object.entries({
      "no token": {},
      "empty token": { token: "" },
      "wrong token": { token: "x".repeat(43) },
    })) {
      const res = await server.anon.request("/api/auth/setup", json({ ...body, ...extra }));
      expect(res.status, name).toBe(403);
      expect(res.json.error).toBe("invalid setup token");
    }
    const token = readFileSync(path.join(server.configDir, "setup-token"), "utf8").trim();
    const foreign = await server.anon.request("/api/auth/setup", {
      ...json({ ...body, token }),
      headers: { "content-type": "application/json", origin: "https://evil.example" },
    });
    expect(foreign.status).toBe(403);
    expect((await server.anon.request("/api/auth/setup")).json).toEqual({ setupRequired: true });
  });

  it("rejects a weak password or bad username without spending the token", async () => {
    const token = readFileSync(path.join(server.configDir, "setup-token"), "utf8").trim();
    for (const body of [
      { token, username: "boss", password: "short" },
      { token, username: "boss", password: "password1234" },
      { token, username: "boss", password: "boss" },
      { token, username: "bad name", password: ADMIN.password },
      { token, username: "boss", password: ADMIN.password, role: "user" },
    ]) {
      const res = await server.anon.request("/api/auth/setup", json(body));
      expect(res.status, JSON.stringify(body)).toBe(400);
    }
    expect(existsSync(path.join(server.configDir, "setup-token"))).toBe(true);
  });

  it("creates the first administrator once, deletes the token, and then serves normally", async () => {
    const file = path.join(server.configDir, "setup-token");
    const token = readFileSync(file, "utf8").trim();
    const res = await server.anon.request(
      "/api/auth/setup",
      json({ token, username: ADMIN.username, password: ADMIN.password })
    );
    expect(res.status, res.text).toBe(201);
    expect(res.json.user).toMatchObject({ username: ADMIN.username, role: "admin" });
    expect(res.text).not.toContain(token);
    expect(res.headers.getSetCookie()).toEqual([]); // setup does not sign anyone in
    expect(existsSync(file)).toBe(false);

    // Setup is over for everyone, token or not.
    const again = await server.anon.request(
      "/api/auth/setup",
      json({ token, username: "second", password: "second-long-passphrase-9" })
    );
    expect(again.status).toBe(409);
    expect((await server.anon.request("/api/auth/setup")).json).toEqual({ setupRequired: false });

    // Guarded routes are now 401, not 503, and login works.
    expect((await server.anon.request("/api/status")).status).toBe(401);
    expect((await server.admin.login(ADMIN.username, ADMIN.password)).status).toBe(200);
    expect((await server.admin.request("/api/status")).status).toBe(200);
    expect(server.output()).not.toContain(ADMIN.password);
  });
});

describe("restarting over an existing database", () => {
  it("keeps accounts and sessions and makes no setup token", async () => {
    const first = await startServer({ keepConfig: true });
    const configDir = first.configDir;
    const cookie = first.admin.cookie!;
    const csrf = first.admin.csrf!;
    await first.stop();

    const second = await startServer({ auth: "none", configDir });
    try {
      expect(existsSync(path.join(configDir, "setup-token"))).toBe(false);
      expect(second.output()).not.toMatch(/setup/i);
      expect((await second.anon.request("/api/auth/setup")).json).toEqual({ setupRequired: false });
      // The session is in the database, so it is still good.
      const res = await second.anon.request("/api/auth/session", {
        headers: { cookie: `${cookie.name}=${cookie.value}`, "x-csrf-token": csrf },
      });
      expect(res.status).toBe(200);
      expect(res.json.user.username).toBe(ADMIN.username);
      expect((await second.admin.login(ADMIN.username, ADMIN.password)).status).toBe(200);
    } finally {
      await second.stop();
      rmSync(configDir, { recursive: true, force: true });
    }
  });
});

// Startup problems are fatal and say what to do.
describe("a server that cannot start", () => {
  it("refuses a database file that others can read", async () => {
    const r = await startExpectingFailure({
      prepare: (dir) => {
        const db = path.join(dir, "wheelhouse.db");
        writeFileSync(db, "");
        chmodSync(db, 0o644);
      },
    });
    expect(r.code).not.toContain("code 0");
    expect(r.output).toMatch(/mode 0644/);
    expect(r.output).toMatch(/chmod 600/);
  });

  it("refuses a missing or unusable key file instead of making a new key", async () => {
    const r = await startExpectingFailure({
      env: { WHEELHOUSE_SECRET_KEY_FILE: "/nonexistent/wheelhouse.key" },
    });
    expect(r.code).not.toContain("code 0");
    expect(r.output).toContain("WHEELHOUSE_SECRET_KEY_FILE");
  });

  it("refuses a malformed trusted proxy list", async () => {
    const r = await startExpectingFailure({
      env: { WHEELHOUSE_TRUSTED_PROXIES: "not-an-address" },
    });
    expect(r.code).not.toContain("code 0");
    expect(r.output).toContain("WHEELHOUSE_TRUSTED_PROXIES");
  });

  it("refuses a config directory that does not exist", async () => {
    const r = await startExpectingFailure({
      env: { WHEELHOUSE_CONFIG_DIR: "/nonexistent/wheelhouse-config" },
      configDir: "/nonexistent/wheelhouse-config",
    });
    expect(r.code).not.toContain("code 0");
    expect(r.output).toMatch(/does not exist/);
  });
});

// docs/operations.md, "Key loss": the master key is not in backups. Without it
// the server refuses to start (it never invents a new key for an old database),
// `forget-secrets --yes` discards the stored provider credentials, and accounts
// and sessions are untouched because they never depended on the key.
describe("a master key that is lost", () => {
  it("stops the server with a clear message, and forget-secrets lets it start with the accounts intact", async () => {
    const first = await startServer({ keepConfig: true });
    const dir = first.configDir;
    const cookie = first.admin.cookie!;
    const csrf = first.admin.csrf!;
    await first.stop();

    // What a restore without secret.key looks like: a different key.
    writeFileSync(path.join(dir, "secret.key"), randomBytes(32), { mode: 0o600 });
    try {
      const failed = await startExpectingFailure({ configDir: dir });
      expect(failed.code).not.toContain("code 0");
      expect(failed.output).toMatch(/does not match this database/);
      expect(failed.output).toContain("forget-secrets");
      expect(failed.output).toContain("operations.md");

      // Discarding secrets needs the explicit flag.
      const bare = await runCommand(["forget-secrets"], { configDir: dir });
      expect(bare.code).not.toBe(0);
      expect(bare.stderr).toMatch(/usage/);
      const done = await runCommand(["forget-secrets", "--yes"], { configDir: dir });
      expect(done.code, done.stderr).toBe(0);
      expect(done.stdout).toMatch(/Discarded \d+ stored secret/);

      const second = await startServer({ auth: "existing", configDir: dir });
      try {
        expect((await second.admin.request("/api/status")).status).toBe(200);
        // A session from before the loss still works: it never needed the key.
        const old = await second.anon.request("/api/auth/session", {
          headers: { cookie: `${cookie.name}=${cookie.value}`, "x-csrf-token": csrf },
        });
        expect(old.status).toBe(200);
      } finally {
        await second.stop();
      }

      // With a working key there is nothing to forget, and nothing is discarded.
      const again = await runCommand(["forget-secrets", "--yes"], { configDir: dir });
      expect(again.code).not.toBe(0);
      expect(again.stderr).toMatch(/nothing to forget/);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
