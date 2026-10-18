import { NextResponse } from "next/server";
import {
  getRuntime,
  redact,
  type DownloadClient,
  type MediaManager,
  type MediaServer,
  type ResolvedInstance,
} from "@/lib/providers";
import {
  STATUS_ROLES,
  hasRecentError,
  serviceLink,
  type InstanceState,
  type StatusInstance,
  type StatusResponse,
} from "@/lib/status";

export const dynamic = "force-dynamic";

// Where a provider's own web UI lives, under the configured URL.
const WEB_UI_PATH: Record<string, string> = { plex: "/web" };

const roleOrder = (role: string) => STATUS_ROLES.findIndex((r) => r.role === role);

/** One instance's entry. Never throws: a failure becomes a "down" entry with the reason. */
async function statusOf(resolved: ResolvedInstance): Promise<StatusInstance | undefined> {
  const { instance, definition } = resolved;
  const role = definition?.role;
  if (role !== "mediaManager" && role !== "downloadClient" && role !== "mediaServer") {
    return undefined;
  }
  const base = {
    id: instance.id,
    providerId: instance.providerId,
    name: instance.name,
    role,
  };
  const link = serviceLink(
    (instance.config as { url?: unknown } | null)?.url,
    WEB_UI_PATH[instance.providerId]
  );
  const down = (error: string): StatusInstance => ({
    ...base,
    state: "down",
    error,
    ...(link ? { link } : {}),
  });

  if (resolved.status !== "ready") return down(resolved.error ?? resolved.status);

  const withLink = { ...(link ? { link } : {}) };
  try {
    const runtime = getRuntime();
    if (role === "mediaManager") {
      const detail = await (await runtime.getClient<MediaManager>(instance.id)).getStatus();
      const state: InstanceState = detail.health.some(
        (h) => h.type === "warning" || h.type === "error"
      )
        ? "degraded"
        : "ok";
      return { ...base, role, state, version: detail.version, detail, ...withLink };
    }
    if (role === "downloadClient") {
      const detail = await (await runtime.getClient<DownloadClient>(instance.id)).getStatus();
      const state: InstanceState = hasRecentError(detail.warnings) ? "degraded" : "ok";
      return { ...base, role, state, version: detail.version, detail, ...withLink };
    }
    const detail = await (await runtime.getClient<MediaServer>(instance.id)).getStatus();
    const state: InstanceState = detail.state === "needs-setup" ? "needs-setup" : "ok";
    return { ...base, role, state, version: detail.version, detail, ...withLink };
  } catch (err) {
    return down(err instanceof Error ? redact(err.message) : "unknown error");
  }
}

export async function GET() {
  const resolved = (await getRuntime().listInstances()).filter((r) => r.status !== "disabled");
  const described = await Promise.all(resolved.map(statusOf));
  const instances = described
    .filter((i): i is StatusInstance => i !== undefined)
    // Array.sort is stable, so configuration order holds within a role.
    .sort((a, b) => roleOrder(a.role) - roleOrder(b.role));
  return NextResponse.json({ instances } satisfies StatusResponse);
}
