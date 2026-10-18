import { NextResponse } from "next/server";
import { resolveManager } from "@/lib/manager-route";

export interface SeasonSummary {
  seasonNumber: number;
  monitored: boolean;
  episodeFileCount: number;
  episodeCount: number;
  totalEpisodeCount: number;
  sizeOnDisk: number;
}

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ instanceId: string; id: string }> }
) {
  const { instanceId, id: idParam } = await params;
  const resolved = await resolveManager(instanceId);
  if ("response" in resolved) return resolved.response;
  const { client } = resolved;
  if (client.kind !== "series") {
    return NextResponse.json({ error: "seasons are only available for series" }, { status: 400 });
  }
  const id = Number(idParam);
  if (!Number.isInteger(id)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    const series = await client.getSeriesById(id);
    const seasons: SeasonSummary[] = series.seasons
      .map((s) => ({
        seasonNumber: s.seasonNumber,
        monitored: s.monitored,
        episodeFileCount: s.statistics?.episodeFileCount ?? 0,
        episodeCount: s.statistics?.episodeCount ?? 0,
        totalEpisodeCount: s.statistics?.totalEpisodeCount ?? 0,
        sizeOnDisk: s.statistics?.sizeOnDisk ?? 0,
      }))
      .sort((a, b) => a.seasonNumber - b.seasonNumber);

    return NextResponse.json({ title: series.title, seasons });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "failed to load seasons" },
      { status: 502 }
    );
  }
}
