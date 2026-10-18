import { NextResponse } from "next/server";
import { collectFromManagers, getMediaManagers } from "@/lib/providers";
import {
  getNowPlaying,
  getPopular,
  getRecentPhysicalReleases,
  posterUrl,
  type TmdbMovie,
} from "@/lib/tmdb-client";

export interface ExploreHit {
  id: number; // tmdbId
  alreadyAdded: boolean;
  title: string;
  year: number | null;
  overview: string;
  posterUrl: string | null;
  voteAverage: number;
}

const CATEGORY_FETCHERS = {
  theaters: getNowPlaying,
  bluray: getRecentPhysicalReleases,
  popular: getPopular,
} as const;

type Category = keyof typeof CATEGORY_FETCHERS;

function toHit(m: TmdbMovie, addedIds: Set<number>): ExploreHit {
  return {
    id: m.id,
    alreadyAdded: addedIds.has(m.id),
    title: m.title,
    year: m.release_date ? Number(m.release_date.slice(0, 4)) : null,
    overview: m.overview,
    posterUrl: posterUrl(m.poster_path),
    voteAverage: m.vote_average,
  };
}

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const category = searchParams.get("category") as Category | null;

  if (!category || !(category in CATEGORY_FETCHERS)) {
    return NextResponse.json(
      { error: "category must be one of: theaters, bluray, popular" },
      { status: 400 }
    );
  }

  const limitParam = Number(searchParams.get("limit") ?? "20");
  const limit = Number.isFinite(limitParam) ? Math.min(Math.max(limitParam, 1), 60) : 20;
  const pageParam = Number(searchParams.get("page") ?? "1");
  const page = Number.isInteger(pageParam) && pageParam >= 1 ? pageParam : 1;

  try {
    // "In library" means in any movie instance. One that can't be reached just
    // doesn't contribute, rather than taking the whole page down.
    const [{ results: movies, hasMore }, library] = await Promise.all([
      CATEGORY_FETCHERS[category](limit, page),
      getMediaManagers("movies").then((entries) =>
        collectFromManagers(entries, (client) => client.getMovies())
      ),
    ]);
    const addedIds = new Set(library.results.flatMap((r) => r.value.map((m) => m.tmdbId)));
    return NextResponse.json({ results: movies.map((m) => toHit(m, addedIds)), hasMore });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "explore failed" },
      { status: 502 }
    );
  }
}
