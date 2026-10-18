import { NextResponse } from "next/server";
import {
  collectFromManagers,
  getMediaManagers,
  noManagerMessage,
  type LookupResult,
  type ManagerKind,
  type ManagerProblem,
} from "@/lib/providers";

export interface SearchHit {
  instanceId: string;
  instanceName: string;
  id: number; // tvdbId (series) or tmdbId (movies): stable external id, used to add
  alreadyAdded: boolean;
  title: string;
  year: number;
  overview: string;
  posterUrl: string | null;
  subtitle: string | null; // network (series) or studio (movies)
}

export interface SearchResponse {
  results: SearchHit[];
  /** Instances that couldn't be searched; the others' results are still returned. */
  problems?: ManagerProblem[];
}

// Per instance, so a second instance doesn't crowd out the first.
const MAX_RESULTS = 8;

function toHit(
  r: LookupResult,
  kind: ManagerKind,
  instance: { instanceId: string; instanceName: string }
): SearchHit | null {
  const externalId = kind === "series" ? r.tvdbId : r.tmdbId;
  if (!externalId) return null;
  return {
    ...instance,
    id: externalId,
    // Sonarr/Radarr omit `id` entirely (not `id: null`) for lookup results
    // that aren't in the library yet, so `r.id` is `undefined` there: a
    // strict `!== null` check misses that and marks everything as added.
    alreadyAdded: typeof r.id === "number",
    title: r.title,
    year: r.year,
    overview: r.overview ?? "",
    posterUrl: r.images?.find((i) => i.coverType === "poster")?.remoteUrl ?? null,
    subtitle: (kind === "series" ? r.network : r.studio) ?? null,
  };
}

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const kind = searchParams.get("kind");
  const term = searchParams.get("term")?.trim();

  if (kind !== "movies" && kind !== "series") {
    return NextResponse.json({ error: "kind must be movies or series" }, { status: 400 });
  }
  if (!term) {
    return NextResponse.json({ results: [] });
  }

  const entries = await getMediaManagers(kind);
  if (entries.length === 0) {
    return NextResponse.json({ error: noManagerMessage(kind) }, { status: 404 });
  }

  const { results, problems } = await collectFromManagers(entries, async (client, entry) => {
    const raw = await client.lookup(term);
    return raw
      .map((r) => toHit(r, kind, { instanceId: entry.instanceId, instanceName: entry.name }))
      .filter((h): h is SearchHit => h !== null)
      .slice(0, MAX_RESULTS);
  });

  if (results.length === 0 && problems.length > 0) {
    return NextResponse.json({ error: problems[0].error, problems }, { status: 502 });
  }
  return NextResponse.json({
    results: results.flatMap((r) => r.value),
    ...(problems.length > 0 ? { problems } : {}),
  } satisfies SearchResponse);
}
