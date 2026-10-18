import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  GOOD_TOKEN,
  startFakePlex,
  type PlexScenario,
} from "@/lib/providers/media-server/__fixtures__/plex/server";
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
