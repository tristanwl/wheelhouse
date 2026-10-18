import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  GOOD_KEY as SAB_KEY,
  startFakeSabnzbd,
} from "@/lib/providers/download-client/__fixtures__/sabnzbd/server";
import {
  GOOD_TOKEN,
  startFakePlex,
  type PlexScenario,
} from "@/lib/providers/media-server/__fixtures__/plex/server";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import type { StatusInstance, StatusResponse } from "@/lib/status";
import { GET } from "./route";

// Calls the real route handler through the real env config and provider
// runtime, with fake servers standing in for the services.
describe("/api/status", () => {
  let stack: ReturnType<typeof createArrStack>;
  const stops: (() => Promise<void>)[] = [];

  beforeEach(() => {
    stack = createArrStack();
    for (const name of ["PLEX_URL", "PLEX_TOKEN", "SABNZBD_URL", "SABNZBD_API_KEY"]) {
      vi.stubEnv(name, "");
    }
    // The SABnzbd fixture's warnings are from a fixed moment: put "now" an hour after the latest.
    vi.useFakeTimers({ toFake: ["Date"], now: 1792300000 * 1000 + 3_600_000 });
  });

  afterEach(async () => {
    vi.useRealTimers();
    await stack.stop();
    await Promise.all(stops.splice(0).map((stop) => stop()));
  });

  async function sab(scenario: "idle" | "active" | "paused" = "active", key = SAB_KEY) {
    const server = await startFakeSabnzbd(scenario);
    stops.push(server.close);
    vi.stubEnv("SABNZBD_URL", server.url);
    vi.stubEnv("SABNZBD_API_KEY", key);
    return server;
  }

  async function plex(scenario: PlexScenario, token: string | null = GOOD_TOKEN) {
    const server = await startFakePlex(scenario);
    stops.push(server.close);
    vi.stubEnv("PLEX_URL", server.url);
    vi.stubEnv("PLEX_TOKEN", token ?? "");
    return server;
  }

  async function get() {
    const response = await GET();
    const text = await response.text();
    const body = JSON.parse(text) as StatusResponse;
    return { body, text, byId: (id: string) => body.instances.find((i) => i.id === id)! };
  }

  const okHealth = [{ source: "UpdateCheck", type: "ok", message: "all good" }];

  it("returns no instances when nothing is configured", async () => {
    expect((await get()).body).toEqual({ instances: [] });
  });

  it("lists every instance at once, grouped by role in configuration order", async () => {
    await stack.radarr4k({ health: okHealth });
    await stack.radarr();
    await stack.sonarr({ health: okHealth });
    await sab();
    await plex({ claimed: true, sessions: "active" });

    const { body, byId } = await get();
    expect(body.instances.map((i) => [i.id, i.role, i.state])).toEqual([
      ["sonarr", "mediaManager", "ok"],
      ["radarr", "mediaManager", "degraded"],
      ["radarr-4k", "mediaManager", "ok"],
      ["sabnzbd", "downloadClient", "degraded"],
      ["plex", "mediaServer", "ok"],
    ]);
    expect(byId("radarr-4k")).toMatchObject({
      providerId: "radarr",
      name: "Radarr 4K",
      version: "5.26.2.10099",
      detail: { queueCount: 0, indexers: { total: 2, enabled: 2 } },
    });
    expect(byId("sonarr")).toMatchObject({ version: "4.0.15.2941", detail: { queueCount: 2 } });
    expect(byId("sabnzbd")).toMatchObject({
      providerId: "sabnzbd",
      detail: { paused: false, usenet: { servers: expect.any(Array) } },
    });
    expect(byId("plex")).toMatchObject({
      name: "Plex",
      version: "1.43.3.10828-00f62d37d",
      detail: { authenticated: true, activeSessions: 2, name: "Example Plex" },
    });
  });

  it("marks reachable instances with problems as degraded, and healthy ones ok", async () => {
    await stack.radarr(); // fixture health has warnings
    await stack.sonarr({ health: okHealth });
    const { byId } = await get();
    expect(byId("radarr").state).toBe("degraded");
    expect(byId("sonarr").state).toBe("ok");
  });

  it("degrades a download client only for recent error warnings", async () => {
    await sab();
    expect((await get()).byId("sabnzbd").state).toBe("degraded");

    // Two days on, the error has aged out; the client is fine again.
    vi.setSystemTime(1792300000 * 1000 + 48 * 3_600_000);
    expect((await get()).byId("sabnzbd").state).toBe("ok");
  });

  it("flags an unclaimed Plex as needing setup", async () => {
    await plex({ claimed: false, sessions: "idle" }, null);
    const plexInstance = (await get()).byId("plex");
    expect(plexInstance.state).toBe("needs-setup");
    expect(plexInstance).toMatchObject({ detail: { state: "needs-setup", authenticated: false } });
  });

  it("keeps a Plex without a token ok, with its notice in the detail", async () => {
    await plex({ claimed: true, sessions: "active" }, null);
    const plexInstance = (await get()).byId("plex");
    expect(plexInstance.state).toBe("ok");
    expect(String((plexInstance as { detail: { notice?: string } }).detail.notice)).toMatch(
      /no plex token/i
    );
  });

  it("reports down, with a reason, for an unreachable instance", async () => {
    const radarr = await stack.radarr();
    await radarr.close();
    expect((await get()).byId("radarr")).toMatchObject({
      state: "down",
      error: "radarr: connection refused",
    });
    expect((await get()).byId("radarr").detail).toBeUndefined();
  });

  it("reports a wrong API key as down, and never echoes the key", async () => {
    await stack.radarr({}, "wrong-key-value-123");
    const { byId, text } = await get();
    expect(byId("radarr")).toMatchObject({ state: "down", error: "radarr: API key incorrect" });
    expect(text).not.toContain("wrong-key-value-123");
  });

  it("reports a half-configured service as down with the configuration problem", async () => {
    vi.stubEnv("SONARR_URL", "http://sonarr.example.com:8989");
    const sonarr = (await get()).byId("sonarr");
    expect(sonarr.state).toBe("down");
    expect(sonarr.error).toContain("apiKey");
    // The link still points at the configured port.
    expect(sonarr.link).toEqual({ protocol: "http", port: 8989, path: "/" });
  });

  it("omits the link when the URL itself is the problem", async () => {
    vi.stubEnv("SONARR_URL", "not a url");
    vi.stubEnv("SONARR_API_KEY", SAB_KEY);
    const sonarr = (await get()).byId("sonarr");
    expect(sonarr.state).toBe("down");
    expect(sonarr.link).toBeUndefined();
  });

  it("isolates failures: one broken instance leaves the others as they were", async () => {
    await stack.sonarr({ health: okHealth });
    const radarr = await stack.radarr();
    await radarr.close();
    await stack.radarr4k({ health: okHealth }, "wrong-key-value-123");
    await sab("idle", "wrong-sab-key");
    await plex({ claimed: true, sessions: "idle" });

    const { byId } = await get();
    expect(byId("sonarr").state).toBe("ok");
    expect(byId("radarr").state).toBe("down");
    expect(byId("radarr-4k").state).toBe("down");
    expect(byId("sabnzbd").state).toBe("down");
    expect(byId("plex").state).toBe("ok");
    expect(byId("plex")).toMatchObject({ detail: { activeSessions: 0 } });
  });

  it("gives the browser only a port and path, never host, credentials or key", async () => {
    const sonarr = await stack.sonarr({ health: okHealth });
    const server = await plex({ claimed: true, sessions: "idle" });
    vi.stubEnv("SABNZBD_URL", "http://user:hunter2@sab.example.com:8080/sab/?x=1");
    vi.stubEnv("SABNZBD_API_KEY", "sab-secret-key-abc");

    const { byId, text } = await get();
    expect(JSON.stringify(byId("sonarr").link)).not.toContain("127.0.0.1");
    const port = (url: string) => Number(new URL(url).port);
    expect(byId("sonarr").link).toEqual({
      protocol: "http",
      port: port(sonarr.url),
      path: "/",
    });
    expect(byId("plex").link).toEqual({
      protocol: "http",
      port: port(server.url),
      path: "/web",
    });
    expect(byId("sabnzbd").link).toEqual({ protocol: "http", port: 8080, path: "/sab" });
    // Credentials in the URL never come back, in the link or in the error.
    for (const secret of ["hunter2", "sab-secret-key-abc"]) expect(text).not.toContain(secret);
    for (const instance of (JSON.parse(text) as StatusResponse).instances as StatusInstance[]) {
      expect(
        Object.keys(instance.link ?? {}).every((k) => ["protocol", "port", "path"].includes(k))
      ).toBe(true);
    }
  });
});
