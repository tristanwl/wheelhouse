import { describe, expect, it } from "vitest";
import { createRegistry } from "../core/registry";
import { createProviderRuntime } from "../core/runtime";
import { describeMediaServerContract } from "./contract";
import { GOOD_TOKEN, startFakePlex, type PlexScenario } from "./__fixtures__/plex/server";
import { plexProvider } from "./plex";
import type { MediaServer } from "./types";

// Pass null for "no token": an explicit undefined would trigger the default.
async function clientFor(scenario: PlexScenario, token: string | null = GOOD_TOKEN) {
  const server = await startFakePlex(scenario);
  const client = plexProvider.create(
    { url: server.url, token: token ?? undefined },
    { instanceId: "plex", instanceName: "Plex" }
  );
  return { client, server };
}

describeMediaServerContract("plex", async () => {
  const { client, server } = await clientFor({ claimed: true, sessions: "active" });
  return { client, teardown: server.close };
});

describe("plex adapter: status", () => {
  it("reports name, version and active sessions for a healthy, authenticated server", async () => {
    const { client, server } = await clientFor({ claimed: true, sessions: "active" });
    expect(await client.getStatus()).toEqual({
      state: "ok",
      name: "Example Plex",
      version: "1.43.3.10828-00f62d37d",
      activeSessions: 2,
      authenticated: true,
    });
    await server.close();
  });

  it("reports zero sessions when idle", async () => {
    const { client, server } = await clientFor({ claimed: true, sessions: "idle" });
    expect((await client.getStatus()).activeSessions).toBe(0);
    await server.close();
  });

  it("needs setup when the server is unclaimed, without asking for a token", async () => {
    const { client, server } = await clientFor({ claimed: false, sessions: "idle" }, null);
    const status = await client.getStatus();
    expect(status).toMatchObject({ state: "needs-setup", authenticated: false });
    expect(status.notice).toMatch(/not claimed/i);
    expect(server.requests.map((r) => r.path)).toEqual(["/identity"]);
    await server.close();
  });

  it("is ok but unauthenticated when claimed and no token is configured", async () => {
    const { client, server } = await clientFor({ claimed: true, sessions: "active" }, null);
    const status = await client.getStatus();
    expect(status).toMatchObject({
      state: "ok",
      authenticated: false,
      version: "1.43.3.10828-00f62d37d",
    });
    expect(status.activeSessions).toBeUndefined();
    expect(status.notice).toMatch(/no plex token/i);
    await server.close();
  });

  it("says the token was rejected (HTTP 401 with an HTML page) instead of showing zero sessions", async () => {
    const { client, server } = await clientFor(
      { claimed: true, sessions: "active" },
      "stale-token-value"
    );
    const status = await client.getStatus();
    expect(status).toMatchObject({ state: "ok", authenticated: false });
    expect(status.activeSessions).toBeUndefined();
    expect(status.notice).toMatch(/token was rejected/i);
    expect(JSON.stringify(status)).not.toContain("stale-token-value");
    await server.close();
  });

  it("keeps the server 'ok' when details fail for another reason", async () => {
    const { client, server } = await clientFor({
      claimed: true,
      sessions: "idle",
      authedFailure: 500,
    });
    const status = await client.getStatus();
    expect(status.state).toBe("ok");
    expect(status.authenticated).toBe(false);
    expect(status.notice).toMatch(/couldn't read plex details/i);
    await server.close();
  });

  it("sends the token as a header and never to /identity", async () => {
    const { client, server } = await clientFor({ claimed: true, sessions: "idle" });
    await client.getStatus();
    const identity = server.requests.find((r) => r.path === "/identity");
    expect(identity?.token).toBeUndefined();
    expect(
      server.requests.filter((r) => r.path !== "/identity").every((r) => r.token === GOOD_TOKEN)
    ).toBe(true);
    await server.close();
  });

  it("throws when the server can't be reached at all", async () => {
    const dead = await startFakePlex({ claimed: true, sessions: "idle" });
    const url = dead.url;
    await dead.close();
    const client = plexProvider.create(
      { url, token: GOOD_TOKEN },
      { instanceId: "plex", instanceName: "Plex" }
    );
    await expect(client.getStatus()).rejects.toThrow("plex: connection refused");
  });
});

describe("plex adapter: testConnection", () => {
  it("ok with a good token", async () => {
    const { client, server } = await clientFor({ claimed: true, sessions: "idle" });
    expect(await client.testConnection()).toEqual({ ok: true, version: "1.43.3.10828-00f62d37d" });
    await server.close();
  });

  it("ok without a token (the server is reachable)", async () => {
    const { client, server } = await clientFor({ claimed: false, sessions: "idle" }, null);
    expect((await client.testConnection()).ok).toBe(true);
    await server.close();
  });

  it("fails clearly with a rejected token", async () => {
    const { client, server } = await clientFor(
      { claimed: true, sessions: "idle" },
      "wrong-token-value"
    );
    expect(await client.testConnection()).toEqual({
      ok: false,
      version: "1.43.3.10828-00f62d37d",
      error: "plex: token rejected",
    });
    await server.close();
  });

  it("fails clearly when unreachable", async () => {
    const dead = await startFakePlex({ claimed: true, sessions: "idle" });
    const url = dead.url;
    await dead.close();
    const client = plexProvider.create({ url }, { instanceId: "plex", instanceName: "Plex" });
    expect(await client.testConnection()).toEqual({ ok: false, error: "plex: connection refused" });
  });
});

describe("plex provider through the runtime", () => {
  const registry = createRegistry([plexProvider]);

  it("accepts a missing token and rejects a missing URL", async () => {
    const make = (config: unknown) =>
      createProviderRuntime({
        registry,
        source: {
          getInstances: async () => [
            { id: "plex", providerId: "plex", name: "Plex", enabled: true, config },
          ],
        },
      });
    expect((await make({ url: "http://plex:32400" }).listInstances())[0].status).toBe("ready");
    const bad = (await make({ token: "t" }).listInstances())[0];
    expect(bad.status).toBe("invalid-config");
    expect(bad.error).toContain("url");
  });

  it("returns a MediaServer client for the mediaServer role", async () => {
    const server = await startFakePlex({ claimed: true, sessions: "idle" });
    const runtime = createProviderRuntime({
      registry,
      source: {
        getInstances: async () => [
          {
            id: "plex",
            providerId: "plex",
            name: "Plex",
            enabled: true,
            config: { url: server.url, token: GOOD_TOKEN },
          },
        ],
      },
    });
    const [{ client }] = await runtime.getClients<MediaServer>("mediaServer");
    expect((await client.getStatus()).state).toBe("ok");
    await server.close();
  });
});
