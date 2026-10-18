import { NextResponse } from "next/server";
import { getMediaManagers, noManagerMessage, redact } from "@/lib/providers";

export interface AddMeta {
  instanceId: string;
  instanceName: string;
  qualityProfiles: { id: number; name: string }[];
  rootFolderPath: string | null;
}

// Either ?instanceId=radarr-4k, or ?kind=movies for the first ready instance
// of that kind (what Explore uses, since it has no instance picker yet).
export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const instanceId = searchParams.get("instanceId");
  const kindParam = searchParams.get("kind");
  const kind = kindParam === "movies" || kindParam === "series" ? kindParam : undefined;

  if (!instanceId && !kind) {
    return NextResponse.json({ error: "instanceId or kind is required" }, { status: 400 });
  }

  const entries = await getMediaManagers(instanceId ? undefined : kind);
  const entry = instanceId
    ? entries.find((e) => e.instanceId === instanceId)
    : (entries.find((e) => e.client) ?? entries[0]);
  if (!entry) {
    return NextResponse.json(
      { error: instanceId ? `unknown instance "${instanceId}"` : noManagerMessage(kind!) },
      { status: 404 }
    );
  }
  if (!entry.client) {
    return NextResponse.json(
      { error: redact(`${entry.name}: ${entry.error ?? "not ready"}`) },
      { status: 502 }
    );
  }

  try {
    const [profiles, roots] = await Promise.all([
      entry.client.getQualityProfiles(),
      entry.client.getRootFolders(),
    ]);
    const meta: AddMeta = {
      instanceId: entry.instanceId,
      instanceName: entry.name,
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
