import { NextResponse } from "next/server";
import { detectEncoders } from "@/lib/hw-encode";

export const dynamic = "force-dynamic";

export async function GET() {
  const encoders = await detectEncoders();
  return NextResponse.json({ encoders });
}
