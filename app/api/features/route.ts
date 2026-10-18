import { NextResponse } from "next/server";
import { getFeatures } from "@/lib/features/server";

export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json(await getFeatures());
}
