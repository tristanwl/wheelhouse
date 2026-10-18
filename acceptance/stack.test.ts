import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { fixtureMovies, fourKMovies } from "./fakes/media-manager/server";
import { startStack, type Stack } from "./fakes";
import { startServer, type RunningServer } from "./harness";
import { ALL_GET_CASES, PAGES } from "./routes";

// Scenario 2: every service configured with a healthy fake. Bodies are checked
// for shape and invariants, not exact content: timestamps, the order of equal
// items and ffmpeg's presence are properties of the run, not of the contract.

const DOWNLOAD_STATES = [
  "queued",
  "downloading",
  "stalled",
  "postprocessing",
  "seeding",
  "paused",
  "completed",
  "failed",
];
const INSTANCE_STATES = ["ok", "degraded", "needs-setup", "down"];

// The fake TMDB lists one movie that is in the first Radarr, one that is in
// the second (4K) Radarr, and one that is in neither.
const IN_RADARR = fixtureMovies()[0].tmdbId as number;
const IN_RADARR_4K = fourKMovies()[0].tmdbId as number;
const IN_NEITHER = 999_001;

describe("full stack with healthy fakes", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack({ tmdbMovieIds: [IN_RADARR, IN_RADARR_4K, IN_NEITHER] });
    server = await startServer({ env: stack.env });
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  const get = async (path: string, expectStatus = 200) => {
    const res = await server.request(path);
    expect(res.status, `${path}: ${res.text.slice(0, 300)}`).toBe(expectStatus);
    return res.json;
  };

  it.each(PAGES)("page $path answers 200 HTML", async ({ path }) => {
    const res = await server.request(path);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/text\/html/);
  });

  it("answers every GET route as the route table says", async () => {
    // All at once: also shows the server handles concurrent requests.
    const results = await Promise.all(ALL_GET_CASES.map((c) => server.request(c.path)));
    ALL_GET_CASES.forEach((c, i) => expect(results[i].status, c.name).toBe(c.full));
  });

  describe("/api/status", () => {
    it("lists every configured instance with a stable id, and none are down", async () => {
      const { instances } = await get("/api/status");
      const ids = instances.map((i: { id: string }) => i.id);
      expect([...ids].sort()).toEqual(
        ["jellyfin", "plex", "qbittorrent", "radarr", "radarr-4k", "sabnzbd", "sonarr"].sort()
      );
      for (const i of instances) {
        expect(INSTANCE_STATES).toContain(i.state);
        expect(i.state, `${i.id}: ${i.error}`).not.toBe("down");
        expect(i.name).toBeTypeOf("string");
        expect(i.providerId).toBeTypeOf("string");
        expect(["mediaManager", "downloadClient", "mediaServer"]).toContain(i.role);
        expect(i.version).toBeTypeOf("string");
        expect(i.detail).toBeTypeOf("object");
      }
    });

    it("names the second Radarr from its variable and groups instances by role", async () => {
      const { instances } = await get("/api/status");
      const byId = new Map(instances.map((i: { id: string }) => [i.id, i]));
      expect(byId.get("radarr-4k")).toMatchObject({ providerId: "radarr", name: "Radarr 4K" });
      expect(byId.get("radarr")).toMatchObject({ providerId: "radarr", name: "Radarr" });
      expect(byId.get("sabnzbd")).toMatchObject({ role: "downloadClient" });
      // Roles come out as contiguous groups, managers first.
      const roles: string[] = instances.map((i: { role: string }) => i.role);
      const order = ["mediaManager", "downloadClient", "mediaServer"];
      expect(roles.map((r) => order.indexOf(r))).toEqual(
        [...roles.map((r) => order.indexOf(r))].sort((a, b) => a - b)
      );
    });

    it("gives a link to each web UI without credentials", async () => {
      const { instances } = await get("/api/status");
      for (const i of instances) {
        expect(i.link, i.id).toBeTypeOf("object");
        expect(JSON.stringify(i.link)).not.toMatch(/@/);
      }
    });
  });

  describe("/api/downloads", () => {
    it("normalizes rows from both download clients", async () => {
      const body = await get("/api/downloads");
      expect(body.instances.map((i: { instanceId: string }) => i.instanceId).sort()).toEqual([
        "qbittorrent",
        "sabnzbd",
      ]);
      const protocols = Object.fromEntries(
        body.instances.map((i: { instanceId: string; protocol: string }) => [
          i.instanceId,
          i.protocol,
        ])
      );
      expect(protocols).toEqual({ sabnzbd: "usenet", qbittorrent: "torrent" });
      for (const i of body.instances) {
        expect(i.up, i.instanceId).toBe(true);
        expect(i.paused).toBeTypeOf("boolean");
        expect(i.speedBytesPerSec).toBeGreaterThanOrEqual(0);
      }

      expect(body.items.length).toBeGreaterThan(0);
      for (const row of body.items) {
        expect(row.id).toBeTypeOf("string");
        expect(row.name).toBeTypeOf("string");
        expect(["usenet", "torrent"]).toContain(row.protocol);
        expect(DOWNLOAD_STATES).toContain(row.state);
        expect(row.progress).toBeGreaterThanOrEqual(0);
        expect(row.progress).toBeLessThanOrEqual(1);
        expect(row.sizeBytes).toBeGreaterThanOrEqual(0);
        expect(row.remainingBytes).toBeGreaterThanOrEqual(0);
        expect(row.remainingBytes).toBeLessThanOrEqual(row.sizeBytes);
        expect(row.etaSeconds === null || row.etaSeconds >= 0).toBe(true);
        expect(["sabnzbd", "qbittorrent"]).toContain(row.instanceId);
        expect(row.protocol).toBe(protocols[row.instanceId]);
      }
      // Each client contributes rows; torrents carry their seeding details.
      const byInstance = (id: string) =>
        body.items.filter((r: { instanceId: string }) => r.instanceId === id);
      expect(byInstance("sabnzbd").length).toBeGreaterThan(0);
      expect(byInstance("qbittorrent").length).toBeGreaterThan(0);
      for (const row of byInstance("qbittorrent")) {
        if (row.state === "seeding") expect(row.torrent.ratio).toBeTypeOf("number");
      }
    });
  });

  describe("/api/features", () => {
    it("enables what the configuration unlocks", async () => {
      const { features } = await get("/api/features");
      type Feature = { id: string; available: boolean; missing: string[] };
      const byId = new Map<string, Feature>((features as Feature[]).map((f) => [f.id, f]));
      for (const id of ["dashboard", "library", "explore"]) {
        expect(byId.get(id)?.available, id).toBe(true);
        expect(byId.get(id)?.missing, id).toEqual([]);
      }
      // Codecs also needs ffmpeg on the machine running the server.
      const codecs = byId.get("codecs")!;
      expect(codecs.available).toBe(codecs.missing.length === 0);
    });
  });

  describe("library", () => {
    it("merges every media manager, biggest first", async () => {
      const body = await get("/api/library");
      expect(body.instances.map((i: { instanceId: string }) => i.instanceId).sort()).toEqual([
        "radarr",
        "radarr-4k",
        "sonarr",
      ]);
      for (const i of body.instances) expect(i.up, i.instanceId).toBe(true);
      const kinds = Object.fromEntries(
        body.instances.map((i: { instanceId: string; kind: string }) => [i.instanceId, i.kind])
      );
      expect(kinds).toEqual({ radarr: "movies", "radarr-4k": "movies", sonarr: "series" });

      expect(body.items.length).toBeGreaterThan(0);
      for (const item of body.items) {
        expect(kinds[item.instanceId]).toBe(item.kind);
        expect(item.instanceName).toBeTypeOf("string");
        expect(item.id).toBeTypeOf("number");
        expect(item.title).toBeTypeOf("string");
        expect(item.sizeOnDisk).toBeGreaterThanOrEqual(0);
        expect(item.monitored).toBeTypeOf("boolean");
        expect(item.rootFolderPath).toBeTypeOf("string");
        expect(Number.isNaN(Date.parse(item.added))).toBe(false);
      }
      const sizes: number[] = body.items.map((i: { sizeOnDisk: number }) => i.sizeOnDisk);
      expect(sizes).toEqual([...sizes].sort((a, b) => b - a));
      // Every instance contributes items, and the second Radarr's ids are its own.
      for (const id of ["radarr", "radarr-4k", "sonarr"]) {
        expect(
          body.items.some((i: { instanceId: string }) => i.instanceId === id),
          id
        ).toBe(true);
      }
    });

    it("gives movie and series details", async () => {
      for (const path of [
        "/api/library/radarr/1/details",
        "/api/library/radarr-4k/101/details",
        "/api/library/sonarr/1/details",
      ]) {
        const d = await get(path);
        expect(d.title, path).toBeTypeOf("string");
        expect(Array.isArray(d.genres)).toBe(true);
        expect(Array.isArray(d.ratings)).toBe(true);
        for (const r of d.ratings) {
          expect(r.source).toBeTypeOf("string");
          expect(r.value).toBeTypeOf("number");
        }
        for (const field of ["overview", "certification", "status", "posterUrl"]) {
          expect(d[field] === null || typeof d[field] === "string", `${path} ${field}`).toBe(true);
        }
      }
      expect((await get("/api/library/sonarr/1/details")).network).toBeTypeOf("string");
      expect((await get("/api/library/radarr/1/details")).studio).toBeTypeOf("string");
    });

    it("gives a series' seasons in order", async () => {
      const body = await get("/api/library/sonarr/1/seasons");
      expect(body.title).toBeTypeOf("string");
      const numbers: number[] = body.seasons.map((s: { seasonNumber: number }) => s.seasonNumber);
      expect(numbers.length).toBeGreaterThan(0);
      expect(numbers).toEqual([...numbers].sort((a, b) => a - b));
      for (const s of body.seasons) {
        expect(s.monitored).toBeTypeOf("boolean");
        for (const f of ["episodeFileCount", "episodeCount", "totalEpisodeCount", "sizeOnDisk"]) {
          expect(s[f], f).toBeGreaterThanOrEqual(0);
        }
      }
    });

    it("refuses what does not exist", async () => {
      await get("/api/library/nope/1/details", 404);
      await get("/api/library/nope/1/seasons", 404);
      await get("/api/library/radarr/1/seasons", 400);
      await get("/api/library/sonarr/x/seasons", 400);
      // An id the manager does not have is its upstream's error, not a crash.
      const missing = await server.request("/api/library/radarr/987654/details");
      expect([404, 502]).toContain(missing.status);
      expect(missing.json.error).toBeTypeOf("string");
    });
  });

  describe("/api/storage", () => {
    it("reports free space and library use per root folder", async () => {
      const body = await get("/api/storage");
      expect(body.instances).toHaveLength(3);
      for (const i of body.instances) expect(i.up).toBe(true);
      expect(new Set(body.entries.map((e: { instanceId: string }) => e.instanceId))).toEqual(
        new Set(["radarr", "radarr-4k", "sonarr"])
      );
      for (const e of body.entries) {
        expect(e.label).toBeTypeOf("string");
        expect(e.path).toMatch(/^\//);
        expect(e.freeBytes).toBeGreaterThanOrEqual(0);
        expect(e.libraryUsedBytes).toBeGreaterThanOrEqual(0);
      }
      // With two movie instances, the labels tell them apart.
      const labels = body.entries
        .filter((e: { instanceId: string }) => e.instanceId.startsWith("radarr"))
        .map((e: { label: string }) => e.label);
      expect(new Set(labels).size).toBe(2);
    });
  });

  describe("explore", () => {
    it("lists each category and marks movies in either Radarr", async () => {
      for (const category of ["popular", "theaters", "bluray"]) {
        const body = await get(`/api/explore?category=${category}`);
        expect(body.hasMore).toBe(false);
        expect(body.results.map((r: { id: number }) => r.id)).toEqual([
          IN_RADARR,
          IN_RADARR_4K,
          IN_NEITHER,
        ]);
        expect(body.results.map((r: { alreadyAdded: boolean }) => r.alreadyAdded)).toEqual([
          true,
          true,
          false,
        ]);
        for (const r of body.results) {
          expect(r.title).toBeTypeOf("string");
          expect(r.year).toBe(2024);
          expect(r.voteAverage).toBeTypeOf("number");
          expect(r.posterUrl === null || /^https?:\/\//.test(r.posterUrl)).toBe(true);
        }
      }
    });

    it("honours limit", async () => {
      const body = await get("/api/explore?category=popular&limit=2");
      expect(body.results).toHaveLength(2);
    });

    it("gives a movie's detail", async () => {
      const d = await get(`/api/explore/${IN_RADARR_4K}`);
      expect(d).toMatchObject({ id: IN_RADARR_4K, alreadyAdded: true, year: 2024 });
      expect(d.genres).toEqual(["Drama"]);
      expect(d.runtime).toBe(100);
      expect(d.trailerKey).toBeNull();
      expect((await get(`/api/explore/${IN_NEITHER}`)).alreadyAdded).toBe(false);
    });
  });

  describe("search and add-meta", () => {
    it("searches every instance of the kind and marks what is in the library", async () => {
      const body = await get("/api/search?kind=movies&term=example");
      expect(body.problems).toBeUndefined();
      const instances = new Set(body.results.map((r: { instanceId: string }) => r.instanceId));
      expect(instances).toEqual(new Set(["radarr", "radarr-4k"]));
      for (const id of instances) {
        const hits = body.results.filter((r: { instanceId: string }) => r.instanceId === id);
        expect(hits.length).toBeGreaterThan(0);
        expect(hits.length).toBeLessThanOrEqual(8);
      }
      for (const r of body.results) {
        expect(r.id).toBeGreaterThan(0);
        expect(r.alreadyAdded).toBeTypeOf("boolean");
        expect(r.title).toBeTypeOf("string");
        expect(r.year).toBeTypeOf("number");
        expect(r.overview).toBeTypeOf("string");
      }
      expect(body.results.some((r: { alreadyAdded: boolean }) => r.alreadyAdded)).toBe(true);
      expect(body.results.some((r: { alreadyAdded: boolean }) => !r.alreadyAdded)).toBe(true);

      const series = await get("/api/search?kind=series&term=example");
      expect(series.results.length).toBeGreaterThan(0);
      for (const r of series.results) expect(r.instanceId).toBe("sonarr");
    });

    it("gives what the add form needs", async () => {
      for (const [path, id] of [
        ["/api/add-meta?kind=movies", /^radarr/],
        ["/api/add-meta?kind=series", /^sonarr$/],
        ["/api/add-meta?instanceId=radarr-4k", /^radarr-4k$/],
      ] as const) {
        const meta = await get(path);
        expect(meta.instanceId, path).toMatch(id);
        expect(meta.instanceName).toBeTypeOf("string");
        expect(meta.qualityProfiles.length).toBeGreaterThan(0);
        for (const p of meta.qualityProfiles) {
          expect(p.id).toBeTypeOf("number");
          expect(p.name).toBeTypeOf("string");
        }
        expect(meta.rootFolderPath).toMatch(/^\//);
      }
      await get("/api/add-meta?instanceId=nope", 404);
    });
  });

  describe("codecs", () => {
    it("analyses movies across both Radarr instances", async () => {
      const body = await get("/api/codec-analysis?kind=movies");
      expect(body.totalCount).toBeGreaterThan(0);
      expect(body.preferences).toEqual({ hd: "h264", uhd: "hevc" });
      expect(Object.keys(body.buckets).sort()).toEqual(["h264", "hevc", "legacy"]);
      const bucketCount = Object.values(body.buckets as Record<string, { count: number }>).reduce(
        (sum, b) => sum + b.count,
        0
      );
      expect(bucketCount).toBe(body.totalCount);
      expect(body.skippedCount).toBeGreaterThanOrEqual(0);
      for (const f of [...body.candidates, ...body.matching]) {
        expect(f.title).toBeTypeOf("string");
        expect(f.codec).toBeTypeOf("string");
        expect(f.size).toBeGreaterThan(0);
        expect(["radarr", "radarr-4k"]).toContain(f.instanceId);
        expect(f.itemId).toBeTypeOf("number");
        expect(f.filePath).toMatch(/^\//);
        expect(["hd", "uhd"]).toContain(f.tier);
      }
      for (const c of body.candidates) {
        expect(["h264", "hevc"]).toContain(c.targetFamily);
        expect(c.reason).toBeTypeOf("string");
      }
    });

    it("analyses series episodes, labelled by episode", async () => {
      const body = await get("/api/codec-analysis?kind=series");
      const files = [...body.candidates, ...body.matching];
      expect(files.length).toBeGreaterThan(0);
      for (const f of files) {
        expect(f.instanceId).toBe("sonarr");
        expect(f.title).toMatch(/ - (S\d{2}E\d{2}|Season \d+)/);
      }
    });

    it("reads preferences, encoders and the (empty) job list", async () => {
      const prefs = await get("/api/codec-analysis/settings");
      expect(["h264", "hevc"]).toContain(prefs.hd);
      expect(["h264", "hevc"]).toContain(prefs.uhd);

      // Which encoders exist depends on the machine; only the shape is the contract.
      const { encoders } = await get("/api/codec-analysis/encoders");
      expect(Array.isArray(encoders)).toBe(true);
      for (const e of encoders) {
        expect(["vaapi", "nvenc", "software"]).toContain(e.backend);
        expect(["h264", "hevc"]).toContain(e.family);
        expect(e.label).toBeTypeOf("string");
        expect(e.available).toBeTypeOf("boolean");
      }

      expect(await get("/api/codec-analysis/convert")).toEqual({ jobs: [] });
      await get("/api/codec-analysis/convert/nope", 404);
    });
  });
});
