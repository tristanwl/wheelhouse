import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createRegistry } from "../core/registry";
import { createProviderRuntime } from "../core/runtime";
import type { Instance } from "../core/types";
import { describeDownloadClientContract } from "./contract";
import { GOOD_KEY, startFakeSabnzbd, type Scenario } from "./__fixtures__/sabnzbd/server";
import { parseSabDuration, sabnzbdProvider } from "./sabnzbd";
import type { DownloadClient } from "./types";

async function clientFor(scenario: Scenario, apiKey = GOOD_KEY) {
  const server = await startFakeSabnzbd(scenario);
  const client = sabnzbdProvider.create(
    { url: server.url, apiKey },
    { instanceId: "sabnzbd", instanceName: "SABnzbd" }
  );
  return { client, server };
}

describeDownloadClientContract("sabnzbd", async () => {
  const { client, server } = await clientFor("active");
  return { client, protocol: "usenet", teardown: server.close };
});

describe("parseSabDuration", () => {
  it("parses H:MM:SS and D:HH:MM:SS, and treats zero as unknown", () => {
    expect(parseSabDuration("0:07:12")).toBe(432);
    expect(parseSabDuration("1:02:03")).toBe(3723);
    expect(parseSabDuration("1:02:03:04")).toBe(86400 + 2 * 3600 + 3 * 60 + 4);
    expect(parseSabDuration("0:00:00")).toBeNull();
  });

  it("returns null for anything malformed", () => {
    expect(parseSabDuration(undefined)).toBeNull();
    expect(parseSabDuration("")).toBeNull();
    expect(parseSabDuration("soon")).toBeNull();
    expect(parseSabDuration("1:2")).toBeNull();
    expect(parseSabDuration("1:2:3:4:5")).toBeNull();
  });
});

describe("sabnzbd adapter: items", () => {
  let client: DownloadClient;
  let close: () => Promise<void>;

  beforeAll(async () => {
    const made = await clientFor("active");
    client = made.client;
    close = made.server.close;
  });
  afterAll(() => close());

  it("converts megabytes to bytes and percentages to 0..1", async () => {
    const [first] = await client.listItems();
    expect(first.name).toBe("Example.Movie.2024.1080p.WEB-DL.x264-GROUP");
    expect(first.sizeBytes).toBe(4096 * 1024 * 1024);
    expect(first.remainingBytes).toBe(3072 * 1024 * 1024);
    expect(first.progress).toBe(0.25);
    expect(first.etaSeconds).toBe(60);
    expect(first.state).toBe("downloading");
  });

  it("maps queued jobs and unknown ETAs", async () => {
    const items = await client.listItems();
    expect(items[1].state).toBe("queued");
    expect(items[1].etaSeconds).toBeNull();
  });

  it("uses history to reveal post-processing that the queue calls 'Downloading'", async () => {
    const items = await client.listItems();
    // The queue says "Downloading" at 100%; history says the job is Extracting.
    expect(items[2].state).toBe("postprocessing");
    expect(items[2].stateDetail).toBe("Extracting");
    expect(items[2].progress).toBe(1);
    // The same fixture gives it a day-long ETA in D:HH:MM:SS form.
    expect(items[2].etaSeconds).toBe(86400 + 2 * 3600 + 3 * 60 + 4);
  });

  it("returns nothing for an idle queue", async () => {
    const idle = await clientFor("idle");
    expect(await idle.client.listItems()).toEqual([]);
    await idle.server.close();
  });

  it("reports a paused item as paused", async () => {
    const paused = await clientFor("paused");
    const items = await paused.client.listItems();
    expect(items[0].state).toBe("paused");
    await paused.server.close();
  });
});

describe("sabnzbd adapter: status", () => {
  it("normalizes speed, queue, ETA, version and servers", async () => {
    const { client, server } = await clientFor("active");
    const status = await client.getStatus();
    expect(status).toMatchObject({
      paused: false,
      statusText: "Downloading",
      speedBytesPerSec: 51200 * 1024,
      queueCount: 3,
      etaSeconds: 432,
      version: "4.5.2",
    });
    expect(status.usenet?.servers).toEqual([
      { name: "news.example.com", connections: 20, ssl: true, enabled: true },
      { name: "backup.example.net", connections: 8, ssl: false, enabled: false },
    ]);
    await server.close();
  });

  it("reports an idle queue", async () => {
    const { client, server } = await clientFor("idle");
    const status = await client.getStatus();
    expect(status).toMatchObject({
      paused: false,
      statusText: "Idle",
      speedBytesPerSec: 0,
      queueCount: 0,
      etaSeconds: null,
    });
    await server.close();
  });

  it("reports a paused queue", async () => {
    const { client, server } = await clientFor("paused");
    expect(await client.getStatus()).toMatchObject({ paused: true, statusText: "Paused" });
    await server.close();
  });

  it("keeps the last three warnings with time and severity, minus refused connections", async () => {
    const { client, server } = await clientFor("active");
    const { warnings } = await client.getStatus();
    // Fixture: ERROR, "Refused connection" (dropped), WARNING, INFO -> last three kept.
    expect(warnings.map((w) => w.severity)).toEqual(["error", "warning", "warning"]);
    expect(warnings[0].message).toContain("__ADMIN__");
    expect(warnings[0].at).toBe(new Date(1792297332 * 1000).toISOString());
    expect(warnings.some((w) => w.message.startsWith("Refused connection"))).toBe(false);
    await server.close();
  });
});

describe("sabnzbd adapter: connection and errors", () => {
  it("testConnection returns the version", async () => {
    const { client, server } = await clientFor("idle");
    expect(await client.testConnection()).toEqual({ ok: true, version: "4.5.2" });
    await server.close();
  });

  it("a wrong API key becomes a clear message that doesn't contain the key", async () => {
    const { client, server } = await clientFor("idle", "wrong-key-value-123");
    const result = await client.testConnection();
    expect(result).toEqual({ ok: false, error: "sabnzbd: API key incorrect" });
    await expect(client.getStatus()).rejects.toThrow("sabnzbd: API key incorrect");
    await expect(client.listItems()).rejects.toThrow("sabnzbd: API key incorrect");
    await server.close();
  });

  it("an unreachable server is reported, not thrown, by testConnection", async () => {
    const dead = await startFakeSabnzbd("idle");
    const url = dead.url;
    await dead.close();
    const client = sabnzbdProvider.create(
      { url, apiKey: GOOD_KEY },
      { instanceId: "sabnzbd", instanceName: "SABnzbd" }
    );
    const result = await client.testConnection();
    expect(result.ok).toBe(false);
    expect(result.error).toBe("sabnzbd: connection refused");
  });

  it("sends the key as a query parameter (so errors must never echo URLs)", async () => {
    const { client, server } = await clientFor("idle");
    await client.testConnection();
    expect(server.requests[0].searchParams.get("apikey")).toBe(GOOD_KEY);
    await server.close();
  });
});

describe("sabnzbd provider through the runtime", () => {
  const registry = createRegistry([sabnzbdProvider]);
  const instance = (config: unknown): Instance => ({
    id: "sabnzbd",
    providerId: "sabnzbd",
    name: "SABnzbd",
    enabled: true,
    config,
  });

  it("builds a client from valid config", async () => {
    const server = await startFakeSabnzbd("idle");
    const runtime = createProviderRuntime({
      registry,
      source: { getInstances: async () => [instance({ url: server.url, apiKey: GOOD_KEY })] },
    });
    const clients = await runtime.getClients<DownloadClient>("downloadClient");
    expect(clients).toHaveLength(1);
    expect((await clients[0].client.getStatus()).statusText).toBe("Idle");
    await server.close();
  });

  it("reports missing config as invalid, naming the fields", async () => {
    const runtime = createProviderRuntime({
      registry,
      source: { getInstances: async () => [instance({ url: "http://sab:8080" })] },
    });
    const [resolved] = await runtime.listInstances();
    expect(resolved.status).toBe("invalid-config");
    expect(resolved.error).toContain("apiKey");
  });
});
