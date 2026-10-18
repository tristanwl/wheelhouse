import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  GOOD_TOKEN,
  startFakePlex,
  type PlexScenario,
} from "@/lib/providers/media-server/__fixtures__/plex/server";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { GET } from "./route";

// Calls the real route handler through the real env config and provider
// runtime, with a fake Plex standing in for the service.
describe("/api/status: Plex card", () => {
  const stops: (() => Promise<void>)[] = [];

  beforeEach(() => {
    for (const name of [
      "PLEX_URL",
      "PLEX_TOKEN",
      "SABNZBD_URL",
      "SABNZBD_API_KEY",
      "SONARR_URL",
      "RADARR_URL",
    ]) {
      vi.stubEnv(name, "");
    }
  });

  afterEach(async () => {
    vi.unstubAllEnvs();
    await Promise.all(stops.splice(0).map((stop) => stop()));
  });

  async function plexStatus(scenario: PlexScenario, token: string | null = GOOD_TOKEN) {
    const plex = await startFakePlex(scenario);
    stops.push(plex.close);
    vi.stubEnv("PLEX_URL", plex.url);
    vi.stubEnv("PLEX_TOKEN", token ?? "");
    return ((await (await GET()).json()) as { plex: Record<string, unknown> }).plex;
  }

  it("shows name, version and sessions for a healthy server", async () => {
    expect(await plexStatus({ claimed: true, sessions: "active" })).toEqual({
      up: true,
      configured: true,
      authenticated: true,
      needsSetup: false,
      version: "1.43.3.10828-00f62d37d",
      friendlyName: "Example Plex",
      activeSessions: 2,
    });
  });

  it("flags an unclaimed server as needing setup", async () => {
    const plex = await plexStatus({ claimed: false, sessions: "idle" }, null);
    expect(plex).toMatchObject({ up: true, needsSetup: true, authenticated: false });
    expect(String(plex.notice)).toMatch(/not claimed/i);
  });

  it("explains a missing token without claiming zero sessions", async () => {
    const plex = await plexStatus({ claimed: true, sessions: "active" }, null);
    expect(plex).toMatchObject({ up: true, needsSetup: false, authenticated: false });
    expect(plex.activeSessions).toBeUndefined();
    expect(String(plex.notice)).toMatch(/no plex token/i);
  });

  it("explains a rejected token, and never echoes it", async () => {
    const plex = await plexStatus({ claimed: true, sessions: "active" }, "stale-token-value");
    expect(plex).toMatchObject({ up: true, authenticated: false });
    expect(String(plex.notice)).toMatch(/rejected/i);
    expect(JSON.stringify(plex)).not.toContain("stale-token-value");
  });

  it("reports down, with a reason, when the server is unreachable", async () => {
    const plex = await startFakePlex({ claimed: true, sessions: "idle" });
    const url = plex.url;
    await plex.close();
    vi.stubEnv("PLEX_URL", url);
    const body = (await (await GET()).json()) as { plex: Record<string, unknown> };
    expect(body.plex).toMatchObject({
      up: false,
      configured: true,
      error: "plex: connection refused",
    });
  });

  it("reports not configured when no Plex variables are set", async () => {
    const body = (await (await GET()).json()) as { plex: Record<string, unknown> };
    expect(body.plex).toEqual({ up: false, configured: false });
  });

  it("reports a token without a URL as a configuration error", async () => {
    vi.stubEnv("PLEX_TOKEN", "token-without-url");
    const body = (await (await GET()).json()) as { plex: Record<string, unknown> };
    expect(body.plex).toMatchObject({ up: false, configured: true });
    expect(String(body.plex.error)).toContain("url");
  });
});

describe("/api/status: Sonarr and Radarr cards", () => {
  let stack: ReturnType<typeof createArrStack>;

  beforeEach(() => {
    for (const name of ["PLEX_URL", "PLEX_TOKEN", "SABNZBD_URL", "SABNZBD_API_KEY"]) {
      vi.stubEnv(name, "");
    }
    stack = createArrStack();
  });

  afterEach(() => stack.stop());

  async function status() {
    return (await (await GET()).json()) as {
      sonarr: Record<string, unknown>;
      radarr: Record<string, unknown>;
    };
  }

  it("keeps the response shape for the default instances", async () => {
    await stack.sonarr();
    await stack.radarr();
    const body = await status();

    expect(body.sonarr).toEqual({
      up: true,
      configured: true,
      version: "4.0.15.2941",
      health: expect.any(Array),
      queueCount: 2,
      indexers: { total: 2, enabled: 2, issues: expect.any(Array) },
    });
    expect(body.radarr).toMatchObject({
      up: true,
      configured: true,
      version: "5.26.2.10099",
      queueCount: 0,
    });
    expect(body.radarr.health).toEqual([
      expect.objectContaining({ source: "IndexerLongTermStatusCheck", type: "warning" }),
      expect.objectContaining({ source: "UpdateCheck" }),
      expect.objectContaining({ source: "DownloadClientRootFolderCheck" }),
    ]);
  });

  it("reports not configured, without failing, when nothing is set", async () => {
    const body = await status();
    expect(body.sonarr).toEqual({ up: false, configured: false, health: [], queueCount: 0 });
    expect(body.radarr).toEqual({ up: false, configured: false, health: [], queueCount: 0 });
  });

  it("explains a wrong API key, and never echoes it", async () => {
    await stack.radarr({}, "wrong-key-value-123");
    const body = await status();
    expect(body.radarr).toMatchObject({
      up: false,
      configured: true,
      error: "radarr: API key incorrect",
    });
    expect(JSON.stringify(body)).not.toContain("wrong-key-value-123");
  });

  it("reports a half-configured service as a configuration error", async () => {
    vi.stubEnv("SONARR_URL", "http://sonarr.example.com:8989");
    const body = await status();
    expect(body.sonarr).toMatchObject({ up: false, configured: true });
    expect(String(body.sonarr.error)).toContain("apiKey");
  });

  it("reports down, with a reason, when the service is unreachable", async () => {
    const radarr = await stack.radarr();
    await radarr.close();
    expect((await status()).radarr).toMatchObject({
      up: false,
      configured: true,
      error: "radarr: connection refused",
    });
  });
});
