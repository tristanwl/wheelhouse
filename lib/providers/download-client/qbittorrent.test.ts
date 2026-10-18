import { describe, expect, it } from "vitest";
import { createRegistry } from "../core/registry";
import { createProviderRuntime } from "../core/runtime";
import type { Instance } from "../core/types";
import { describeDownloadClientContract } from "./contract";
import {
  GOOD_PASSWORD,
  GOOD_USER,
  startFakeQbittorrent,
  type QbVersion,
  type Scenario,
} from "./__fixtures__/qbittorrent/server";
import torrentsV5 from "./__fixtures__/qbittorrent/torrents-v5.json";
import { mapQbittorrentState, qbittorrentProvider } from "./qbittorrent";
import type { DownloadState } from "./types";

const ctx = { instanceId: "qbittorrent", instanceName: "qBittorrent" };

async function clientFor(
  version: QbVersion,
  scenario: Scenario = "active",
  credentials: { username?: string; password?: string } = {}
) {
  const server = await startFakeQbittorrent(version, scenario);
  const client = qbittorrentProvider.create(
    {
      url: server.url,
      username: credentials.username ?? GOOD_USER,
      password: credentials.password ?? GOOD_PASSWORD,
    },
    ctx
  );
  return { client, server };
}

for (const version of ["v5", "v4"] as const) {
  describeDownloadClientContract(`qbittorrent ${version}`, async () => {
    const { client, server } = await clientFor(version);
    return { client, protocol: "torrent", teardown: server.close };
  });
}

describe("qbittorrent state mapping", () => {
  const table: [string, DownloadState, string | undefined][] = [
    ["downloading", "downloading", undefined],
    ["forcedDL", "downloading", undefined],
    ["forcedMetaDL", "downloading", undefined],
    ["metaDL", "downloading", "Fetching metadata"],
    ["stalledDL", "stalled", undefined],
    ["queuedDL", "queued", undefined],
    ["queuedUP", "queued", undefined],
    ["checkingDL", "postprocessing", "Checking"],
    ["checkingUP", "postprocessing", "Checking"],
    ["checkingResumeData", "postprocessing", "Checking"],
    ["moving", "postprocessing", "Moving"],
    ["uploading", "seeding", undefined],
    ["forcedUP", "seeding", undefined],
    ["stalledUP", "seeding", undefined],
    ["pausedDL", "paused", undefined],
    ["stoppedDL", "paused", undefined],
    ["pausedUP", "completed", undefined],
    ["stoppedUP", "completed", undefined],
    ["error", "failed", undefined],
    ["missingFiles", "failed", "Files missing"],
  ];

  it.each(table)("%s -> %s", (qbState, state, stateDetail) => {
    expect(mapQbittorrentState(qbState)).toEqual(stateDetail ? { state, stateDetail } : { state });
  });

  it("keeps the client's word for a state it doesn't know", () => {
    expect(mapQbittorrentState("somethingNew")).toEqual({
      state: "downloading",
      stateDetail: "somethingNew",
    });
  });

  it("the v5 and v4 names of a stopped torrent agree", () => {
    expect(mapQbittorrentState("stoppedDL")).toEqual(mapQbittorrentState("pausedDL"));
    expect(mapQbittorrentState("stoppedUP")).toEqual(mapQbittorrentState("pausedUP"));
  });
});

describe("qbittorrent adapter: items", () => {
  it("lists everything except finished, stopped torrents (v5 fixtures from a real client)", async () => {
    const { client, server } = await clientFor("v5");
    const items = await client.listItems();
    const stoppedUp = torrentsV5.filter((t) => t.state === "stoppedUP");
    expect(stoppedUp).toHaveLength(1);
    expect(items).toHaveLength(torrentsV5.length - 1);
    expect(items.some((i) => i.id === stoppedUp[0].hash)).toBe(false);
    // Seeding, stalled, queued, checking, moving, metadata and a stopped download are all there.
    expect(new Set(items.map((i) => i.state))).toEqual(
      new Set(["seeding", "stalled", "downloading", "queued", "postprocessing", "paused"])
    );
    await server.close();
  });

  it("maps a real seeding torrent: bytes, progress, id is the hash", async () => {
    const { client, server } = await clientFor("v5");
    const items = await client.listItems();
    const seeding = items.find((i) => i.name === "Example.Movie.2024.1080p.BluRay.x264-GROUP")!;
    expect(seeding).toMatchObject({
      id: torrentsV5[0].hash,
      protocol: "torrent",
      state: "seeding",
      progress: 1,
      sizeBytes: 1073741824,
      remainingBytes: 0,
      etaSeconds: null,
      torrent: { ratio: 0, seeders: 0, uploadBytesPerSec: 0 },
    });
    await server.close();
  });

  it("maps a half-downloaded stalled torrent and a magnet still fetching metadata", async () => {
    const { client, server } = await clientFor("v5");
    const items = await client.listItems();
    expect(items.find((i) => i.state === "stalled")).toMatchObject({
      progress: 0.5,
      sizeBytes: 262144,
      remainingBytes: 131072,
      etaSeconds: null,
    });
    expect(items.find((i) => i.stateDetail === "Fetching metadata")).toMatchObject({
      state: "downloading",
      sizeBytes: 0,
      remainingBytes: 0,
    });
    await server.close();
  });

  it("reads v4's paused names", async () => {
    const { client, server } = await clientFor("v4");
    const items = await client.listItems();
    // The v4 capture has one paused download and one paused (finished) upload.
    expect(items.map((i) => i.state)).toEqual(["paused"]);
    await server.close();
  });

  it("treats eta 8640000 as infinity and passes real ETAs through", async () => {
    const { client, server } = await clientFor("v5");
    const base = server.state.torrents[0];
    server.state.torrents = [
      { ...base, hash: "a", state: "downloading", progress: 0.1, eta: 8640000 },
      { ...base, hash: "b", state: "downloading", progress: 0.1, eta: 90 },
      { ...base, hash: "c", state: "downloading", progress: 0.1, eta: 0 },
    ];
    const items = await client.listItems();
    expect(items.map((i) => i.etaSeconds)).toEqual([null, 90, null]);
    await server.close();
  });

  it("reports ratio, seeders and upload speed for a seeding torrent", async () => {
    const { client, server } = await clientFor("v5");
    server.state.torrents = [
      {
        ...server.state.torrents[0],
        state: "uploading",
        ratio: 1.75,
        num_seeds: 4,
        upspeed: 51200,
      },
    ];
    const [item] = await client.listItems();
    expect(item.state).toBe("seeding");
    expect(item.torrent).toEqual({ ratio: 1.75, seeders: 4, uploadBytesPerSec: 51200 });
    await server.close();
  });

  it("maps downloading, error and missing files, which a client without peers can't produce", async () => {
    const { client, server } = await clientFor("v5");
    const base = server.state.torrents[0];
    server.state.torrents = [
      { ...base, hash: "d", state: "downloading", progress: 0.25, size: 1000, amount_left: 750 },
      { ...base, hash: "e", state: "error", progress: 0.25, size: 1000, amount_left: 750 },
      { ...base, hash: "f", state: "missingFiles", progress: 1, size: 1000, amount_left: 0 },
    ];
    const items = await client.listItems();
    expect(items.map((i) => i.state)).toEqual(["downloading", "failed", "failed"]);
    expect(items[0]).toMatchObject({ sizeBytes: 1000, remainingBytes: 750, progress: 0.25 });
    await server.close();
  });

  it("returns nothing for an empty client", async () => {
    const { client, server } = await clientFor("v5", "idle");
    expect(await client.listItems()).toEqual([]);
    await server.close();
  });
});

describe("qbittorrent adapter: status", () => {
  it("reports an empty client as idle", async () => {
    const { client, server } = await clientFor("v5", "idle");
    expect(await client.getStatus()).toEqual({
      paused: false,
      statusText: "Idle",
      speedBytesPerSec: 0,
      queueCount: 0,
      etaSeconds: null,
      version: "5.2.4",
      warnings: [],
      torrent: { seeding: 0, uploadBytesPerSec: 0 },
    });
    await server.close();
  });

  it("counts unfinished torrents as the queue and seeding ones separately", async () => {
    const { client, server } = await clientFor("v5");
    const status = await client.getStatus();
    const incomplete = torrentsV5.filter((t) => t.progress < 1).length;
    expect(status.queueCount).toBe(incomplete);
    // stalledUP and forcedUP seed; queuedUP, stoppedUP and the rest don't.
    expect(status.torrent?.seeding).toBe(2);
    expect(status.paused).toBe(false);
    expect(status.statusText).toBe("Seeding");
    expect(status.version).toBe("5.2.4");
    expect(status.usenet).toBeUndefined();
    await server.close();
  });

  it("uses transfer info for speeds, and derives the whole-queue ETA from them", async () => {
    const { client, server } = await clientFor("v5");
    server.state.transfer = { ...server.state.transfer, dl_info_speed: 1000, up_info_speed: 250 };
    server.state.torrents = [
      { ...server.state.torrents[0], state: "downloading", progress: 0.5, amount_left: 4000 },
      { ...server.state.torrents[0], hash: "x", state: "queuedDL", progress: 0, amount_left: 1500 },
    ];
    expect(await client.getStatus()).toMatchObject({
      statusText: "Downloading",
      speedBytesPerSec: 1000,
      queueCount: 2,
      etaSeconds: 6,
      torrent: { seeding: 0, uploadBytesPerSec: 250 },
    });
    await server.close();
  });

  it("is paused only when everything still downloading is stopped", async () => {
    const { client, server } = await clientFor("v4");
    // The v4 capture: one paused download, one finished and paused.
    expect(await client.getStatus()).toMatchObject({
      paused: true,
      statusText: "Paused",
      queueCount: 1,
      version: "4.6.7",
    });
    const [paused] = server.state.torrents;
    server.state.torrents = [paused, { ...paused, hash: "y", state: "downloading" }];
    expect((await client.getStatus()).paused).toBe(false);
    await server.close();
  });
});

describe("qbittorrent adapter: session and errors", () => {
  it("testConnection logs in and returns the version without the leading v", async () => {
    const { client, server } = await clientFor("v5");
    expect(await client.testConnection()).toEqual({ ok: true, version: "5.2.4" });
    expect(server.state.logins).toBe(1);
    await server.close();
  });

  it("logs in once and reuses the session cookie", async () => {
    const { client, server } = await clientFor("v4");
    await client.listItems();
    await client.listItems();
    await client.getStatus();
    expect(server.state.logins).toBe(1);
    const protectedCalls = server.requests.filter((r) => r.path !== "/api/v2/auth/login");
    expect(protectedCalls.every((r) => r.cookie?.startsWith("SID="))).toBe(true);
    await server.close();
  });

  it("v5 keeps its port-suffixed cookie name", async () => {
    const { client, server } = await clientFor("v5");
    await client.listItems();
    expect(server.requests.at(-1)?.cookie).toMatch(/^QBT_SID_8080=/);
    await server.close();
  });

  for (const version of ["v4", "v5"] as const) {
    it(`signs in again, once, when the ${version} session expires`, async () => {
      const { client, server } = await clientFor(version);
      await client.listItems();
      server.expireSessions();
      const items = await client.listItems();
      expect(items.length).toBeGreaterThan(0);
      expect(server.state.logins).toBe(2);
      // The expired call was a 403, then the retry succeeded.
      const calls = server.requests.filter((r) => r.path === "/api/v2/torrents/info");
      expect(calls).toHaveLength(3);
      await server.close();
    });
  }

  it("the parallel calls of one getStatus share a single new login", async () => {
    const { client, server } = await clientFor("v5");
    await client.listItems();
    server.expireSessions();
    await client.getStatus();
    expect(server.state.logins).toBe(2);
    await server.close();
  });

  for (const version of ["v4", "v5"] as const) {
    it(`a wrong password (${version}) is a clear message that doesn't contain it`, async () => {
      const secret = "wrong-secret-password-999";
      const { client, server } = await clientFor(version, "active", { password: secret });
      const result = await client.testConnection();
      expect(result).toEqual({
        ok: false,
        error: "qbittorrent: login failed (check the username and password)",
      });
      const failures: string[] = [];
      for (const call of [() => client.getStatus(), () => client.listItems()]) {
        await call().catch((err: Error) => failures.push(err.message));
      }
      expect(failures).toHaveLength(2);
      for (const message of failures) {
        expect(message).toBe("qbittorrent: login failed (check the username and password)");
        expect(message).not.toContain(secret);
      }
      await server.close();
    });
  }

  it("a banned IP (403 on login) says so, even with the right password", async () => {
    const { client: bad, server } = await clientFor("v5", "active", { password: "nope-nope" });
    for (let i = 0; i < 5; i++) await bad.testConnection();
    const good = qbittorrentProvider.create(
      { url: server.url, username: GOOD_USER, password: GOOD_PASSWORD },
      ctx
    );
    const result = await good.testConnection();
    expect(result.ok).toBe(false);
    expect(result.error).toBe(
      "qbittorrent: login refused, this IP may be banned after too many failed attempts"
    );
    expect(result.error).not.toContain(GOOD_PASSWORD);
    await server.close();
  });

  it("signs in only once more when the session keeps being refused", async () => {
    const { client, server } = await clientFor("v5");
    server.state.refuseSessions = true;
    await expect(client.listItems()).rejects.toThrow(
      "qbittorrent: not authorized after signing in"
    );
    // The first login, plus the single retry.
    expect(server.state.logins).toBe(2);
    await server.close();
  });

  it("an unreachable server is reported, not thrown, by testConnection", async () => {
    const dead = await startFakeQbittorrent("v5");
    const url = dead.url;
    await dead.close();
    const client = qbittorrentProvider.create(
      { url, username: GOOD_USER, password: GOOD_PASSWORD },
      ctx
    );
    expect(await client.testConnection()).toEqual({
      ok: false,
      error: "qbittorrent: connection refused",
    });
  });
});

describe("qbittorrent provider through the runtime", () => {
  const registry = createRegistry([qbittorrentProvider]);
  const instance = (config: unknown): Instance => ({
    id: "qbittorrent",
    providerId: "qbittorrent",
    name: "qBittorrent",
    enabled: true,
    config,
  });

  it("builds a client from valid config", async () => {
    const server = await startFakeQbittorrent("v5");
    const runtime = createProviderRuntime({
      registry,
      source: {
        getInstances: async () => [
          instance({ url: server.url, username: GOOD_USER, password: GOOD_PASSWORD }),
        ],
      },
    });
    const [resolved] = await runtime.listInstances();
    expect(resolved.status).toBe("ready");
    expect(resolved.definition?.capabilities).toEqual({
      protocol: "torrent",
      canPause: true,
      usenetServers: false,
      seeding: true,
    });
    await server.close();
  });

  it("reports an incomplete config as invalid, naming the missing field", async () => {
    const runtime = createProviderRuntime({
      registry,
      source: { getInstances: async () => [instance({ url: "http://qbittorrent:8080" })] },
    });
    const [resolved] = await runtime.listInstances();
    expect(resolved.status).toBe("invalid-config");
    expect(resolved.error).toContain("username");
    expect(resolved.error).toContain("password");
  });

  it("marks the password as a secret", () => {
    expect(qbittorrentProvider.secretKeys).toEqual(["password"]);
  });
});
