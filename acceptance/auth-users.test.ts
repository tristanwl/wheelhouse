import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { ADMIN, startServer, type Client, type RunningServer } from "./harness";

const json = (method: string, body: unknown) => ({
  method,
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const PW = "second-user-passphrase-01";
const PW2 = "second-user-passphrase-02";

describe("accounts and the audit log", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer();
    server.plant(PW);
    server.plant(PW2);
  });
  afterAll(async () => {
    await server?.stop();
  });

  const admin = () => server.admin;
  const adminId = async () =>
    ((await admin().request("/api/users")).json.users as { id: string; username: string }[]).find(
      (u) => u.username === ADMIN.username
    )!.id;

  async function newUser(name: string, role: "admin" | "user", mustChange = false) {
    const res = await admin().request(
      "/api/users",
      json("POST", { username: name, password: PW, role, mustChangePassword: mustChange })
    );
    expect(res.status, res.text).toBe(201);
    return res.json.user as { id: string; username: string; role: string };
  }
  async function signIn(name: string, password = PW): Promise<Client> {
    const c = server.newClient();
    const res = await c.login(name, password);
    expect(res.status, res.text).toBe(200);
    return c;
  }

  it("creates, lists, renames, re-roles, disables and deletes accounts, case-insensitively unique", async () => {
    const bob = await newUser("Bob-Example", "user", true);
    expect(bob.role).toBe("user");
    expect(
      (
        await admin().request(
          "/api/users",
          json("POST", { username: "bob-example", password: PW, role: "user" })
        )
      ).status
    ).toBe(409);

    const list = (await admin().request("/api/users")).json.users as { username: string }[];
    expect(list.map((u) => u.username)).toContain("Bob-Example");
    const raw = (await admin().request("/api/users")).text;
    expect(raw).not.toMatch(/argon2|hash|password_hash/i);

    const renamed = await admin().request(
      `/api/users/${bob.id}`,
      json("PATCH", { username: "bobby-example" })
    );
    expect(renamed.status, renamed.text).toBe(200);
    expect(renamed.json.user.username).toBe("bobby-example");

    const session = await signIn("bobby-example");
    expect((await session.request("/api/status")).status).toBe(403); // must change first
    // Disabling ends his sessions at once and stops logins.
    expect(
      (await admin().request(`/api/users/${bob.id}`, json("PATCH", { disabled: true }))).status
    ).toBe(200);
    expect((await session.request("/api/auth/session")).status).toBe(401);
    expect((await server.newClient().login("bobby-example", PW)).status).toBe(401);
    expect(
      (await admin().request(`/api/users/${bob.id}`, json("PATCH", { disabled: false }))).status
    ).toBe(200);
    expect((await server.newClient().login("bobby-example", PW)).status).toBe(200);

    expect((await admin().request(`/api/users/${bob.id}`, { method: "DELETE" })).status).toBe(200);
    expect((await admin().request(`/api/users/${bob.id}`, { method: "DELETE" })).status).toBe(404);
    expect((await server.newClient().login("bobby-example", PW)).status).toBe(401);
  });

  it("validates input strictly", async () => {
    const post = (b: unknown) => admin().request("/api/users", json("POST", b));
    for (const body of [
      { username: "ab", password: PW, role: "user" },
      { username: "bad name", password: PW, role: "user" },
      { username: "valid-name", password: "short", role: "user" },
      { username: "valid-name", password: "password1234", role: "user" },
      { username: "valid-name", password: PW, role: "owner" },
      { username: "valid-name", password: PW },
      { username: "valid-name", password: PW, role: "user", extra: 1 },
      { username: 5, password: PW, role: "user" },
      [],
    ]) {
      const res = await post(body);
      expect(res.status, JSON.stringify(body)).toBe(400);
      expect(res.json.error).toBeTypeOf("string");
    }
    const bad = await admin().request("/api/users", {
      method: "POST",
      headers: { "content-type": "text/plain" },
      body: "{}",
    });
    expect(bad.status).toBe(415);
    const huge = await admin().request("/api/users", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ username: "a".repeat(20_000) }),
    });
    expect(huge.status).toBe(413);
    // The password is at most 1024 bytes.
    expect(
      (await post({ username: "valid-name", password: "p".repeat(1025) + "q", role: "user" }))
        .status
    ).toBe(400);
    expect(
      (await admin().request("/api/users/not-an-id", json("PATCH", { disabled: true }))).status
    ).toBe(404);
    expect(
      (await admin().request(`/api/users/${"0".repeat(32)}`, json("PATCH", { disabled: true })))
        .status
    ).toBe(404);
  });

  it("never lets the last administrator be deleted, demoted or disabled", async () => {
    const id = await adminId();
    expect((await admin().request(`/api/users/${id}`, { method: "DELETE" })).status).toBe(409);
    expect(
      (await admin().request(`/api/users/${id}`, json("PATCH", { role: "user" }))).status
    ).toBe(409);
    expect(
      (await admin().request(`/api/users/${id}`, json("PATCH", { disabled: true }))).status
    ).toBe(409);
    expect((await admin().request("/api/status")).status).toBe(200);

    // With a second administrator one may go, but then the other is the last.
    const second = await newUser("second-admin", "admin");
    expect(
      (await admin().request(`/api/users/${second.id}`, json("PATCH", { role: "user" }))).status
    ).toBe(200);
    expect((await admin().request(`/api/users/${id}`, { method: "DELETE" })).status).toBe(409);
    expect((await admin().request(`/api/users/${second.id}`, { method: "DELETE" })).status).toBe(
      200
    );
  });

  it("does not let a user become an administrator, create one, or read accounts and the audit log", async () => {
    const carol = await newUser("carol-example", "user");
    const c = await signIn("carol-example");
    for (const [method, path, body] of [
      ["PATCH", `/api/users/${carol.id}`, { role: "admin" }],
      ["POST", "/api/users", { username: "evil-admin", password: PW, role: "admin" }],
      ["POST", `/api/users/${carol.id}/password`, { password: PW2 }],
      ["DELETE", `/api/users/${await adminId()}`, undefined],
      ["GET", "/api/users", undefined],
      ["GET", "/api/audit", undefined],
      ["GET", `/api/users/${await adminId()}/sessions`, undefined],
      ["DELETE", `/api/users/${await adminId()}/sessions`, undefined],
    ] as [string, string, unknown][]) {
      const res = await c.request(path, body === undefined ? { method } : json(method, body));
      expect(res.status, `${method} ${path}`).toBe(403);
    }
    const users = (await admin().request("/api/users")).json.users as {
      username: string;
      role: string;
    }[];
    expect(users.find((u) => u.username === "carol-example")!.role).toBe("user");
    expect(users.some((u) => u.username === "evil-admin")).toBe(false);
    // But a user can see and end their own sessions, and change their own password.
    expect((await c.request("/api/auth/sessions")).status).toBe(200);
  });

  it("lets an administrator list and end another user's sessions", async () => {
    const dave = await newUser("dave-example", "user");
    const d1 = await signIn("dave-example");
    const d2 = await signIn("dave-example");
    const list = await admin().request(`/api/users/${dave.id}/sessions`);
    expect(list.status).toBe(200);
    expect(list.json.sessions).toHaveLength(2);
    expect(list.text).not.toContain(d1.cookie!.value);
    const del = await admin().request(`/api/users/${dave.id}/sessions`, { method: "DELETE" });
    expect(del.json).toEqual({ revoked: 2 });
    for (const d of [d1, d2]) expect((await d.request("/api/auth/session")).status).toBe(401);
  });

  it("lets an administrator end one session of another user, and only that one", async () => {
    const hana = await newUser("hana-example", "user");
    const e1 = await signIn("hana-example");
    const e2 = await signIn("hana-example");
    const other = await newUser("gina-example", "user");
    const list = await admin().request(`/api/users/${hana.id}/sessions`);
    const ids = (list.json.sessions as { id: string }[]).map((s) => s.id);
    expect(ids).toHaveLength(2);

    // A session id under the wrong user is not found, and ends nothing.
    expect(
      (await admin().request(`/api/users/${other.id}/sessions/${ids[0]}`, { method: "DELETE" }))
        .status
    ).toBe(404);
    expect((await e1.request("/api/auth/session")).status).toBe(200);
    expect((await e2.request("/api/auth/session")).status).toBe(200);

    expect(
      (await admin().request(`/api/users/${hana.id}/sessions/${ids[0]}`, { method: "DELETE" }))
        .status
    ).toBe(200);
    const alive = [e1, e2].filter((c) => c.cookie);
    const statuses = await Promise.all(alive.map((c) => c.request("/api/auth/session")));
    expect(statuses.map((r) => r.status).sort()).toEqual([200, 401]);
    expect(
      (await admin().request(`/api/users/${hana.id}/sessions/${ids[0]}`, { method: "DELETE" }))
        .status
    ).toBe(404);
    const audit = await admin().request("/api/audit?limit=20");
    expect(
      (audit.json.entries as { action: string; target: string }[]).some(
        (e) => e.action === "session.revoke" && e.target === ids[0]
      )
    ).toBe(true);
  });

  it("an administrator's reset forces a new password at the next login", async () => {
    const erin = await newUser("erin-example", "user");
    const old = await signIn("erin-example");
    const reset = await admin().request(
      `/api/users/${erin.id}/password`,
      json("POST", { password: PW2 })
    );
    expect(reset.status, reset.text).toBe(200);
    expect((await old.request("/api/auth/session")).status).toBe(401);
    expect((await server.newClient().login("erin-example", PW)).status).toBe(401);

    const c = server.newClient();
    const login = await c.login("erin-example", PW2);
    expect(login.status).toBe(200);
    expect(login.json.user.mustChangePassword).toBe(true);
    // Nothing but the password change (and reading the session) works.
    const blocked = await c.request("/api/status");
    expect(blocked.status).toBe(403);
    expect(blocked.json.code).toBe("password-change-required");
    expect((await c.request("/api/auth/session")).status).toBe(200);
    const change = await c.request(
      "/api/auth/password",
      json("POST", { currentPassword: PW2, newPassword: "erin-chosen-passphrase-1" })
    );
    expect(change.status, change.text).toBe(200);
    server.plant("erin-chosen-passphrase-1");
    c.cookie = {
      name: "wheelhouse",
      value: change.headers.getSetCookie()[0].split(";")[0].split("=")[1],
    };
    c.csrf = change.json.csrfToken;
    expect((await c.request("/api/status")).status).toBe(200);
  });

  it("wheelhouse reset-password prints a one-time password and locks out nobody else", async () => {
    const frank = await newUser("frank-example", "user");
    const live = await signIn("frank-example");
    // Lock frank's account first: a reset from the CLI must lift that too.
    for (let i = 0; i < 5; i++) {
      await server.anon.request(
        "/api/auth/login",
        json("POST", { username: "frank-example", password: `wrong-password-${i}` })
      );
    }
    expect((await server.newClient().login("frank-example", PW)).status).toBe(401);

    const bad = await server.cli(["reset-password", "no-such-user"]);
    expect(bad.code).not.toBe(0);
    expect(bad.stderr).toMatch(/no user named/);

    await new Promise((r) => setTimeout(r, 1100)); // the reset must be later than the failures
    const out = await server.cli(["reset-password", "frank-example"]);
    expect(out.code, out.stderr).toBe(0);
    const m = out.stdout.match(/\n {4}(\S{20})\n/);
    expect(m, out.stdout).not.toBeNull();
    const oneTime = m![1];
    server.plant(oneTime);

    expect((await live.request("/api/auth/session")).status).toBe(401);
    const c = server.newClient();
    const login = await c.login("frank-example", oneTime);
    expect(login.status, login.text).toBe(200);
    expect(login.json.user.mustChangePassword).toBe(true);
    expect((await c.request("/api/status")).status).toBe(403);
    expect((await server.newClient().login("frank-example", PW)).status).toBe(401);

    // The password is not in the log of the running server, nor in the audit log, which names the CLI.
    expect(server.output()).not.toContain(oneTime);
    const audit = await admin().request("/api/audit?limit=200");
    expect(audit.text).not.toContain(oneTime);
    const entry = (audit.json.entries as { action: string; actor: string; target: string }[]).find(
      (e) => e.action === "password.reset" && e.target === "frank-example"
    );
    expect(entry?.actor).toBe("cli");
    void frank;
  });

  it("records the security-relevant events, with the acting user and no secrets", async () => {
    const audit = await admin().request("/api/audit?limit=200");
    expect(audit.status).toBe(200);
    const entries = audit.json.entries as {
      id: number;
      at: string;
      actor: string;
      action: string;
      target: string;
      address: string;
    }[];
    const actions = new Set(entries.map((e) => e.action));
    for (const a of [
      "setup",
      "login.ok",
      "user.create",
      "user.update",
      "user.delete",
      "user.role",
      "password.reset",
      "session.revokeAll",
    ]) {
      expect(actions, `no ${a} entry`).toContain(a);
    }
    const created = entries.find((e) => e.action === "user.create")!;
    expect(created.actor).toBe(ADMIN.username);
    expect(created.address).toBe("127.0.0.1");
    expect(Date.parse(created.at)).toBeGreaterThan(Date.now() - 600_000);
    for (const secret of server.planted())
      expect(audit.text, "audit holds a secret").not.toContain(secret);

    // Newest first, and paged.
    expect(entries.map((e) => e.id)).toEqual([...entries.map((e) => e.id)].sort((a, b) => b - a));
    const page = await admin().request("/api/audit?limit=3");
    expect(page.json.entries).toHaveLength(3);
    const next = await admin().request(`/api/audit?limit=3&before=${page.json.nextBefore}`);
    expect(next.json.entries[0].id).toBeLessThan(page.json.entries[2].id);
    expect((await admin().request("/api/audit?limit=0")).status).toBe(400);
    expect((await admin().request("/api/audit?limit=abc")).status).toBe(400);
  });

  it("the audit table cannot be edited through the API (there is no such route)", async () => {
    for (const method of ["PUT", "PATCH", "DELETE", "POST"]) {
      const res = await admin().request("/api/audit", { method });
      expect(res.status, method).toBeGreaterThanOrEqual(400);
      expect(res.status).toBeLessThan(500);
    }
  });
});
