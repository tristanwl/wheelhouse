import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { GET as getAddMeta, type AddMeta } from "../add-meta/route";
import { POST as add } from "../add/route";
import { GET, type SearchResponse } from "./route";

describe("search, add-meta and add across instances", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
  });
  afterEach(() => stack.stop());

  const search = (kind: string, term = "example") =>
    GET(new Request(`http://localhost/api/search?kind=${kind}&term=${term}`));

  it("searches every instance of the kind and tags hits with their instance", async () => {
    await stack.radarr();
    const fourK = await stack.radarr4k({ movies: [] });
    await stack.sonarr();

    const body = (await (await search("movies")).json()) as SearchResponse;
    const byInstance = (id: string) => body.results.filter((h) => h.instanceId === id);

    expect(body.results).toHaveLength(4);
    expect(byInstance("radarr")).toHaveLength(2);
    expect(byInstance("radarr-4k")).toHaveLength(2);
    expect(byInstance("radarr-4k")[0].instanceName).toBe("Radarr 4K");
    // The default one has the first hit in its library; the (empty) 4K one doesn't.
    expect(byInstance("radarr").map((h) => h.alreadyAdded)).toEqual([true, false]);
    expect(byInstance("radarr-4k").map((h) => h.alreadyAdded)).toEqual([false, false]);
    expect(fourK.count("GET", "/api/v3/movie/lookup")).toBe(1);
  });

  it("does not search Sonarr for movies, or Radarr for series", async () => {
    const radarr = await stack.radarr();
    const sonarr = await stack.sonarr();
    const series = (await (await search("series")).json()) as SearchResponse;
    expect(series.results.every((h) => h.instanceId === "sonarr")).toBe(true);
    expect(radarr.count("GET", "/api/v3/movie/lookup")).toBe(0);
    await search("movies");
    expect(sonarr.count("GET", "/api/v3/series/lookup")).toBe(1);
  });

  it("keeps the first instance's results when another fails, and says which", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: [] }, "wrong-key-value-123");
    const body = (await (await search("movies")).json()) as SearchResponse;
    expect(body.results.every((h) => h.instanceId === "radarr")).toBe(true);
    expect(body.problems).toEqual([
      { instanceId: "radarr-4k", name: "Radarr 4K", error: "radarr-4k: API key incorrect" },
    ]);
  });

  it("fails with the reason when every instance fails", async () => {
    await stack.radarr({}, "wrong-key-value-123");
    const res = await search("movies");
    expect(res.status).toBe(502);
    expect(((await res.json()) as { error: string }).error).toBe("radarr: API key incorrect");
  });

  it("says plainly when no instance of the kind is configured", async () => {
    await stack.sonarr();
    const res = await search("movies");
    expect(res.status).toBe(404);
    expect(((await res.json()) as { error: string }).error).toBe(
      "No Radarr instance is configured"
    );
  });

  it("rejects an unknown kind", async () => {
    expect((await search("radarr")).status).toBe(400);
  });

  it("add-meta describes the requested instance, or the first ready one of a kind", async () => {
    await stack.radarr();
    await stack.radarr4k({
      movies: fourKMovies(),
      rootFolders: [{ id: 7, path: "/movies-4k", accessible: true, freeSpace: 1 }],
    });
    const meta = async (query: string) =>
      (await (
        await getAddMeta(new Request(`http://localhost/api/add-meta?${query}`))
      ).json()) as AddMeta;

    expect(await meta("instanceId=radarr-4k")).toMatchObject({
      instanceId: "radarr-4k",
      instanceName: "Radarr 4K",
      rootFolderPath: "/movies-4k",
    });
    expect(await meta("kind=movies")).toMatchObject({
      instanceId: "radarr",
      rootFolderPath: "/movies",
    });
    const missing = await getAddMeta(new Request("http://localhost/api/add-meta?instanceId=nope"));
    expect(missing.status).toBe(404);
  });

  it("add sends the movie to the instance it names, and only that one", async () => {
    const radarr = await stack.radarr();
    const fourK = await stack.radarr4k({ movies: [] });

    const res = await add(
      new Request("http://localhost/api/add", {
        method: "POST",
        body: JSON.stringify({
          instanceId: "radarr-4k",
          id: 900090,
          qualityProfileId: 1,
          rootFolderPath: "/movies-4k",
          monitored: true,
          searchNow: false,
        }),
      })
    );
    expect(await res.json()).toMatchObject({ ok: true, title: "Example Movie 90" });
    expect(fourK.count("POST", "/api/v3/movie")).toBe(1);
    expect(radarr.count("POST", "/api/v3/movie")).toBe(0);
    expect(fourK.requests.find((r) => r.method === "POST")?.body).toMatchObject({
      rootFolderPath: "/movies-4k",
      qualityProfileId: 1,
      minimumAvailability: "released",
      addOptions: { searchForMovie: false },
    });
  });

  it("add builds a series payload for a Sonarr instance", async () => {
    const sonarr = await stack.sonarr();
    const res = await add(
      new Request("http://localhost/api/add", {
        method: "POST",
        body: JSON.stringify({
          instanceId: "sonarr",
          id: 800099,
          qualityProfileId: 1,
          rootFolderPath: "/tv",
          monitored: true,
          searchNow: true,
        }),
      })
    );
    expect((await res.json()).ok).toBe(true);
    expect(sonarr.requests.find((r) => r.method === "POST")?.body).toMatchObject({
      seasonFolder: true,
      addOptions: { searchForMissingEpisodes: true },
    });
  });

  it("add refuses an unknown instance", async () => {
    await stack.radarr();
    const res = await add(
      new Request("http://localhost/api/add", {
        method: "POST",
        body: JSON.stringify({
          instanceId: "radarr-nope",
          id: 1,
          qualityProfileId: 1,
          rootFolderPath: "/x",
        }),
      })
    );
    expect(res.status).toBe(404);
  });
});
