import { NextResponse } from "next/server";
import { addMovie, addSeries, lookupMovieByTmdbId, lookupSeriesByTvdbId } from "@/lib/arr-client";

interface AddRequestBody {
  service: "sonarr" | "radarr";
  id: number; // tvdbId (sonarr) or tmdbId (radarr)
  qualityProfileId: number;
  rootFolderPath: string;
  monitored: boolean;
  searchNow: boolean;
  minimumAvailability?: string; // radarr only
}

export async function POST(req: Request) {
  let body: AddRequestBody;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid request body" }, { status: 400 });
  }

  const { service, id, qualityProfileId, rootFolderPath, monitored, searchNow } = body;

  if (service !== "sonarr" && service !== "radarr") {
    return NextResponse.json({ error: "service must be sonarr or radarr" }, { status: 400 });
  }
  if (!id || !qualityProfileId || !rootFolderPath) {
    return NextResponse.json(
      { error: "id, qualityProfileId, and rootFolderPath are required" },
      { status: 400 }
    );
  }

  try {
    if (service === "sonarr") {
      const [base] = await lookupSeriesByTvdbId(id);
      if (!base) {
        return NextResponse.json({ error: "series not found" }, { status: 404 });
      }
      const payload = {
        ...base,
        id: undefined,
        qualityProfileId,
        languageProfileId: 1,
        rootFolderPath,
        monitored,
        seasonFolder: true,
        monitorNewItems: "all",
        addOptions: { searchForMissingEpisodes: searchNow },
      };
      const created = await addSeries(payload);
      return NextResponse.json({ ok: true, id: created.id, title: created.title });
    }

    const base = await lookupMovieByTmdbId(id);
    if (!base) {
      return NextResponse.json({ error: "movie not found" }, { status: 404 });
    }
    const payload = {
      ...base,
      id: undefined,
      qualityProfileId,
      rootFolderPath,
      monitored,
      minimumAvailability: body.minimumAvailability ?? "released",
      addOptions: { searchForMovie: searchNow },
    };
    const created = await addMovie(payload);
    return NextResponse.json({ ok: true, id: created.id, title: created.title });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "add failed" },
      { status: 502 }
    );
  }
}
