import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fixtureMovies, fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { GOOD_KEY, installFakeTmdb } from "@/lib/providers/metadata/__fixtures__/fake-tmdb";
import { GET as getDetail, type ExploreDetail } from "./[id]/route";
import { GET, type ExploreHit } from "./route";

// The Radarr side and the TMDB provider are real: route handler, env config
// source and runtime, against a fake TMDB (fetch-level) and fake Radarrs.

const IN_DEFAULT = fixtureMovies()[0].tmdbId as number;
const IN_4K = fourKMovies()[0].tmdbId as number;
const IN_NEITHER = 5;

describe("Explore's 'already in library' badge", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
    vi.stubEnv("TMDB_API_KEY", GOOD_KEY);
    installFakeTmdb({ movieIds: [IN_DEFAULT, IN_4K, IN_NEITHER] });
  });
  afterEach(async () => {
    vi.unstubAllGlobals();
    await stack.stop();
  });

  async function popular() {
    const res = await GET(new Request("http://localhost/api/explore?category=popular"));
    return ((await res.json()) as { results: ExploreHit[] }).results;
  }

  it("reflects the default instance", async () => {
    await stack.radarr();
    expect((await popular()).map((h) => [h.id, h.alreadyAdded])).toEqual([
      [IN_DEFAULT, true],
      [IN_4K, false],
      [IN_NEITHER, false],
    ]);
  });

  it("reflects both Radarr instances", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() });
    expect((await popular()).map((h) => [h.id, h.alreadyAdded])).toEqual([
      [IN_DEFAULT, true],
      [IN_4K, true],
      [IN_NEITHER, false],
    ]);
  });

  it("still works from the instances that answer when another is invalid", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() }, "wrong-key-value-123");
    expect((await popular()).map((h) => [h.id, h.alreadyAdded])).toEqual([
      [IN_DEFAULT, true],
      [IN_4K, false],
      [IN_NEITHER, false],
    ]);
  });

  it("applies to the detail view too", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() });
    const detail = async (id: number) => {
      const res = await getDetail(new Request("http://localhost"), {
        params: Promise.resolve({ id: String(id) }),
      });
      return ((await res.json()) as ExploreDetail).alreadyAdded;
    };
    expect(await detail(IN_DEFAULT)).toBe(true);
    expect(await detail(IN_4K)).toBe(true);
    expect(await detail(IN_NEITHER)).toBe(false);
  });

  it("explains itself when TMDB isn't configured, without calling it", async () => {
    vi.stubEnv("TMDB_API_KEY", "");
    const res = await GET(new Request("http://localhost/api/explore?category=popular"));
    expect(res.status).toBe(502);
    expect(((await res.json()) as { error: string }).error).toMatch(/TMDB is not configured/);
  });

  it("shows nothing as added when no Radarr is configured", async () => {
    expect((await popular()).every((h) => !h.alreadyAdded)).toBe(true);
  });
});
