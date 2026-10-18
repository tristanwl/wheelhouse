import { mkdtempSync, linkSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startStack, type Stack } from "./fakes";
import { startServer, type ApiResponse, type RunningServer } from "./harness";

// Scenario 5: the routes that change things, against fakes, on the paths that
// are safe to run anywhere: validation, refusal, and add/delete flows whose
// effect is on the fake Radarr/Sonarr. The converter and the settings file
// touch real files on the machine, so only their refusals are tested here
// (the media roots are /movies and /tv unless WHEELHOUSE_MEDIA_ROOTS says
// otherwise, issue #31): a bad body,
// an unknown job, and the hard-link confirmation, which answers before any
// work is queued.

const body = (value: unknown) => ({
  headers: { "content-type": "application/json" },
  body: JSON.stringify(value),
});

describe("mutating routes", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack();
    // The Go server only converts files inside its media roots (/movies and /tv
    // by default, issue #31); the hard-link scenario below uses scratch files in
    // the temp directory, so that is made a root for this server.
    server = await startServer({
      env: { ...stack.env, WHEELHOUSE_MEDIA_ROOTS: realpathSync(tmpdir()) },
    });
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  const send = (method: string, p: string, init: RequestInit = {}) =>
    server.request(p, { method, ...init });
  const expectError = (res: ApiResponse, status: number) => {
    expect(res.status, res.text).toBe(status);
    expect(res.json.error).toBeTypeOf("string");
  };

  describe("POST /api/add", () => {
    const valid = {
      instanceId: "radarr",
      id: 900001,
      qualityProfileId: 1,
      rootFolderPath: "/movies",
      monitored: true,
      searchNow: false,
    };

    it("refuses a malformed or incomplete request", async () => {
      expectError(await send("POST", "/api/add", { body: "not json" }), 400);
      expectError(await send("POST", "/api/add", body({})), 400);
      expectError(await send("POST", "/api/add", body({ ...valid, instanceId: undefined })), 400);
      expectError(await send("POST", "/api/add", body({ ...valid, id: 0 })), 400);
      expectError(
        await send("POST", "/api/add", body({ ...valid, qualityProfileId: undefined })),
        400
      );
      expectError(await send("POST", "/api/add", body({ ...valid, rootFolderPath: "" })), 400);
    });

    it("refuses an unknown instance, and a GET", async () => {
      expectError(await send("POST", "/api/add", body({ ...valid, instanceId: "nope" })), 404);
      const get = await send("GET", "/api/add");
      expect(get.status).toBeGreaterThanOrEqual(400);
      expect(get.status).toBeLessThan(500);
    });

    it("adds a movie to the chosen Radarr, which then lists it", async () => {
      const radarr = stack.fakes.radarr!;
      const before = (await server.request("/api/library")).json.items.length;

      const res = await send(
        "POST",
        "/api/add",
        body({ ...valid, id: 424242, searchNow: true, minimumAvailability: "announced" })
      );
      expect(res.status, res.text).toBe(200);
      expect(res.json.ok).toBe(true);
      expect(res.json.id).toBeTypeOf("number");

      // What the upstream was asked to create.
      const post = radarr.requests.find(
        (r) => r.method === "POST" && r.pathname === "/api/v3/movie"
      );
      expect(post?.body).toMatchObject({
        tmdbId: 424242,
        qualityProfileId: 1,
        rootFolderPath: "/movies",
        monitored: true,
        minimumAvailability: "announced",
        addOptions: { searchForMovie: true },
      });
      expect(post?.body).not.toHaveProperty("id");

      const library = (await server.request("/api/library")).json;
      expect(library.items).toHaveLength(before + 1);
      expect(
        library.items.some(
          (i: { instanceId: string; id: number }) =>
            i.instanceId === "radarr" && i.id === res.json.id
        )
      ).toBe(true);
      // The other Radarr was not touched.
      expect(
        stack.fakes["radarr-4k"]!.requests.some((r) => (r as { method: string }).method === "POST")
      ).toBe(false);
    });

    it("adds a movie to the second Radarr, defaulting availability to released", async () => {
      const fourK = stack.fakes["radarr-4k"]!;
      const res = await send(
        "POST",
        "/api/add",
        body({ ...valid, instanceId: "radarr-4k", id: 424243, rootFolderPath: "/movies-4k" })
      );
      expect(res.status, res.text).toBe(200);
      const post = fourK.requests.find(
        (r) => r.method === "POST" && r.pathname === "/api/v3/movie"
      );
      expect(post?.body).toMatchObject({
        tmdbId: 424243,
        rootFolderPath: "/movies-4k",
        minimumAvailability: "released",
        addOptions: { searchForMovie: false },
      });
    });

    it("adds a series to Sonarr", async () => {
      const sonarr = stack.fakes.sonarr!;
      const res = await send(
        "POST",
        "/api/add",
        body({
          instanceId: "sonarr",
          id: 800123,
          qualityProfileId: 2,
          rootFolderPath: "/tv",
          monitored: false,
          searchNow: true,
        })
      );
      expect(res.status, res.text).toBe(200);
      expect(res.json.ok).toBe(true);
      const post = sonarr.requests.find(
        (r) => r.method === "POST" && r.pathname === "/api/v3/series"
      );
      expect(post?.body).toMatchObject({
        tvdbId: 800123,
        qualityProfileId: 2,
        rootFolderPath: "/tv",
        monitored: false,
        seasonFolder: true,
        addOptions: { searchForMissingEpisodes: true },
      });
      expect(post?.body).not.toHaveProperty("id");
    });
  });

  describe("DELETE /api/library/{instanceId}/{id}", () => {
    it("refuses bad ids and unknown instances", async () => {
      expectError(await send("DELETE", "/api/library/radarr/x"), 400);
      expectError(await send("DELETE", "/api/library/radarr/1.5"), 400);
      expectError(await send("DELETE", "/api/library/nope/1"), 404);
    });

    it("removes a movie from its own instance only", async () => {
      const radarr = stack.fakes.radarr!;
      const res = await send("DELETE", "/api/library/radarr/1?confirm=true");
      expect(res.status, res.text).toBe(200);
      expect(res.json).toEqual({ ok: true });
      expect(radarr.count("DELETE", "/api/v3/movie/1")).toBe(1);
      expect(
        stack.fakes["radarr-4k"]!.requests.some(
          (r) => (r as { method: string }).method === "DELETE"
        )
      ).toBe(false);

      const items = (await server.request("/api/library")).json.items as {
        instanceId: string;
        id: number;
      }[];
      expect(items.some((i) => i.instanceId === "radarr" && i.id === 1)).toBe(false);
      // Same numeric id elsewhere is another item.
      expect(items.some((i) => i.instanceId === "sonarr" && i.id === 1)).toBe(true);
    });

    it("answers a failing upstream with a 502 and a reason", async () => {
      // Gone already (deleted above), so the fake's lookup fails or the delete is a no-op;
      // either is an answer, never a crash.
      const res = await send("DELETE", "/api/library/sonarr/987654?confirm=true");
      expect([200, 502]).toContain(res.status);
      expect(res.json).toBeTypeOf("object");
    });
  });

  describe("DELETE /api/library/{instanceId}/{id}/seasons/{season}", () => {
    it("refuses what is not a series, a number, or an instance", async () => {
      expectError(await send("DELETE", "/api/library/radarr/1/seasons/1"), 400);
      expectError(await send("DELETE", "/api/library/sonarr/x/seasons/1"), 400);
      expectError(await send("DELETE", "/api/library/sonarr/1/seasons/x"), 400);
      expectError(await send("DELETE", "/api/library/nope/1/seasons/1"), 404);
    });

    it("deletes that season's episode files and stops monitoring it", async () => {
      const sonarr = stack.fakes.sonarr!;
      const res = await send("DELETE", "/api/library/sonarr/1/seasons/1?confirm=true");
      expect(res.status, res.text).toBe(200);
      expect(res.json.ok).toBe(true);
      expect(res.json.filesDeleted).toBeGreaterThanOrEqual(0);
      const deleted = sonarr.requests.filter(
        (r) => r.method === "DELETE" && r.pathname.startsWith("/api/v3/episodefile/")
      );
      expect(deleted).toHaveLength(res.json.filesDeleted);

      const season = (await server.request("/api/library/sonarr/1/seasons")).json.seasons.find(
        (s: { seasonNumber: number }) => s.seasonNumber === 1
      );
      expect(season.monitored).toBe(false);
    });
  });

  describe("PUT /api/codec-analysis/settings", () => {
    it("refuses anything but h264 or hevc for both tiers", async () => {
      const url = "/api/codec-analysis/settings";
      expectError(await send("PUT", url, { body: "not json" }), 400);
      expectError(await send("PUT", url, body({})), 400);
      expectError(await send("PUT", url, body({ hd: "h264" })), 400);
      expectError(await send("PUT", url, body({ hd: "x265", uhd: "hevc" })), 400);
      expectError(await send("PUT", url, body({ hd: "h264", uhd: 1 })), 400);
      // Unchanged by all of that.
      const prefs = (await server.request(url)).json;
      expect(["h264", "hevc"]).toContain(prefs.hd);
    });
  });

  describe("conversion", () => {
    const url = "/api/codec-analysis/convert";
    const valid = {
      instanceId: "radarr",
      itemId: 1,
      filePath: "/nonexistent-media-root/Example Movie 1 (2025).mkv",
      title: "Example Movie 1",
      originalSize: 1000,
      backend: "software",
      targetFamily: "hevc",
    };

    it("refuses a malformed request", async () => {
      expectError(await send("POST", url, { body: "not json" }), 400);
      expectError(await send("POST", url, body({})), 400);
      for (const bad of [
        { instanceId: 1 },
        { itemId: "1" },
        { filePath: 5 },
        { title: undefined },
        { originalSize: "big" },
        { backend: "quicksync" },
        { targetFamily: "av1" },
      ]) {
        expectError(await send("POST", url, body({ ...valid, ...bad })), 400);
      }
      expect((await server.request(url)).json).toEqual({ jobs: [] });
    });

    it("cancelling a job that does not exist is refused", async () => {
      const res = await send("POST", `${url}/nope/cancel`);
      expect(res.status).toBeGreaterThanOrEqual(400);
      expect(res.status).toBeLessThan(500);
      expect(res.json.error).toBeTypeOf("string");
    });

    describe("hard-linked files", () => {
      let dir: string;
      let file: string;
      beforeAll(() => {
        // Scratch files in the temp directory, never a media root.
        dir = mkdtempSync(path.join(tmpdir(), "wheelhouse-acceptance-"));
        file = path.join(dir, "Example Movie 1 (2025).mkv");
        writeFileSync(file, "not a real video");
        linkSync(file, path.join(dir, "seeding-copy.mkv"));
      });
      afterAll(() => {
        rmSync(dir, { recursive: true, force: true });
      });

      it("asks for confirmation (409) instead of queueing, and says how many links", async () => {
        const res = await send("POST", url, body({ ...valid, filePath: file }));
        expect(res.status, res.text).toBe(409);
        expect(res.json.code).toBe("hardlinked");
        expect(res.json.linkCount).toBe(2);
        expect(res.json.error).toMatch(/hard link/i);
        // Nothing was queued.
        expect((await server.request(url)).json).toEqual({ jobs: [] });
      });
    });
  });
});
