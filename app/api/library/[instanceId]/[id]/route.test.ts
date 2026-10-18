import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { GET as getLibrary, type LibraryResponse } from "../../route";
import { GET as getSeasons } from "./seasons/route";
import { DELETE as deleteSeason } from "./seasons/[season]/route";
import { GET as getDetails } from "./details/route";
import { DELETE } from "./route";

const params = (instanceId: string, id: string) => ({
  params: Promise.resolve({ instanceId, id }),
});
const seasonParams = (instanceId: string, id: string, season: string) => ({
  params: Promise.resolve({ instanceId, id, season }),
});
const req = new Request("http://localhost");

describe("/api/library/[instanceId]/[id]", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
  });
  afterEach(() => stack.stop());

  it("deletes from the instance it is addressed to, and the next listing reflects it", async () => {
    const radarr = await stack.radarr();
    const fourK = await stack.radarr4k({ movies: fourKMovies() });

    const before = (await (await getLibrary()).json()) as LibraryResponse;
    const doomed = before.items.find((i) => i.instanceId === "radarr-4k")!;
    expect(radarr.count("GET", "/api/v3/movie")).toBe(1);

    const res = await DELETE(req, params("radarr-4k", String(doomed.id)));
    expect(await res.json()).toEqual({ ok: true });
    expect(fourK.count("DELETE", `/api/v3/movie/${doomed.id}`)).toBe(1);
    expect(radarr.requests.some((r) => r.method === "DELETE")).toBe(false);

    // Inside the cache window, yet the deleted movie is gone: the write invalidated 4K's cache.
    const after = (await (await getLibrary()).json()) as LibraryResponse;
    expect(after.items.some((i) => i.instanceId === "radarr-4k" && i.id === doomed.id)).toBe(false);
    expect(after.items.filter((i) => i.instanceId === "radarr-4k")).toHaveLength(1);
    expect(fourK.count("GET", "/api/v3/movie")).toBe(2);
    // The other instance's cache was left alone.
    expect(radarr.count("GET", "/api/v3/movie")).toBe(1);
  });

  it("answers 404 for an unknown instance and 400 for a bad id", async () => {
    await stack.radarr();
    expect((await DELETE(req, params("radarr-nope", "1"))).status).toBe(404);
    expect((await DELETE(req, params("radarr", "abc"))).status).toBe(400);
  });

  it("explains an instance that isn't usable instead of crashing", async () => {
    await stack.radarr({}, "wrong-key-value-123");
    const res = await DELETE(req, params("radarr", "1"));
    expect(res.status).toBe(502);
    expect(((await res.json()) as { error: string }).error).toBe("radarr: API key incorrect");
  });

  it("gives details for a movie, and for a series, by instance", async () => {
    await stack.radarr();
    await stack.sonarr();
    const movie = await (await getDetails(req, params("radarr", "1"))).json();
    expect(movie).toMatchObject({
      title: "Example Movie 1",
      studio: "Example Studio",
      network: null,
    });
    const series = await (await getDetails(req, params("sonarr", "1"))).json();
    expect(series).toMatchObject({
      title: "Example Series 1",
      network: "Example Network",
      studio: null,
    });
  });

  it("serves seasons for a series instance only", async () => {
    await stack.radarr();
    await stack.sonarr();
    const body = (await (await getSeasons(req, params("sonarr", "1"))).json()) as {
      title: string;
      seasons: unknown[];
    };
    expect(body.title).toBe("Example Series 1");
    expect(body.seasons.length).toBeGreaterThan(0);
    expect((await getSeasons(req, params("radarr", "1"))).status).toBe(400);
    expect((await deleteSeason(req, seasonParams("radarr", "1", "1"))).status).toBe(400);
  });

  it("deletes a season's files and stops monitoring it", async () => {
    const sonarr = await stack.sonarr();
    const [file] = sonarr.state.episodeFiles.filter((f) => f.seriesId === 1);
    const res = await deleteSeason(req, seasonParams("sonarr", "1", String(file.seasonNumber)));
    expect(await res.json()).toEqual({ ok: true, filesDeleted: 1 });
    expect(sonarr.count("DELETE", `/api/v3/episodefile/${file.id}`)).toBe(1);
    const put = sonarr.requests.find((r) => r.method === "PUT")!;
    const seasons = (put.body as { seasons: { seasonNumber: number; monitored: boolean }[] })
      .seasons;
    expect(seasons.find((s) => s.seasonNumber === file.seasonNumber)?.monitored).toBe(false);
  });
});
