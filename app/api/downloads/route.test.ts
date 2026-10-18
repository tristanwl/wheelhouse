import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  GOOD_KEY,
  startFakeSabnzbd,
} from "@/lib/providers/download-client/__fixtures__/sabnzbd/server";
import { GET as getDownloads, type DownloadsResponse } from "./route";
import { GET as getStatus } from "../status/route";
import type { StatusResponse } from "@/lib/status";

// These call the real route handlers, through the real env config source and
// provider runtime, with a fake SABnzbd standing in for the service.
describe("/api/downloads and /api/status with SABnzbd configured from the environment", () => {
  const stops: (() => Promise<void>)[] = [];

  beforeEach(() => {
    for (const name of ["SABNZBD_URL", "SABNZBD_API_KEY", "SONARR_URL", "RADARR_URL", "PLEX_URL"]) {
      vi.stubEnv(name, "");
    }
  });

  afterEach(async () => {
    vi.unstubAllEnvs();
    await Promise.all(stops.splice(0).map((stop) => stop()));
  });

  async function withSab(scenario: "idle" | "active" | "paused", apiKey = GOOD_KEY) {
    const sab = await startFakeSabnzbd(scenario);
    stops.push(sab.close);
    vi.stubEnv("SABNZBD_URL", sab.url);
    vi.stubEnv("SABNZBD_API_KEY", apiKey);
    return sab;
  }

  it("lists active downloads, tagged with their instance, in normalized units", async () => {
    await withSab("active");
    const body = (await (await getDownloads()).json()) as DownloadsResponse;

    expect(body.instances).toEqual([
      expect.objectContaining({
        instanceId: "sabnzbd",
        name: "SABnzbd",
        protocol: "usenet",
        up: true,
        paused: false,
        speedBytesPerSec: 51200 * 1024,
      }),
    ]);
    expect(body.items).toHaveLength(3);
    expect(body.items[0]).toMatchObject({
      instanceId: "sabnzbd",
      name: "Example.Movie.2024.1080p.WEB-DL.x264-GROUP",
      state: "downloading",
      progress: 0.25,
      sizeBytes: 4096 * 1024 * 1024,
    });
    expect(body.items[2]).toMatchObject({ state: "postprocessing", stateDetail: "Extracting" });
  });

  it("returns no instances and no items when no download client is configured", async () => {
    const body = (await (await getDownloads()).json()) as DownloadsResponse;
    expect(body).toEqual({ instances: [], items: [] });
  });

  it("reports a paused client", async () => {
    await withSab("paused");
    const body = (await (await getDownloads()).json()) as DownloadsResponse;
    expect(body.instances[0]).toMatchObject({ up: true, paused: true });
  });

  it("reports a wrong key per instance without leaking it", async () => {
    await withSab("active", "wrong-secret-key-999");
    const res = await getDownloads();
    const text = await res.text();
    const body = JSON.parse(text) as DownloadsResponse;
    expect(body.instances[0]).toMatchObject({
      up: false,
      error: "sabnzbd: API key incorrect",
    });
    expect(body.items).toEqual([]);
    expect(text).not.toContain("wrong-secret-key-999");
  });

  it("reports a half-configured client as down, naming the missing field", async () => {
    vi.stubEnv("SABNZBD_URL", "http://127.0.0.1:9");
    const body = (await (await getDownloads()).json()) as DownloadsResponse;
    expect(body.instances[0].up).toBe(false);
    expect(body.instances[0].error).toContain("apiKey");
  });

  async function sabInstance() {
    const body = (await (await getStatus()).json()) as StatusResponse;
    return body.instances.find((i) => i.id === "sabnzbd")!;
  }

  it("feeds the dashboard's SABnzbd card with the normalized status", async () => {
    await withSab("active");
    expect(await sabInstance()).toMatchObject({
      role: "downloadClient",
      detail: {
        paused: false,
        statusText: "Downloading",
        speedBytesPerSec: 51200 * 1024,
        queueCount: 3,
        etaSeconds: 432,
        usenet: {
          servers: [
            { name: "news.example.com", connections: 20, ssl: true, enabled: true },
            { name: "backup.example.net", connections: 8, ssl: false, enabled: false },
          ],
        },
      },
    });
    expect((await sabInstance()).detail).toHaveProperty("warnings", expect.any(Array));
  });

  it("an idle SABnzbd has no ETA to show", async () => {
    await withSab("idle");
    expect(await sabInstance()).toMatchObject({
      detail: { statusText: "Idle", queueCount: 0, etaSeconds: null },
    });
  });

  it("a missing SABnzbd config reports down instead of crashing the status route", async () => {
    vi.stubEnv("SABNZBD_URL", "http://127.0.0.1:9");
    const sab = await sabInstance();
    expect(sab.state).toBe("down");
    expect(String(sab.error)).toContain("apiKey");
  });
});
