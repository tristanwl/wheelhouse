import { NextResponse } from "next/server";
import { getSabnzbdActivePostProc, getSabnzbdQueue } from "@/lib/sabnzbd-client";

export const dynamic = "force-dynamic";

export interface DownloadItem {
  id: string;
  filename: string;
  status: string;
  postProcessing: boolean;
  percentage: number;
  sizeMb: number;
  sizeLeftMb: number;
  timeleft: string;
}

export async function GET() {
  try {
    const [queue, activePostProc] = await Promise.all([
      getSabnzbdQueue(),
      getSabnzbdActivePostProc(),
    ]);
    const items: DownloadItem[] = queue.slots.map((s) => {
      const realStatus = activePostProc.get(s.nzo_id);
      return {
        id: s.nzo_id,
        filename: s.filename,
        status: realStatus ?? s.status,
        postProcessing: realStatus !== undefined,
        percentage: Number(s.percentage) || 0,
        sizeMb: Number(s.mb) || 0,
        sizeLeftMb: Number(s.mbleft) || 0,
        timeleft: s.timeleft,
      };
    });
    return NextResponse.json({
      up: true,
      paused: queue.paused,
      kbpersec: Number(queue.kbpersec) || 0,
      items,
    });
  } catch (err) {
    return NextResponse.json({
      up: false,
      error: err instanceof Error ? err.message : "unknown error",
      items: [],
    });
  }
}
