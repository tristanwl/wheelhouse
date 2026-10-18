import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getPopular, pickTrailerKey, type TmdbVideo } from "./tmdb-client";

function video(overrides: Partial<TmdbVideo>): TmdbVideo {
  return { key: "k", site: "YouTube", type: "Trailer", official: true, name: "x", ...overrides };
}

describe("pickTrailerKey", () => {
  it("returns null without videos", () => {
    expect(pickTrailerKey(undefined)).toBeNull();
    expect(pickTrailerKey([])).toBeNull();
  });

  it("prefers an official video named 'Official Trailer'", () => {
    const key = pickTrailerKey([
      video({ key: "teaser", type: "Teaser" }),
      video({ key: "plain", name: "Trailer 2" }),
      video({ key: "best", name: "Official Trailer" }),
    ]);
    expect(key).toBe("best");
  });

  it("falls back official trailer -> any trailer -> teaser", () => {
    expect(pickTrailerKey([video({ key: "a", official: false }), video({ key: "b" })])).toBe("b");
    expect(pickTrailerKey([video({ key: "a", official: false })])).toBe("a");
    expect(pickTrailerKey([video({ key: "t", type: "Teaser" })])).toBe("t");
  });

  it("ignores non-YouTube videos", () => {
    expect(pickTrailerKey([video({ site: "Vimeo" })])).toBeNull();
  });
});

describe("tmdb list paging", () => {
  const requestedPages: number[] = [];

  beforeEach(() => {
    requestedPages.length = 0;
    process.env.TMDB_API_KEY = "test-key";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string) => {
        const page = Number(new URL(input).searchParams.get("page"));
        requestedPages.push(page);
        const results = Array.from({ length: 20 }, (_, i) => ({
          id: page * 100 + i,
          title: `Movie ${page}-${i}`,
          overview: "",
          release_date: "2024-01-01",
          poster_path: null,
          vote_average: 7,
        }));
        return { ok: true, json: async () => ({ results, total_pages: 600 }) };
      })
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    delete process.env.TMDB_API_KEY;
  });

  it("fetches TMDB pages 1-2 for the first batch of 40", async () => {
    const { results, hasMore } = await getPopular(40, 1);
    expect(requestedPages.sort((a, b) => a - b)).toEqual([1, 2]);
    expect(results).toHaveLength(40);
    expect(hasMore).toBe(true);
  });

  it("maps batch N to the next TMDB pages", async () => {
    await getPopular(40, 2);
    expect(requestedPages.sort((a, b) => a - b)).toEqual([3, 4]);
  });

  it("reports no more once TMDB's page cap is reached", async () => {
    // Batch 250 of 40 covers TMDB pages 499-500, the last allowed.
    const last = await getPopular(40, 250);
    expect(requestedPages.sort((a, b) => a - b)).toEqual([499, 500]);
    expect(last.hasMore).toBe(false);
  });

  it("returns nothing, without fetching, past the cap", async () => {
    const past = await getPopular(40, 251);
    expect(requestedPages).toEqual([]);
    expect(past).toEqual({ results: [], hasMore: false });
  });
});
