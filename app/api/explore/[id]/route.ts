import { NextResponse } from "next/server";
import { getMovies } from "@/lib/arr-client";
import { backdropUrl, getMovieDetail, pickTrailerKey, posterUrl } from "@/lib/tmdb-client";

export interface ExploreDetail {
  id: number;
  alreadyAdded: boolean;
  title: string;
  overview: string;
  year: number | null;
  genres: string[];
  runtime: number | null;
  voteAverage: number;
  posterUrl: string | null;
  backdropUrl: string | null;
  trailerKey: string | null;
}

export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id: idParam } = await params;
  const id = Number(idParam);
  if (!Number.isInteger(id)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    const [detail, libraryMovies] = await Promise.all([getMovieDetail(id), getMovies()]);
    const result: ExploreDetail = {
      id: detail.id,
      alreadyAdded: libraryMovies.some((m) => m.tmdbId === detail.id),
      title: detail.title,
      overview: detail.overview,
      year: detail.release_date ? Number(detail.release_date.slice(0, 4)) : null,
      genres: detail.genres.map((g) => g.name),
      runtime: detail.runtime,
      voteAverage: detail.vote_average,
      posterUrl: posterUrl(detail.poster_path),
      backdropUrl: backdropUrl(detail.backdrop_path),
      trailerKey: pickTrailerKey(detail.videos?.results),
    };
    return NextResponse.json(result);
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "failed to load details" },
      { status: 502 }
    );
  }
}
