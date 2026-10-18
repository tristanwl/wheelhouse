import { NextResponse } from "next/server";
import { getMediaManagers, redact, type MediaManager } from "@/lib/providers";

/** Looks up the media manager a route was addressed to, or the error response to return instead. */
export async function resolveManager(
  instanceId: string
): Promise<{ client: MediaManager; name: string } | { response: NextResponse }> {
  const entry = (await getMediaManagers()).find((e) => e.instanceId === instanceId);
  if (!entry) {
    return {
      response: NextResponse.json({ error: `unknown instance "${instanceId}"` }, { status: 404 }),
    };
  }
  if (!entry.client) {
    return {
      response: NextResponse.json(
        { error: redact(`${entry.name}: ${entry.error ?? "not ready"}`) },
        { status: 502 }
      ),
    };
  }
  return { client: entry.client, name: entry.name };
}
