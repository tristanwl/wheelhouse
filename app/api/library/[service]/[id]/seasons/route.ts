import { NextResponse } from "next/server";
import { getSeriesById } from "@/lib/arr-client";

export interface SeasonSummary {
  seasonNumber: number;
  monitored: boolean;
  episodeFileCount: number;
  episodeCount: number;
  totalEpisodeCount: number;
  sizeOnDisk: number;
}

export async function GET(_req: Request, { params }: { params: { service: string; id: string } }) {
  if (params.service !== "sonarr") {
    return NextResponse.json({ error: "seasons are only available for sonarr" }, { status: 400 });
  }
  const id = Number(params.id);
  if (!Number.isInteger(id)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    const series = await getSeriesById(id);
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
