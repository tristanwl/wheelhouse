import { env } from "./env";

export interface TmdbMovie {
  id: number;
  title: string;
  overview: string;
  release_date: string;
  poster_path: string | null;
  vote_average: number;
}

export interface TmdbVideo {
  key: string;
  site: string;
  type: string;
  official: boolean;
  name: string;
}

export interface TmdbMovieDetail extends TmdbMovie {
  genres: { id: number; name: string }[];
  runtime: number | null;
  backdrop_path: string | null;
  videos?: { results: TmdbVideo[] };
}

async function tmdbFetch<T>(path: string, params: Record<string, string> = {}): Promise<T> {
  const apiKey = env.tmdbApiKey;
  if (!apiKey) {
    throw new Error("TMDB_API_KEY not configured");
  }
  const search = new URLSearchParams({ api_key: apiKey, ...params });
  const res = await fetch(`https://api.themoviedb.org/3${path}?${search}`, {
    cache: "no-store",
  });
  if (!res.ok) {
    throw new Error(`tmdb ${path} failed: ${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

// TMDB list endpoints page at 20 results/page. To serve more than one page's
// worth (the Explore "All" view loads 40 per batch), fetch as many pages as
// needed in parallel and concatenate — TMDB has no larger-page-size option.
// `page` is 1-based in units of `count`, so page 2 of count=40 is TMDB pages
// 3–4. TMDB refuses pages past 500, so hasMore also stops there.
const TMDB_MAX_PAGE = 500;

export interface TmdbListResult {
  results: TmdbMovie[];
  hasMore: boolean;
}

async function tmdbList(
  path: string,
  params: Record<string, string>,
  count: number,
  page = 1
): Promise<TmdbListResult> {
  const pages = Math.max(1, Math.ceil(count / 20));
  const firstPage = (page - 1) * pages + 1;
  const tmdbPages = Array.from({ length: pages }, (_, i) => firstPage + i).filter(
    (p) => p <= TMDB_MAX_PAGE
  );
  if (tmdbPages.length === 0) return { results: [], hasMore: false };
  const pageResults = await Promise.all(
    tmdbPages.map((p) =>
      tmdbFetch<{ results: TmdbMovie[]; total_pages: number }>(path, {
        ...params,
        page: String(p),
      })
    )
  );
  const lastFetched = tmdbPages[tmdbPages.length - 1];
  const totalPages = Math.min(pageResults[0]?.total_pages ?? 0, TMDB_MAX_PAGE);
  return {
    results: pageResults.flatMap((r) => r.results).slice(0, count),
    hasMore: lastFetched < totalPages,
  };
}

export async function getNowPlaying(count = 20, page = 1): Promise<TmdbListResult> {
  return tmdbList("/movie/now_playing", { region: "US" }, count, page);
}

export async function getPopular(count = 20, page = 1): Promise<TmdbListResult> {
  return tmdbList("/movie/popular", { region: "US" }, count, page);
}

// TMDB has no "Blu-ray" concept specifically, but release_type=5 is the
// physical-media release date in its per-region release_dates data — the
// closest real equivalent. Sorted newest-first and capped at today so
// announced-but-unreleased physical dates don't show up as "recent".
export async function getRecentPhysicalReleases(
  count = 20,
  page = 1
): Promise<TmdbListResult> {
  const today = new Date().toISOString().slice(0, 10);
  return tmdbList(
    "/discover/movie",
    {
      region: "US",
      with_release_type: "5",
      sort_by: "primary_release_date.desc",
      "release_date.lte": today,
      "vote_count.gte": "10",
    },
    count,
    page
  );
}

export function getMovieDetail(id: number) {
  return tmdbFetch<TmdbMovieDetail>(`/movie/${id}`, {
    append_to_response: "videos",
  });
}

// Prefers an official trailer named like "Official Trailer" over other
// trailers/teasers — TMDB movies often have dozens of promo videos (TV
// spots, featurettes, regional teasers), so picking well matters.
export function pickTrailerKey(videos: TmdbVideo[] | undefined): string | null {
  if (!videos) return null;
  const youtube = videos.filter((v) => v.site === "YouTube");
  const named = youtube.find(
    (v) => v.type === "Trailer" && v.official && /official trailer/i.test(v.name)
  );
  if (named) return named.key;
  const officialTrailer = youtube.find((v) => v.type === "Trailer" && v.official);
  if (officialTrailer) return officialTrailer.key;
  const anyTrailer = youtube.find((v) => v.type === "Trailer");
  if (anyTrailer) return anyTrailer.key;
  const teaser = youtube.find((v) => v.type === "Teaser");
  return teaser?.key ?? null;
}

export function posterUrl(path: string | null): string | null {
  return path ? `https://image.tmdb.org/t/p/w342${path}` : null;
}

export function backdropUrl(path: string | null): string | null {
  return path ? `https://image.tmdb.org/t/p/w780${path}` : null;
}
