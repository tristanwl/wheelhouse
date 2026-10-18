import { NextResponse } from "next/server";
import { enqueueConversion, findActiveJobForPath, listJobs } from "@/lib/convert-queue";
import { linkCount } from "@/lib/hardlink";

export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ jobs: listJobs() });
}

export async function POST(req: Request) {
  const body = await req.json().catch(() => null);
  const validBackend =
    body?.backend === "vaapi" || body?.backend === "nvenc" || body?.backend === "software";
  const validFamily = body?.targetFamily === "h264" || body?.targetFamily === "hevc";
  if (
    !body ||
    typeof body.instanceId !== "string" ||
    typeof body.itemId !== "number" ||
    typeof body.filePath !== "string" ||
    typeof body.title !== "string" ||
    typeof body.originalSize !== "number" ||
    !validBackend ||
    !validFamily
  ) {
    return NextResponse.json({ error: "invalid request body" }, { status: 400 });
  }

  // A library file with other hard links may be the copy a torrent client is
  // seeding. Converting leaves that copy alone (so disk use grows), or breaks
  // the seed if the link was relied on: make the user say they know. A file
  // that already has a job just reattaches, so it isn't asked again.
  if (body.acknowledgeHardlink !== true && !findActiveJobForPath(body.filePath)) {
    const links = await linkCount(body.filePath);
    if (links !== null && links > 1) {
      return NextResponse.json(
        {
          error: `This file has ${links} hard links, so another copy of it (for example one a torrent client is seeding) may share its data.`,
          code: "hardlinked",
          linkCount: links,
        },
        { status: 409 }
      );
    }
  }

  const job = enqueueConversion({
    instanceId: body.instanceId,
    itemId: body.itemId,
    filePath: body.filePath,
    title: body.title,
    originalSize: body.originalSize,
    backend: body.backend,
    targetFamily: body.targetFamily,
  });
  return NextResponse.json(job);
}
