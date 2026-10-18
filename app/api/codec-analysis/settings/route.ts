import { NextResponse } from "next/server";
import { getCodecPreferences, setCodecPreferences } from "@/lib/settings";

export const dynamic = "force-dynamic";

function isValidFamily(v: unknown): v is "h264" | "hevc" {
  return v === "h264" || v === "hevc";
}

export async function GET() {
  return NextResponse.json(await getCodecPreferences());
}

export async function PUT(req: Request) {
  const body = await req.json().catch(() => null);
  if (!body || !isValidFamily(body.hd) || !isValidFamily(body.uhd)) {
    return NextResponse.json(
      { error: "hd and uhd must each be 'h264' or 'hevc'" },
      { status: 400 }
    );
  }
  await setCodecPreferences({ hd: body.hd, uhd: body.uhd });
  return NextResponse.json(await getCodecPreferences());
}
