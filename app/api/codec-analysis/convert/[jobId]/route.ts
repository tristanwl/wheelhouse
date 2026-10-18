import { NextResponse } from "next/server";
import { getJob } from "@/lib/convert-queue";

export const dynamic = "force-dynamic";

export async function GET(_req: Request, { params }: { params: { jobId: string } }) {
  const job = getJob(params.jobId);
  if (!job) {
    return NextResponse.json({ error: "job not found" }, { status: 404 });
  }
  return NextResponse.json(job);
}
