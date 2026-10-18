import { NextResponse } from "next/server";
import { cancelJob, getJob } from "@/lib/convert-queue";

export const dynamic = "force-dynamic";

export async function POST(
  _req: Request,
  { params }: { params: { jobId: string } }
) {
  const ok = cancelJob(params.jobId);
  if (!ok) {
    return NextResponse.json(
      { error: "job not found or not cancelable" },
      { status: 400 }
    );
  }
  return NextResponse.json(getJob(params.jobId));
}
