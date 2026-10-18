import { NextResponse } from "next/server";
import { enqueueConversion, listJobs } from "@/lib/convert-queue";

export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ jobs: listJobs() });
}

export async function POST(req: Request) {
  const body = await req.json().catch(() => null);
  const validBackend = body?.backend === "vaapi" || body?.backend === "nvenc" || body?.backend === "software";
  const validFamily = body?.targetFamily === "h264" || body?.targetFamily === "hevc";
  if (
    !body ||
    (body.service !== "radarr" && body.service !== "sonarr") ||
    typeof body.itemId !== "number" ||
    typeof body.filePath !== "string" ||
    typeof body.title !== "string" ||
    typeof body.originalSize !== "number" ||
    !validBackend ||
    !validFamily
  ) {
    return NextResponse.json({ error: "invalid request body" }, { status: 400 });
  }

  const job = enqueueConversion({
    service: body.service,
    itemId: body.itemId,
    filePath: body.filePath,
    title: body.title,
    originalSize: body.originalSize,
    backend: body.backend,
    targetFamily: body.targetFamily,
  });
  return NextResponse.json(job);
}
