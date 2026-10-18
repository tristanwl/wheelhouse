import { NextResponse } from "next/server";
import { deleteMovie, deleteSeries } from "@/lib/arr-client";

export async function DELETE(
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
      await deleteSeries(id);
    } else if (service === "radarr") {
      await deleteMovie(id);
    } else {
      return NextResponse.json({ error: "unknown service" }, { status: 400 });
    }
    return NextResponse.json({ ok: true });
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "delete failed" },
      { status: 502 }
    );
  }
}
