import { NextResponse } from "next/server";
import { getQualityProfiles, getRootFolders } from "@/lib/arr-client";

export interface AddMeta {
  qualityProfiles: { id: number; name: string }[];
  rootFolderPath: string | null;
}

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const service = searchParams.get("service");

  if (service !== "sonarr" && service !== "radarr") {
    return NextResponse.json({ error: "service must be sonarr or radarr" }, { status: 400 });
  }

  try {
    const [profiles, roots] = await Promise.all([
      getQualityProfiles(service),
      getRootFolders(service),
    ]);
    const meta: AddMeta = {
      qualityProfiles: profiles.map((p) => ({ id: p.id, name: p.name })),
      rootFolderPath: roots[0]?.path ?? null,
    };
    return NextResponse.json(meta);
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "failed to load add options" },
      { status: 502 }
    );
  }
}
