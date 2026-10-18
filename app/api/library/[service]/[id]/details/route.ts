import { NextResponse } from "next/server";
import { getMovieDetail, getSeriesDetail } from "@/lib/arr-client";

export interface ItemRating {
  source: string;
  value: number;
  votes: number;
}

export interface ItemDetails {
  title: string;
  overview: string | null;
  year: number | null;
  genres: string[];
  runtime: number | null;
  certification: string | null;
  status: string | null;
  network: string | null;
  studio: string | null;
  posterUrl: string | null;
  fanartUrl: string | null;
  ratings: ItemRating[];
}

function pickImage(images: { coverType: string; remoteUrl?: string }[] | undefined, type: string) {
  return images?.find((i) => i.coverType === type)?.remoteUrl ?? null;
}

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ service: string; id: string }> }
) {
  const { service, id: idParam } = await params;
  const id = Number(idParam);
  if (!Number.isInteger(id)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    if (service === "sonarr") {
      const s = await getSeriesDetail(id);
      const details: ItemDetails = {
        title: s.title,
        overview: s.overview ?? null,
        year: s.year ?? null,
        genres: s.genres ?? [],
        runtime: s.runtime ?? null,
        certification: s.certification ?? null,
        status: s.status ?? null,
        network: s.network ?? null,
        studio: null,
        posterUrl: pickImage(s.images, "poster"),
        fanartUrl: pickImage(s.images, "fanart"),
        ratings: s.ratings?.value
          ? [{ source: "TVDB", value: s.ratings.value, votes: s.ratings.votes }]
          : [],
      };
      return NextResponse.json(details);
    }

    if (service === "radarr") {
      const m = await getMovieDetail(id);
      const ratings: ItemRating[] = Object.entries(m.ratings ?? {})
        .filter(([, r]) => r && typeof r.value === "number")
        .map(([source, r]) => ({ source, value: r.value, votes: r.votes }));
      const details: ItemDetails = {
        title: m.title,
        overview: m.overview ?? null,
        year: m.year ?? null,
        genres: m.genres ?? [],
        runtime: m.runtime ?? null,
        certification: m.certification ?? null,
        status: m.status ?? null,
        network: null,
        studio: m.studio ?? null,
        posterUrl: pickImage(m.images, "poster"),
        fanartUrl: pickImage(m.images, "fanart"),
        ratings,
      };
      return NextResponse.json(details);
    }

    return NextResponse.json({ error: "unknown service" }, { status: 400 });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "failed to load details" },
      { status: 502 }
    );
  }
}
