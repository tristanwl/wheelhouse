import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import {
  fixtureMovies,
  fourKMovies,
  relocateMovies,
} from "@/lib/providers/media-manager/__fixtures__/server";
import radarrRoots from "@/lib/providers/media-manager/__fixtures__/radarr/rootfolder.json";
import sonarrSeries from "@/lib/providers/media-manager/__fixtures__/sonarr/series.json";
import { GET, type StorageResponse } from "./route";

const sum = (items: { sizeOnDisk: number }[]) => items.reduce((n, i) => n + i.sizeOnDisk, 0);
const root = (id: number, path: string, freeSpace: number) => ({
  ...radarrRoots[0],
  id,
  path,
  freeSpace,
});

describe("/api/storage", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
  });
  afterEach(() => stack.stop());

  async function storage() {
    return (await (await GET()).json()) as StorageResponse;
  }

  it("attributes usage to the root folder that contains each item, not to every root", async () => {
    // Two root folders in one Radarr: the old code gave both the whole library's size.
    const inFirst = fixtureMovies();
    const inSecond = relocateMovies(
      fixtureMovies()
        .filter((m) => m.hasFile)
        .slice(0, 2),
      {
        root: "/movies-b",
        idOffset: 100,
        tmdbOffset: 100,
      }
    );
    await stack.radarr({
      movies: [...inFirst, ...inSecond],
      rootFolders: [root(1, "/movies", 111), root(2, "/movies-b", 222)],
    });

    const { entries } = await storage();
    expect(entries).toHaveLength(2);
    expect(entries[0]).toMatchObject({
      path: "/movies",
      freeBytes: 111,
      libraryUsedBytes: sum(inFirst as { sizeOnDisk: number }[]),
    });
    expect(entries[1]).toMatchObject({
      path: "/movies-b",
      freeBytes: 222,
      libraryUsedBytes: sum(inSecond as { sizeOnDisk: number }[]),
    });
    expect(entries[0].libraryUsedBytes).not.toBe(entries[1].libraryUsedBytes);
  });

  it("matches on whole path segments: /movies does not claim /movies-b/...", async () => {
    const other = relocateMovies(fixtureMovies().slice(0, 1), {
      root: "/movies-b",
      idOffset: 100,
      tmdbOffset: 100,
    });
    await stack.radarr({
      movies: other,
      rootFolders: [root(1, "/movies", 1), root(2, "/movies-b", 2)],
    });
    const { entries } = await storage();
    expect(entries.map((e) => [e.path, e.libraryUsedBytes > 0])).toEqual([
      ["/movies", false],
      ["/movies-b", true],
    ]);
  });

  it("gives an item to the deepest root folder when roots are nested", async () => {
    const movies = fixtureMovies().slice(0, 1) as { sizeOnDisk: number; path: string }[];
    movies[0].path = "/media/movies/uhd/Example Movie 1 (2025)";
    await stack.radarr({
      movies,
      rootFolders: [root(1, "/media/movies", 1), root(2, "/media/movies/uhd", 2)],
    });
    const { entries } = await storage();
    expect(entries.map((e) => e.libraryUsedBytes)).toEqual([0, movies[0].sizeOnDisk]);
  });

  it("lists TV and Movies for default instances, summing series by their size on disk", async () => {
    await stack.radarr();
    await stack.sonarr();
    const { entries, instances } = await storage();

    expect(entries.map((e) => [e.instanceId, e.label, e.path])).toEqual([
      ["sonarr", "TV", "/tv"],
      ["radarr", "Movies", "/movies"],
    ]);
    expect(entries[0].libraryUsedBytes).toBe(
      sonarrSeries.reduce((n, s) => n + (s.statistics?.sizeOnDisk ?? 0), 0)
    );
    expect(entries[1].libraryUsedBytes).toBe(sum(fixtureMovies() as { sizeOnDisk: number }[]));
    expect(instances.every((i) => i.up)).toBe(true);
  });

  it("shows both Radarr instances, each with its own usage and instance-named labels", async () => {
    await stack.radarr();
    const fourK = fourKMovies() as { sizeOnDisk: number }[];
    await stack.radarr4k({
      movies: fourK,
      rootFolders: [root(1, "/movies-4k", 333)],
    });

    const { entries } = await storage();
    expect(entries.map((e) => [e.instanceId, e.label, e.path, e.freeBytes])).toEqual([
      ["radarr", "Radarr", "/movies", radarrRoots[0].freeSpace],
      ["radarr-4k", "Radarr 4K", "/movies-4k", 333],
    ]);
    expect(entries[0].libraryUsedBytes).toBe(sum(fixtureMovies() as { sizeOnDisk: number }[]));
    expect(entries[1].libraryUsedBytes).toBe(sum(fourK));
  });

  it("reports an invalid second instance and still returns the first", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() }, "wrong-key-value-123");
    const { entries, instances } = await storage();

    expect(entries.map((e) => e.instanceId)).toEqual(["radarr"]);
    expect(instances).toEqual([
      { instanceId: "radarr", name: "Radarr", kind: "movies", up: true },
      {
        instanceId: "radarr-4k",
        name: "Radarr 4K",
        kind: "movies",
        up: false,
        error: "radarr-4k: API key incorrect",
      },
    ]);
  });

  it("returns nothing, without failing, when no media manager is configured", async () => {
    expect(await storage()).toEqual({ entries: [], instances: [] });
  });
});
