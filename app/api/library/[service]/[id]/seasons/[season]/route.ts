import { NextResponse } from "next/server";
import { deleteEpisodeFile, getEpisodeFiles, getSeriesById, updateSeries } from "@/lib/arr-client";

export async function DELETE(
  _req: Request,
  { params }: { params: { service: string; id: string; season: string } }
) {
  if (params.service !== "sonarr") {
    return NextResponse.json({ error: "seasons are only available for sonarr" }, { status: 400 });
  }
  const id = Number(params.id);
  const seasonNumber = Number(params.season);
  if (!Number.isInteger(id) || !Number.isInteger(seasonNumber)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    const files = await getEpisodeFiles(id);
    const toDelete = files.filter((f) => f.seasonNumber === seasonNumber);
    await Promise.all(toDelete.map((f) => deleteEpisodeFile(f.id)));

    const series = await getSeriesById(id);
    series.seasons = series.seasons.map((s) =>
      s.seasonNumber === seasonNumber ? { ...s, monitored: false } : s
    );
    await updateSeries(series);

    return NextResponse.json({ ok: true, filesDeleted: toDelete.length });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "delete failed" },
      { status: 502 }
    );
  }
}
