import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fixtureMovies, fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { getMovieDetail, getPopular } from "@/lib/tmdb-client";
import { GET as getDetail, type ExploreDetail } from "./[id]/route";
import { GET, type ExploreHit } from "./route";

// TMDB is the one thing faked at the module boundary; the Radarr side is the
// real route handler, env config source and runtime against fake servers.
vi.mock("@/lib/tmdb-client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/tmdb-client")>()),
  getPopular: vi.fn(),
  getNowPlaying: vi.fn(),
  getRecentPhysicalReleases: vi.fn(),
  getMovieDetail: vi.fn(),
}));

const IN_DEFAULT = fixtureMovies()[0].tmdbId as number;
const IN_4K = fourKMovies()[0].tmdbId as number;
const IN_NEITHER = 5;

const tmdbMovie = (id: number) => ({
  id,
  title: `TMDB ${id}`,
  overview: "",
  release_date: "2024-01-01",
  poster_path: null,
  vote_average: 7,
});

describe("Explore's 'already in library' badge", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
    vi.mocked(getPopular).mockResolvedValue({
      results: [IN_DEFAULT, IN_4K, IN_NEITHER].map(tmdbMovie),
      hasMore: false,
    });
    vi.mocked(getMovieDetail).mockImplementation(async (id: number) => ({
      ...tmdbMovie(id),
      genres: [],
      runtime: 100,
      backdrop_path: null,
    }));
  });
  afterEach(() => stack.stop());

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

  it("shows nothing as added when no Radarr is configured", async () => {
    expect((await popular()).every((h) => !h.alreadyAdded)).toBe(true);
  });
});
