import { NextResponse } from "next/server";
import { resolveManager } from "@/lib/manager-route";

export async function DELETE(
  _req: Request,
  { params }: { params: Promise<{ instanceId: string; id: string; season: string }> }
) {
  const { instanceId, id: idParam, season } = await params;
  const resolved = await resolveManager(instanceId);
  if ("response" in resolved) return resolved.response;
  const { client } = resolved;
  if (client.kind !== "series") {
    return NextResponse.json({ error: "seasons are only available for series" }, { status: 400 });
  }
  const id = Number(idParam);
  const seasonNumber = Number(season);
  if (!Number.isInteger(id) || !Number.isInteger(seasonNumber)) {
    return NextResponse.json({ error: "invalid id" }, { status: 400 });
  }

  try {
    const files = await client.getEpisodeFiles(id);
    const toDelete = files.filter((f) => f.seasonNumber === seasonNumber);
    await Promise.all(toDelete.map((f) => client.deleteEpisodeFile(f.id)));

    const series = await client.getSeriesById(id);
    series.seasons = series.seasons.map((s) =>
      s.seasonNumber === seasonNumber ? { ...s, monitored: false } : s
    );
    await client.updateSeries(series);

    return NextResponse.json({ ok: true, filesDeleted: toDelete.length });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "delete failed" },
      { status: 502 }
    );
  }
}
