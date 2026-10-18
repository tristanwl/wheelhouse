import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  GOOD_KEY,
  startFakeSabnzbd,
} from "@/lib/providers/download-client/__fixtures__/sabnzbd/server";
import {
  GOOD_PASSWORD,
  GOOD_USER,
  startFakeQbittorrent,
} from "@/lib/providers/download-client/__fixtures__/qbittorrent/server";
import { GET as getDownloads, type DownloadsResponse } from "./route";
import { GET as getStatus } from "../status/route";
import type { StatusResponse } from "@/lib/status";

// The real route handlers, through the real env config source and runtime,
// with fake SABnzbd and qBittorrent servers standing in for the services.
describe("/api/downloads with qBittorrent configured from the environment", () => {
  const stops: (() => Promise<void>)[] = [];

  beforeEach(() => {
    for (const name of [
      "SABNZBD_URL",
      "SABNZBD_API_KEY",
      "QBITTORRENT_URL",
      "QBITTORRENT_USERNAME",
      "QBITTORRENT_PASSWORD",
      "SONARR_URL",
      "RADARR_URL",
      "PLEX_URL",
    ]) {
      vi.stubEnv(name, "");
    }
  });

  afterEach(async () => {
    vi.unstubAllEnvs();
    await Promise.all(stops.splice(0).map((stop) => stop()));
  });

  async function withSab(scenario: "idle" | "active" = "active", apiKey = GOOD_KEY) {
    const sab = await startFakeSabnzbd(scenario);
    stops.push(sab.close);
    vi.stubEnv("SABNZBD_URL", sab.url);
    vi.stubEnv("SABNZBD_API_KEY", apiKey);
    return sab;
  }

  async function withQb(password = GOOD_PASSWORD, version: "v4" | "v5" = "v5") {
    const qb = await startFakeQbittorrent(version);
    stops.push(qb.close);
    vi.stubEnv("QBITTORRENT_URL", qb.url);
    vi.stubEnv("QBITTORRENT_USERNAME", GOOD_USER);
    vi.stubEnv("QBITTORRENT_PASSWORD", password);
    return qb;
  }

  async function downloads() {
    const res = await getDownloads();
    const text = await res.text();
    return { body: JSON.parse(text) as DownloadsResponse, text };
  }

  it("shows qBittorrent downloads, seeding included, on their own", async () => {
    await withQb();
    const { body } = await downloads();

    expect(body.instances).toEqual([
      expect.objectContaining({
        instanceId: "qbittorrent",
        name: "qBittorrent",
        protocol: "torrent",
        up: true,
        paused: false,
      }),
    ]);
    expect(body.items.length).toBeGreaterThan(0);
    expect(
      body.items.every((i) => i.instanceId === "qbittorrent" && i.protocol === "torrent")
    ).toBe(true);
    expect(body.items.some((i) => i.state === "seeding")).toBe(true);
    expect(body.items[0]).toMatchObject({
      name: "Example.Movie.2024.1080p.BluRay.x264-GROUP",
      state: "seeding",
      sizeBytes: 1073741824,
      progress: 1,
    });
  });

  it("lists SABnzbd and qBittorrent together, each item tagged by its instance", async () => {
    await withSab();
    await withQb();
    const { body } = await downloads();

    expect(body.instances.map((i) => [i.instanceId, i.protocol, i.up])).toEqual([
      ["sabnzbd", "usenet", true],
      ["qbittorrent", "torrent", true],
    ]);
    const bySource = (id: string) => body.items.filter((i) => i.instanceId === id);
    expect(bySource("sabnzbd")).toHaveLength(3);
    expect(bySource("sabnzbd").every((i) => i.protocol === "usenet")).toBe(true);
    expect(bySource("qbittorrent").length).toBeGreaterThan(3);
    expect(bySource("qbittorrent").every((i) => i.protocol === "torrent")).toBe(true);
    expect(body.items).toHaveLength(bySource("sabnzbd").length + bySource("qbittorrent").length);
    // Configured order: all of SABnzbd's rows, then qBittorrent's.
    expect(body.items[0].instanceId).toBe("sabnzbd");
    expect(body.items.at(-1)?.instanceId).toBe("qbittorrent");
  });

  it("a wrong qBittorrent password doesn't break SABnzbd, and leaks nothing", async () => {
    await withSab();
    await withQb("wrong-qb-password-999");
    const { body, text } = await downloads();

    expect(body.instances.find((i) => i.instanceId === "sabnzbd")).toMatchObject({ up: true });
    expect(body.instances.find((i) => i.instanceId === "qbittorrent")).toMatchObject({
      up: false,
      error: "qbittorrent: login failed (check the username and password)",
    });
    expect(body.items).toHaveLength(3);
    expect(body.items.every((i) => i.instanceId === "sabnzbd")).toBe(true);
    expect(text).not.toContain("wrong-qb-password-999");
  });

  it("a wrong SABnzbd key doesn't break qBittorrent", async () => {
    await withSab("active", "wrong-sab-key-999");
    await withQb();
    const { body, text } = await downloads();

    expect(body.instances.find((i) => i.instanceId === "sabnzbd")).toMatchObject({ up: false });
    expect(body.instances.find((i) => i.instanceId === "qbittorrent")).toMatchObject({ up: true });
    expect(body.items.length).toBeGreaterThan(0);
    expect(body.items.every((i) => i.instanceId === "qbittorrent")).toBe(true);
    expect(text).not.toContain("wrong-sab-key-999");
  });

  it("an unreachable qBittorrent is reported down while SABnzbd still lists", async () => {
    await withSab();
    const dead = await startFakeQbittorrent("v5");
    vi.stubEnv("QBITTORRENT_URL", dead.url);
    vi.stubEnv("QBITTORRENT_USERNAME", GOOD_USER);
    vi.stubEnv("QBITTORRENT_PASSWORD", GOOD_PASSWORD);
    await dead.close();

    const { body } = await downloads();
    expect(body.instances.find((i) => i.instanceId === "qbittorrent")).toMatchObject({
      up: false,
      error: "qbittorrent: connection refused",
    });
    expect(body.items).toHaveLength(3);
  });

  it("reports a half-configured qBittorrent as down, naming the missing fields", async () => {
    vi.stubEnv("QBITTORRENT_URL", "http://127.0.0.1:9");
    const { body } = await downloads();
    expect(body.instances[0]).toMatchObject({ instanceId: "qbittorrent", up: false });
    expect(body.instances[0].error).toContain("username");
    expect(body.instances[0].error).toContain("password");
  });

  it("works with a v4 qBittorrent too", async () => {
    await withQb(GOOD_PASSWORD, "v4");
    const { body } = await downloads();
    expect(body.instances[0]).toMatchObject({ up: true, paused: true });
    expect(body.items.map((i) => i.state)).toEqual(["paused"]);
  });

  it("feeds the dashboard's qBittorrent card with seeding detail and no usenet servers", async () => {
    await withQb();
    const status = (await (await getStatus()).json()) as StatusResponse;
    const card = status.instances.find((i) => i.id === "qbittorrent")!;
    expect(card).toMatchObject({
      role: "downloadClient",
      state: "ok",
      version: "5.2.4",
      detail: { paused: false, torrent: { seeding: 2, uploadBytesPerSec: 0 } },
    });
    expect(card.detail).not.toHaveProperty("usenet");
    expect(JSON.stringify(status)).not.toContain(GOOD_PASSWORD);
  });
});
