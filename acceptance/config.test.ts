import { existsSync, readFileSync } from "node:fs";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { GOOD, startStack, type Stack } from "./fakes";
import { closedUrl, startServer, type ApiResponse, type RunningServer } from "./harness";

// Stored configuration (Phase 2 item 4): provider instances kept in the
// database with encrypted secrets, edited at runtime through the admin API.

const json = (body: unknown) => ({
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const WRONG_KEY = "canary-wrong-arr-key-5c1d09";
const SECOND_KEY = "canary-second-arr-key-77ab31";

describe("stored configuration", () => {
  let stack: Stack;
  let server: RunningServer;
  const send = (path: string, method: string, body?: unknown) =>
    server.request(path, { method, ...(body === undefined ? {} : json(body)) });
  const status = async (id: string) => {
    const res = await server.request("/api/status");
    return (res.json.instances as { id: string; state: string }[]).find((i) => i.id === id);
  };

  beforeAll(async () => {
    stack = await startStack({ only: ["radarr", "sonarr"] });
    server = await startServer(); // nothing in the environment
    server.plant(GOOD.arr);
    server.plant(WRONG_KEY);
    server.plant(SECOND_KEY);
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  it("lists the provider registry with config schemas and capabilities", async () => {
    const res = await server.request("/api/admin/providers");
    expect(res.status).toBe(200);
    const ids = res.json.providers.map((p: { id: string }) => p.id).sort();
    expect(ids).toEqual(["jellyfin", "plex", "qbittorrent", "radarr", "sabnzbd", "sonarr", "tmdb"]);
    const radarr = res.json.providers.find((p: { id: string }) => p.id === "radarr");
    expect(radarr.role).toBe("mediaManager");
    expect(radarr.configSchema.properties.apiKey["x-secret"]).toBe(true);
    expect(radarr.configSchema.required).toEqual(expect.arrayContaining(["url", "apiKey"]));
    expect(radarr.capabilities).toBeTruthy();
  });

  it("creates, edits and deletes an instance, and the app follows without a restart", async () => {
    expect((await server.request("/api/admin/instances")).json).toEqual({ instances: [] });
    expect(await status("radarr")).toBeUndefined();

    const created = await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      name: "Radarr",
      config: { url: stack.fakes.radarr!.url, apiKey: GOOD.arr },
    });
    expect(created.status, created.text).toBe(201);
    expect(created.json.instance).toMatchObject({
      id: "radarr",
      providerId: "radarr",
      role: "mediaManager",
      source: "database",
      readOnly: false,
      enabled: true,
      status: "ready",
    });
    // The secret is never returned: only that it is set.
    expect(created.json.instance.config).toEqual({ url: stack.fakes.radarr!.url });
    expect(created.json.instance.secrets).toEqual({ apiKey: { set: true } });
    expect(created.raw).not.toContain(GOOD.arr);

    // Seen by the rest of the app on the very next request.
    expect((await status("radarr"))?.state).not.toBe("down");
    const features = (await server.request("/api/features")).json.features;
    expect(features.find((f: { id: string }) => f.id === "library").available).toBe(true);

    // Duplicate id, bad config, unknown provider, unknown setting.
    expect(
      (
        await send("/api/admin/instances", "POST", {
          providerId: "radarr",
          id: "radarr",
          name: "x",
          config: { url: stack.fakes.radarr!.url, apiKey: GOOD.arr },
        })
      ).status
    ).toBe(409);
    const bad = await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      id: "radarr-hd",
      name: "x",
      config: { url: "ftp://nope" },
    });
    expect(bad.status).toBe(400);
    expect(bad.json.fields.map((f: { field: string }) => f.field).sort()).toEqual([
      "apiKey",
      "url",
    ]);
    expect(
      (await send("/api/admin/instances", "POST", { providerId: "nope", name: "x", config: {} }))
        .status
    ).toBe(400);
    expect(
      (
        await send("/api/admin/instances", "POST", {
          providerId: "radarr",
          id: "sonarr-x",
          name: "x",
          config: { url: "http://192.0.2.1", apiKey: "k-0001" },
        })
      ).status
    ).toBe(400);
    const creds = await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      id: "radarr-hd",
      name: "x",
      config: { url: "http://user:pw-secret-77@192.0.2.1", apiKey: "k-00001" },
    });
    expect(creds.status).toBe(400);
    expect(creds.raw).not.toContain("pw-secret-77");

    // A second instance of the same provider.
    const second = await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      id: "radarr-4k",
      name: "Radarr 4K",
      config: { url: stack.fakes.radarr!.url, apiKey: SECOND_KEY },
    });
    expect(second.status, second.text).toBe(201);
    expect((await status("radarr-4k"))?.state).toBe("down"); // the fake does not accept that key

    // Update: an omitted secret keeps the stored one.
    const kept = await send("/api/admin/instances/radarr", "PUT", {
      name: "Movies",
      config: { url: stack.fakes.radarr!.url },
    });
    expect(kept.status, kept.text).toBe(200);
    expect(kept.json.instance.name).toBe("Movies");
    expect(kept.json.instance.secrets.apiKey.set).toBe(true);
    expect((await status("radarr"))?.state).not.toBe("down");
    // An explicit new value replaces it (here: a wrong one, so the service goes down) ...
    const wrong = await send("/api/admin/instances/radarr", "PUT", {
      config: { url: stack.fakes.radarr!.url, apiKey: WRONG_KEY },
    });
    expect(wrong.status).toBe(200);
    expect((await status("radarr"))?.state).toBe("down");
    expect(wrong.raw).not.toContain(WRONG_KEY);
    // ... and the right one brings it back.
    await send("/api/admin/instances/radarr", "PUT", {
      config: { url: stack.fakes.radarr!.url, apiKey: GOOD.arr },
    });
    expect((await status("radarr"))?.state).not.toBe("down");
    // Disabling removes it from the app, enabling restores it.
    expect(
      (await send("/api/admin/instances/radarr", "PUT", { enabled: false })).json.instance.status
    ).toBe("disabled");
    expect(await status("radarr")).toBeUndefined();
    await send("/api/admin/instances/radarr", "PUT", { enabled: true });
    expect((await status("radarr"))?.state).not.toBe("down");

    // The list never carries a secret, whatever is stored.
    const list = await server.request("/api/admin/instances");
    expect(list.json.instances.map((i: { id: string }) => i.id)).toEqual(["radarr", "radarr-4k"]);
    for (const s of [GOOD.arr, WRONG_KEY, SECOND_KEY]) expect(list.raw).not.toContain(s);

    // Delete.
    expect((await send("/api/admin/instances/radarr-4k", "DELETE")).status).toBe(200);
    expect((await send("/api/admin/instances/radarr-4k", "DELETE")).status).toBe(404);
    expect(await status("radarr-4k")).toBeUndefined();
    expect((await send("/api/admin/instances/radarr", "DELETE")).status).toBe(200);
    expect(await status("radarr")).toBeUndefined();
    const after = (await server.request("/api/features")).json.features;
    expect(after.find((f: { id: string }) => f.id === "library").available).toBe(false);
  });

  it("tests a connection on unsaved values, and on a saved instance", async () => {
    const test = (body: unknown) => send("/api/admin/instances/test", "POST", body);
    const url = stack.fakes.radarr!.url;

    const good = await test({ providerId: "radarr", config: { url, apiKey: GOOD.arr } });
    expect(good.status, good.text).toBe(200);
    expect(good.json).toMatchObject({ ok: true });
    expect(good.json.version).toBeTruthy();

    const wrong = await test({ providerId: "radarr", config: { url, apiKey: WRONG_KEY } });
    expect(wrong.json.ok).toBe(false);
    expect(wrong.json.error).toMatch(/API key/i);
    expect(wrong.raw).not.toContain(WRONG_KEY);

    const gone = await test({
      providerId: "radarr",
      config: { url: await closedUrl(), apiKey: GOOD.arr },
    });
    expect(gone.json.ok).toBe(false);
    expect(gone.json.error).toMatch(/refused|unreachable|not found|failed/i);

    // Invalid values are a 400 naming the setting, not a result.
    const invalid = await test({ providerId: "radarr", config: { url: "nope" } });
    expect(invalid.status).toBe(400);
    expect(invalid.raw).not.toContain('nope"');
    expect((await test({ providerId: "nope", config: {} })).status).toBe(400);
    expect((await test({})).status).toBe(400);

    // Saved instance: tested as stored, and with edited values plus the stored key.
    await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      name: "Radarr",
      config: { url, apiKey: GOOD.arr },
    });
    expect((await test({ id: "radarr" })).json.ok).toBe(true);
    expect((await test({ id: "radarr", config: { url } })).json.ok).toBe(true); // stored key filled in
    // An edited address never gets the stored key: the secret must be re-entered, for
    // the connection test and for the update alike (a server at the new address
    // would otherwise be handed the key).
    const elsewhere = await closedUrl();
    const moved = await test({ id: "radarr", config: { url: elsewhere } });
    expect(moved.status).toBe(400);
    expect(moved.raw).toContain("entered again");
    expect(moved.raw).not.toContain(GOOD.arr);
    expect(
      (await test({ id: "radarr", config: { url: elsewhere, apiKey: GOOD.arr } })).json.ok
    ).toBe(false);
    const movedUpdate = await send("/api/admin/instances/radarr", "PUT", {
      config: { url: elsewhere },
    });
    expect(movedUpdate.status).toBe(400);
    expect((await server.request("/api/admin/instances")).json.instances[0].config.url).toBe(url);
    // A URL with a fragment or a query string is refused with a clear message.
    const fragment = await test({
      providerId: "radarr",
      config: { url: url + "#/api", apiKey: GOOD.arr },
    });
    expect(fragment.status).toBe(400);
    expect(fragment.raw).toContain("fragment");
    expect((await test({ id: "nothing-here" })).status).toBe(404);
    const list = await server.request("/api/admin/instances");
    expect(list.json.instances[0].lastTest).toMatchObject({ ok: true });
    await send("/api/admin/instances/radarr", "DELETE");
  });

  it("refuses link-local and metadata addresses and still allows private ranges", async () => {
    const meta = await send("/api/admin/instances/test", "POST", {
      providerId: "radarr",
      config: { url: "http://169.254.169.254", apiKey: "metadata-key-0001" },
    });
    expect(meta.status).toBe(200);
    expect(meta.json.ok).toBe(false);
    expect(meta.json.error).toContain("address not allowed");
    expect(meta.raw).not.toContain("169.254");
    const v6 = await send("/api/admin/instances/test", "POST", {
      providerId: "radarr",
      config: { url: "http://[fe80::1]:7878", apiKey: "metadata-key-0001" },
    });
    expect(v6.json.error).toContain("address not allowed");
    // Loopback (where the fakes listen) is allowed: the tests above rely on it.
  });

  it("toggles features, and a disabled one behaves like an unavailable one", async () => {
    await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      name: "Radarr",
      config: { url: stack.fakes.radarr!.url, apiKey: GOOD.arr },
    });
    const list = await server.request("/api/admin/features");
    expect(list.json.features.map((f: { id: string }) => f.id)).toEqual([
      "dashboard",
      "library",
      "explore",
      "codecs",
    ]);
    expect(list.json.features.every((f: { enabled: boolean }) => f.enabled)).toBe(true);
    expect((await server.request("/api/library")).status).toBe(200);

    expect(
      (await send("/api/admin/features", "PUT", { features: { dashboard: false } })).status
    ).toBe(400);
    expect((await send("/api/admin/features", "PUT", { features: { nope: false } })).status).toBe(
      400
    );
    expect((await send("/api/admin/features", "PUT", { features: {} })).status).toBe(400);

    const off = await send("/api/admin/features", "PUT", { features: { library: false } });
    expect(off.status, off.text).toBe(200);
    const features = (await server.request("/api/features")).json.features;
    const lib = features.find((f: { id: string }) => f.id === "library");
    expect(lib).toMatchObject({ available: false, missing: ["disabled by admin"] });
    expect(features.find((f: { id: string }) => f.id === "dashboard").available).toBe(true);
    expect((await server.request("/api/library")).status).toBe(403);

    await send("/api/admin/features", "PUT", { features: { library: true } });
    expect((await server.request("/api/library")).status).toBe(200);
    await send("/api/admin/instances/radarr", "DELETE");
  });

  it("records every change in the audit log without a secret", async () => {
    const audit = await server.request("/api/audit?limit=200");
    const actions = new Set(audit.json.entries.map((e: { action: string }) => e.action));
    for (const a of [
      "instance.create",
      "instance.update",
      "instance.delete",
      "feature.toggle",
      "instance.test",
    ]) {
      expect(actions.has(a), a).toBe(true);
    }
    for (const s of [GOOD.arr, WRONG_KEY, SECOND_KEY]) expect(audit.raw).not.toContain(s);
    const update = audit.json.entries.find(
      (e: { action: string; detail: { secrets?: string[] } }) =>
        e.action === "instance.update" && e.detail.secrets
    );
    expect(update.detail.secrets).toEqual(["API key"]);
  });

  it("keeps every secret out of responses, logs and the database files", async () => {
    const url = stack.fakes.radarr!.url;
    const planted = [GOOD.arr, WRONG_KEY, SECOND_KEY];
    await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      name: "Radarr",
      config: { url, apiKey: GOOD.arr },
    });
    await send("/api/admin/instances", "POST", {
      providerId: "radarr",
      id: "radarr-4k",
      name: "4K",
      config: { url, apiKey: SECOND_KEY },
    });
    const responses: ApiResponse[] = [
      await server.request("/api/admin/instances"),
      await server.request("/api/admin/providers"),
      await server.request("/api/admin/features"),
      await server.request("/api/status"),
      await server.request("/api/library"),
      await send("/api/admin/instances/test", "POST", { id: "radarr-4k" }),
      await send("/api/admin/instances/radarr", "PUT", { config: { url, apiKey: WRONG_KEY } }),
      await server.request("/api/audit?limit=200"),
    ];
    for (const r of responses) for (const s of planted) expect(r.raw).not.toContain(s);
    for (const s of planted) expect(server.output()).not.toContain(s);
    const files = ["", "-wal", "-shm"]
      .map((x) => server.dbPath + x)
      .filter((f) => existsSync(f))
      .map((f) => readFileSync(f, "latin1"));
    expect(files.length).toBeGreaterThan(0);
    const all = files.join("");
    expect(all).toContain("radarr-4k"); // the scan is not vacuous: the rows are there
    for (const s of planted) expect(all).not.toContain(s);
  });
});

describe("environment instances", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack({ only: ["radarr", "sonarr"] });
    server = await startServer({ env: stack.env });
    server.plant(GOOD.arr);
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });
  const send = (path: string, method: string, body?: unknown) =>
    server.request(path, { method, ...(body === undefined ? {} : json(body)) });

  it("shows them read-only, imports them into the database and then ignores the environment", async () => {
    const list = await server.request("/api/admin/instances");
    const radarr = list.json.instances.find((i: { id: string }) => i.id === "radarr");
    expect(radarr).toMatchObject({ source: "environment", readOnly: true, status: "ready" });
    expect(radarr.secrets).toEqual({ apiKey: { set: true } });
    expect(list.raw).not.toContain(GOOD.arr);

    expect((await send("/api/admin/instances/radarr", "PUT", { name: "x" })).status).toBe(409);
    expect((await send("/api/admin/instances/radarr", "DELETE")).status).toBe(409);
    expect(
      (
        await send("/api/admin/instances", "POST", {
          providerId: "radarr",
          id: "radarr",
          name: "x",
          config: { url: stack.fakes.radarr!.url, apiKey: GOOD.arr },
        })
      ).status
    ).toBe(409);
    expect((await send("/api/admin/instances/nothing/import", "POST")).status).toBe(404);

    const imported = await send("/api/admin/instances/radarr/import", "POST");
    expect(imported.status, imported.text).toBe(201);
    expect(imported.json.instance).toMatchObject({
      source: "database",
      overridesEnvironment: true,
      readOnly: false,
    });
    expect(imported.json.instance.secrets.apiKey.set).toBe(true);
    expect(imported.raw).not.toContain(GOOD.arr);
    expect((await send("/api/admin/instances/radarr/import", "POST")).status).toBe(409);

    // The stored copy now wins: breaking its key takes the service down although
    // the environment still holds the good one.
    const broke = await send("/api/admin/instances/radarr", "PUT", {
      config: { url: stack.fakes.radarr!.url, apiKey: "canary-env-override-0001" },
    });
    expect(broke.status).toBe(200);
    const status = (await server.request("/api/status")).json.instances as {
      id: string;
      state: string;
    }[];
    expect(status.find((i) => i.id === "radarr")?.state).toBe("down");
    expect(status.find((i) => i.id === "sonarr")?.state).not.toBe("down");

    // Deleting the stored copy puts the environment one back in effect.
    const del = await send("/api/admin/instances/radarr", "DELETE");
    expect(del.json).toMatchObject({ ok: true, environmentAgain: true });
    const back = (await server.request("/api/status")).json.instances as {
      id: string;
      state: string;
    }[];
    expect(back.find((i) => i.id === "radarr")?.state).not.toBe("down");

    // The audit log has the import.
    const audit = await server.request("/api/audit?limit=100");
    expect(audit.json.entries.some((e: { action: string }) => e.action === "instance.import")).toBe(
      true
    );
  });
});

describe("environment instances with credentials in the URL", () => {
  it("never show or store the credentials", async () => {
    const stack = await startStack({ only: ["radarr"], urlCredentials: true });
    const server = await startServer({ env: stack.env });
    try {
      const list = await server.request("/api/admin/instances");
      expect(list.status).toBe(200);
      expect(list.json.instances).toHaveLength(1);
      expect(list.json.instances[0].source).toBe("environment");
      expect(list.json.instances[0].status).toBe("invalid-config");
      for (const s of stack.secrets) expect(list.raw).not.toContain(s);
      const imported = await server.request("/api/admin/instances/radarr/import", {
        method: "POST",
      });
      expect(imported.status).toBe(201);
      for (const s of stack.secrets) expect(imported.raw).not.toContain(s);
      const files = ["", "-wal"]
        .map((x) => server.dbPath + x)
        .filter((f) => existsSync(f))
        .map((f) => readFileSync(f, "latin1"))
        .join("");
      expect(files).toContain("radarr");
      for (const s of stack.secrets) expect(files).not.toContain(s);
    } finally {
      await server.stop();
      await stack.close();
    }
  });
});
