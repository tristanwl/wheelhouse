import { vi } from "vitest";

export const GOOD_KEY = "tmdb-key-0123456789abcdef";

export interface FakeTmdbOptions {
  /** ok: answers; unreachable: connection refused; hang: never answers (until aborted). */
  mode?: "ok" | "unreachable" | "hang";
  /** Ids returned by every list endpoint. */
  movieIds?: number[];
}

export const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

export const movie = (id: number) => ({
  id,
  title: `Example Movie ${id}`,
  overview: "A made-up plot.",
  release_date: "2024-01-01",
  poster_path: `/poster${id}.jpg`,
  vote_average: 7.5,
});

export const movieDetail = (id: number) => ({
  ...movie(id),
  genres: [{ id: 1, name: "Drama" }],
  runtime: 100,
  backdrop_path: "/backdrop.jpg",
  videos: { results: [] },
});

/**
 * TMDB is a fixed remote host (no URL setting), so unlike the other fakes this
 * one replaces global fetch for api.themoviedb.org and passes everything else
 * (the local fake servers) through. Behaves like TMDB: a missing or wrong key
 * is a 401 with a JSON body. Returns the requests seen and a restore function.
 */
export function installFakeTmdb(options: FakeTmdbOptions = {}) {
  const { mode = "ok", movieIds = [101, 102, 103] } = options;
  const requests: URL[] = [];
  const realFetch = globalThis.fetch;

  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input));
    if (url.hostname !== "api.themoviedb.org") return realFetch(input, init);
    requests.push(url);

    if (mode === "unreachable") {
      throw new TypeError("fetch failed", { cause: { code: "ECONNREFUSED" } });
    }
    if (mode === "hang") {
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () =>
          reject(new DOMException("The operation was aborted due to timeout", "TimeoutError"))
        );
      });
    }
    if (url.searchParams.get("api_key") !== GOOD_KEY) {
      return json({ status_code: 7, status_message: "Invalid API key." }, 401);
    }

    const path = url.pathname.replace(/^\/3/, "");
    if (path === "/configuration")
      return json({ images: { base_url: "http://image.example.com" } });
    const detail = /^\/movie\/(\d+)$/.exec(path);
    if (detail) {
      return json(movieDetail(Number(detail[1])));
    }
    // Any list endpoint.
    return json({ results: movieIds.map(movie), total_pages: 1 });
  });

  return { requests, restore: () => vi.unstubAllGlobals() };
}
