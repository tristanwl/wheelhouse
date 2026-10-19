import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startStack, type Stack } from "./fakes";
import { startServer, type RunningServer } from "./harness";

// Scenario 3: each fake in its failure modes. A service that is down, or has
// the wrong key, is reported as such, and everything else keeps working.
// (Services that never answer are in failures-slow.test.ts.)

const byId = <T extends { id?: string; instanceId?: string }>(list: T[], id: string) =>
  list.find((i) => (i.id ?? i.instanceId) === id);

describe("one broken service at a time never breaks the others", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack({
      modes: {
        radarr: "wrong-key",
        sonarr: "unreachable",
        sabnzbd: "wrong-key",
        qbittorrent: "healthy",
        plex: "wrong-key",
        jellyfin: "unreachable",
        tmdb: "wrong-key",
        // radarr-4k stays healthy
      },
    });
    server = await startServer({ env: stack.env });
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  it("status says which is down and why, in words", async () => {
    const { json } = await server.request("/api/status");
    const down = (id: string) => byId<any>(json.instances, id);

    expect(down("radarr")).toMatchObject({ state: "down" });
    expect(down("radarr").error).toMatch(/API key incorrect/i);
    expect(down("sabnzbd")).toMatchObject({ state: "down" });
    expect(down("sabnzbd").error).toMatch(/API key incorrect/i);
    expect(down("sonarr")).toMatchObject({ state: "down" });
    expect(down("sonarr").error).toMatch(/refused|unreachable|not found/i);
    expect(down("jellyfin")).toMatchObject({ state: "down" });
    expect(down("jellyfin").error).toMatch(/refused|unreachable|not found/i);

    // Healthy ones are untouched.
    for (const id of ["radarr-4k", "qbittorrent"]) {
      expect(["ok", "degraded"], id).toContain(down(id).state);
      expect(down(id).error).toBeUndefined();
    }
    // Plex and Jellyfin answer without a token; a rejected one only limits detail.
    expect(["ok", "degraded", "needs-setup", "down"]).toContain(down("plex").state);
    expect(down("plex").state).not.toBe("down");
  });

  it("downloads: the broken client is listed as down, the other still shows rows", async () => {
    const { status, json } = await server.request("/api/downloads");
    expect(status).toBe(200);
    expect(byId<any>(json.instances, "sabnzbd")).toMatchObject({ up: false });
    expect(byId<any>(json.instances, "sabnzbd").error).toMatch(/API key incorrect/i);
    expect(byId<any>(json.instances, "qbittorrent")).toMatchObject({ up: true });
    expect(json.items.length).toBeGreaterThan(0);
    for (const row of json.items) expect(row.instanceId).toBe("qbittorrent");
  });

  it("library and storage: broken managers are flagged, the healthy one still lists", async () => {
    for (const path of ["/api/library", "/api/storage"]) {
      const { status, json } = await server.request(path);
      expect(status, path).toBe(200);
      const up = Object.fromEntries(
        json.instances.map((i: { instanceId: string; up: boolean }) => [i.instanceId, i.up])
      );
      expect(up, path).toEqual({ radarr: false, "radarr-4k": true, sonarr: false });
      expect(byId<any>(json.instances, "radarr").error).toMatch(/API key incorrect/i);
      const rows = json.items ?? json.entries;
      expect(rows.length, path).toBeGreaterThan(0);
      for (const row of rows) expect(row.instanceId, path).toBe("radarr-4k");
    }
  });

  it("search and codec analysis: problems are listed next to the healthy results", async () => {
    for (const path of [
      "/api/search?kind=movies&term=example",
      "/api/codec-analysis?kind=movies",
    ]) {
      const { status, json } = await server.request(path);
      expect(status, path).toBe(200);
      expect(json.problems, path).toHaveLength(1);
      expect(json.problems[0].instanceId).toBe("radarr");
      expect(json.problems[0].error).toMatch(/API key incorrect/i);
    }
    // The only series manager is down, so there is nothing to answer with.
    const series = await server.request("/api/search?kind=series&term=example");
    expect(series.status).toBe(502);
    expect(series.json.error).toBeTypeOf("string");
  });

  it("add-meta by kind skips the broken instance, by id it does not", async () => {
    // radarr has the wrong key, radarr-4k is healthy: the first instance that
    // answers is used, and the picker still lists both.
    const { status, json } = await server.request("/api/add-meta?kind=movies");
    expect(status).toBe(200);
    expect(json.instanceId).toBe("radarr-4k");
    expect(json.instances.map((i: { instanceId: string }) => i.instanceId)).toEqual([
      "radarr",
      "radarr-4k",
    ]);
    // The only series manager is down: its error is reported.
    const series = await server.request("/api/add-meta?kind=series");
    expect(series.status).toBe(502);
    expect(series.json.error).toBeTypeOf("string");
  });

  it("routes addressed to a broken instance fail with a 502 and its reason", async () => {
    for (const path of ["/api/library/radarr/1/details", "/api/add-meta?instanceId=radarr"]) {
      const { status, json } = await server.request(path);
      expect(status, path).toBe(502);
      expect(json.error, path).toMatch(/API key incorrect/i);
    }
    // ...while the same route on the healthy instance works.
    expect((await server.request("/api/library/radarr-4k/101/details")).status).toBe(200);
  });

  it("explore with a rejected TMDB key is a 502 with a message", async () => {
    for (const path of ["/api/explore?category=popular", "/api/explore/101"]) {
      const { status, json } = await server.request(path);
      expect(status, path).toBe(502);
      expect(json.error, path).toBeTypeOf("string");
      expect(json.error.length, path).toBeGreaterThan(0);
    }
  });

  it("features and pages stay up", async () => {
    expect((await server.request("/api/features")).status).toBe(200);
    expect((await server.request("/")).status).toBe(200);
  });
});

describe("every service unreachable", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack({
      modes: Object.fromEntries(
        ["radarr", "radarr-4k", "sonarr", "sabnzbd", "qbittorrent", "plex", "jellyfin", "tmdb"].map(
          (s) => [s, "unreachable"]
        )
      ),
    });
    server = await startServer({ env: stack.env });
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  it("reports every instance down, quickly, with 200s on the aggregate routes", async () => {
    const started = Date.now();
    const [status, downloads, library, storage] = await Promise.all(
      ["/api/status", "/api/downloads", "/api/library", "/api/storage"].map((p) =>
        server.request(p)
      )
    );
    expect(Date.now() - started).toBeLessThan(5000);

    expect([status.status, downloads.status, library.status, storage.status]).toEqual([
      200, 200, 200, 200,
    ]);
    expect(status.json.instances).toHaveLength(7);
    for (const i of status.json.instances) {
      expect(i.state, i.id).toBe("down");
      expect(i.error, i.id).toBeTypeOf("string");
    }
    for (const i of downloads.json.instances) expect(i.up).toBe(false);
    expect(downloads.json.items).toEqual([]);
    for (const body of [library.json, storage.json]) {
      expect(body.instances).toHaveLength(3);
      for (const i of body.instances) expect(i.up).toBe(false);
    }
    expect(library.json.items).toEqual([]);
  });
});
