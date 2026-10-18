import { NextResponse } from "next/server";
import { resolveManager } from "@/lib/manager-route";

interface AddRequestBody {
  instanceId: string;
  id: number; // tvdbId (series) or tmdbId (movies)
  qualityProfileId: number;
  rootFolderPath: string;
  monitored: boolean;
  searchNow: boolean;
  minimumAvailability?: string; // movies only
}

export async function POST(req: Request) {
  let body: AddRequestBody;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid request body" }, { status: 400 });
  }

  const { instanceId, id, qualityProfileId, rootFolderPath, monitored, searchNow } = body;

  if (typeof instanceId !== "string" || !instanceId) {
    return NextResponse.json({ error: "instanceId is required" }, { status: 400 });
  }
  if (!id || !qualityProfileId || !rootFolderPath) {
    return NextResponse.json(
      { error: "id, qualityProfileId, and rootFolderPath are required" },
      { status: 400 }
    );
  }
  const resolved = await resolveManager(instanceId);
  if ("response" in resolved) return resolved.response;
  const { client } = resolved;

  try {
    const base = await client.lookupByExternalId(id);
    if (!base) {
      return NextResponse.json(
        { error: client.kind === "series" ? "series not found" : "movie not found" },
        { status: 404 }
      );
    }
    const payload =
      client.kind === "series"
        ? {
            ...base,
            id: undefined,
            qualityProfileId,
            languageProfileId: 1,
            rootFolderPath,
            monitored,
            seasonFolder: true,
            monitorNewItems: "all",
            addOptions: { searchForMissingEpisodes: searchNow },
          }
        : {
            ...base,
            id: undefined,
            qualityProfileId,
            rootFolderPath,
            monitored,
            minimumAvailability: body.minimumAvailability ?? "released",
            addOptions: { searchForMovie: searchNow },
          };
    const created = await client.add(payload);
    return NextResponse.json({ ok: true, id: created.id, title: created.title });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "add failed" },
      { status: 502 }
    );
  }
}
