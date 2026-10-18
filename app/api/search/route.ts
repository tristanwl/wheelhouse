import { NextResponse } from "next/server";
import { lookupMovies, lookupSeries, type LookupResult } from "@/lib/arr-client";

export interface SearchHit {
  id: number; // tvdbId (sonarr) or tmdbId (radarr) — stable external id, used to add
  alreadyAdded: boolean;
  title: string;
  year: number;
  overview: string;
  posterUrl: string | null;
  subtitle: string | null; // network (sonarr) or studio (radarr)
}

const MAX_RESULTS = 8;

function toHit(r: LookupResult, service: "sonarr" | "radarr"): SearchHit | null {
  const externalId = service === "sonarr" ? r.tvdbId : r.tmdbId;
  if (!externalId) return null;
  return {
    id: externalId,
    // Sonarr/Radarr omit `id` entirely (not `id: null`) for lookup results
    // that aren't in the library yet, so `r.id` is `undefined` there — a
    // strict `!== null` check misses that and marks everything as added.
    alreadyAdded: typeof r.id === "number",
    title: r.title,
    year: r.year,
    overview: r.overview ?? "",
    posterUrl: r.images?.find((i) => i.coverType === "poster")?.remoteUrl ?? null,
    subtitle: (service === "sonarr" ? r.network : r.studio) ?? null,
  };
}

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const service = searchParams.get("service");
  const term = searchParams.get("term")?.trim();

  if (service !== "sonarr" && service !== "radarr") {
    return NextResponse.json({ error: "service must be sonarr or radarr" }, { status: 400 });
  }
  if (!term) {
    return NextResponse.json({ results: [] });
  }

  try {
    const raw = await (service === "sonarr" ? lookupSeries(term) : lookupMovies(term));
    const results = raw
      .map((r) => toHit(r, service))
      .filter((h): h is SearchHit => h !== null)
      .slice(0, MAX_RESULTS);
    return NextResponse.json({ results });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "search failed" },
      { status: 502 }
    );
  }
}
