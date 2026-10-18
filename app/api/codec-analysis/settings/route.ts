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
  try {
    await setCodecPreferences({ hd: body.hd, uhd: body.uhd });
  } catch (err) {
    // Almost always a /config volume the container user can't write to.
    const detail = err instanceof Error ? err.message : "unknown error";
    return NextResponse.json(
      {
        error: `Could not save settings (${detail}). Make sure the /config volume exists on the host and is writable by the container's user.`,
      },
      { status: 500 }
    );
  }
  return NextResponse.json(await getCodecPreferences());
}
