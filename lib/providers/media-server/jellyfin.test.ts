import { describe, expect, it } from "vitest";
import { createRegistry } from "../core/registry";
import { createProviderRuntime } from "../core/runtime";
import { describeMediaServerContract } from "./contract";
import { GOOD_KEY, startFakeJellyfin, type JellyfinScenario } from "./__fixtures__/jellyfin/server";
import { jellyfinProvider } from "./jellyfin";
import type { MediaServer } from "./types";

const VERSION = "12.2.0";

// Pass null for "no key": an explicit undefined would trigger the default.
async function clientFor(scenario: JellyfinScenario, apiKey: string | null = GOOD_KEY) {
  const server = await startFakeJellyfin(scenario);
  const client = jellyfinProvider.create(
    { url: server.url, apiKey: apiKey ?? undefined },
    { instanceId: "jellyfin", instanceName: "Jellyfin" }
  );
  return { client, server };
}

describeMediaServerContract("jellyfin", async () => {
  const { client, server } = await clientFor({ setupCompleted: true, sessions: "playing" });
  return { client, teardown: server.close };
});

describe("jellyfin adapter: status", () => {
  it("reports name, version and active sessions for a healthy, authenticated server", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "playing" });
    expect(await client.getStatus()).toEqual({
      state: "ok",
      name: "Example Jellyfin",
      version: VERSION,
      activeSessions: 1,
      authenticated: true,
    });
    await server.close();
  });

  it("reports zero sessions when nobody is connected", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "none" });
    expect((await client.getStatus()).activeSessions).toBe(0);
    await server.close();
  });

  it("does not count a connected client that isn't playing anything", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "idle" });
    expect((await client.getStatus()).activeSessions).toBe(0);
    await server.close();
  });

  it("counts only the playing session when one is idle and one is playing", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "mixed" });
    expect((await client.getStatus()).activeSessions).toBe(1);
    await server.close();
  });

  it("needs setup when the wizard hasn't been completed, without asking for a key", async () => {
    const { client, server } = await clientFor({ setupCompleted: false, sessions: "none" }, null);
    const status = await client.getStatus();
    expect(status).toMatchObject({ state: "needs-setup", authenticated: false, version: VERSION });
    expect(status.notice).toMatch(/setup isn't finished/i);
    expect(server.requests.map((r) => r.path)).toEqual(["/System/Info/Public"]);
    await server.close();
  });

  it("is ok but unauthenticated when set up and no key is configured", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "playing" }, null);
    const status = await client.getStatus();
    expect(status).toMatchObject({ state: "ok", authenticated: false, version: VERSION });
    expect(status.activeSessions).toBeUndefined();
    expect(status.notice).toMatch(/no jellyfin api key/i);
    await server.close();
  });

  it("says the key was rejected (HTTP 401) instead of showing zero sessions", async () => {
    const { client, server } = await clientFor(
      { setupCompleted: true, sessions: "playing" },
      "stale-key-value"
    );
    const status = await client.getStatus();
    expect(status).toMatchObject({ state: "ok", authenticated: false });
    expect(status.activeSessions).toBeUndefined();
    expect(status.notice).toMatch(/api key was rejected/i);
    expect(JSON.stringify(status)).not.toContain("stale-key-value");
    await server.close();
  });

  it("keeps the server 'ok' when details fail for another reason", async () => {
    const { client, server } = await clientFor({
      setupCompleted: true,
      sessions: "none",
      authedFailure: 500,
    });
    const status = await client.getStatus();
    expect(status.state).toBe("ok");
    expect(status.authenticated).toBe(false);
    expect(status.notice).toMatch(/couldn't read jellyfin details/i);
    await server.close();
  });

  it("sends the key in the Authorization header only, and never to the public endpoint", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "none" });
    await client.getStatus();
    const publicCall = server.requests.find((r) => r.path === "/System/Info/Public");
    expect(publicCall?.key).toBeUndefined();
    const others = server.requests.filter((r) => r.path !== "/System/Info/Public");
    expect(others.length).toBe(2);
    expect(others.every((r) => r.key === GOOD_KEY && r.embyToken === undefined)).toBe(true);
    await server.close();
  });

  it("throws when the server can't be reached at all", async () => {
    const dead = await startFakeJellyfin({ setupCompleted: true, sessions: "none" });
    const url = dead.url;
    await dead.close();
    const client = jellyfinProvider.create(
      { url, apiKey: GOOD_KEY },
      { instanceId: "jellyfin", instanceName: "Jellyfin" }
    );
    await expect(client.getStatus()).rejects.toThrow("jellyfin: connection refused");
  });
});

describe("jellyfin adapter: testConnection", () => {
  it("ok with a good key", async () => {
    const { client, server } = await clientFor({ setupCompleted: true, sessions: "none" });
    expect(await client.testConnection()).toEqual({ ok: true, version: VERSION });
    await server.close();
  });

  it("ok without a key (the server is reachable)", async () => {
    const { client, server } = await clientFor({ setupCompleted: false, sessions: "none" }, null);
    expect((await client.testConnection()).ok).toBe(true);
    await server.close();
  });

  it("fails clearly with a rejected key", async () => {
    const { client, server } = await clientFor(
      { setupCompleted: true, sessions: "none" },
      "wrong-key-value"
    );
    expect(await client.testConnection()).toEqual({
      ok: false,
      version: VERSION,
      error: "jellyfin: API key rejected",
    });
    await server.close();
  });

  it("fails clearly when unreachable", async () => {
    const dead = await startFakeJellyfin({ setupCompleted: true, sessions: "none" });
    const url = dead.url;
    await dead.close();
    const client = jellyfinProvider.create(
      { url },
      { instanceId: "jellyfin", instanceName: "Jellyfin" }
    );
    expect(await client.testConnection()).toEqual({
      ok: false,
      error: "jellyfin: connection refused",
    });
  });
});

describe("jellyfin provider through the runtime", () => {
  const registry = createRegistry([jellyfinProvider]);

  it("accepts a missing key and rejects a missing URL", async () => {
    const make = (config: unknown) =>
      createProviderRuntime({
        registry,
        source: {
          getInstances: async () => [
            { id: "jellyfin", providerId: "jellyfin", name: "Jellyfin", enabled: true, config },
          ],
        },
      });
    expect((await make({ url: "http://jellyfin:8096" }).listInstances())[0].status).toBe("ready");
    const bad = (await make({ apiKey: "k" }).listInstances())[0];
    expect(bad.status).toBe("invalid-config");
    expect(bad.error).toContain("url");
  });

  it("returns a MediaServer client for the mediaServer role", async () => {
    const server = await startFakeJellyfin({ setupCompleted: true, sessions: "none" });
    const runtime = createProviderRuntime({
      registry,
      source: {
        getInstances: async () => [
          {
            id: "jellyfin",
            providerId: "jellyfin",
            name: "Jellyfin",
            enabled: true,
            config: { url: server.url, apiKey: GOOD_KEY },
          },
        ],
      },
    });
    const [{ client }] = await runtime.getClients<MediaServer>("mediaServer");
    expect((await client.getStatus()).state).toBe("ok");
    await server.close();
  });
});
