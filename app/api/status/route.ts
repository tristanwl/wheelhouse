import { NextResponse } from "next/server";
import {
  getHealth,
  getIndexers,
  getQueueSummary,
  getSystemStatus,
  type HealthIssue,
} from "@/lib/arr-client";
import { getRuntime, redact, type DownloadClient, type MediaServer } from "@/lib/providers";
import { formatEta } from "@/lib/format";
import { env } from "@/lib/env";

export const dynamic = "force-dynamic";

interface IndexerSummary {
  total: number;
  enabled: number;
  issues: string[];
}

interface ArrStatus {
  up: boolean;
  version?: string;
  health: HealthIssue[];
  queueCount: number;
  indexers?: IndexerSummary;
  error?: string;
}

async function arrStatus(service: "sonarr" | "radarr"): Promise<ArrStatus> {
  try {
    const [status, health, queue, indexers] = await Promise.all([
      getSystemStatus(service),
      getHealth(service),
      getQueueSummary(service),
      getIndexers(service),
    ]);
    return {
      up: true,
      version: status.version,
      health,
      queueCount: queue.totalRecords,
      indexers: {
        total: indexers.length,
        enabled: indexers.filter((i) => i.enableRss).length,
        issues: health.filter((h) => h.source.startsWith("Indexer")).map((h) => h.message),
      },
    };
  } catch (err) {
    return {
      up: false,
      health: [],
      queueCount: 0,
      error: err instanceof Error ? err.message : "unknown error",
    };
  }
}

interface SabServerStatus {
  name: string;
  connections: number;
  ssl: boolean;
  enabled: boolean;
}

interface SabStatus {
  up: boolean;
  paused?: boolean;
  status?: string;
  kbpersec?: number;
  queueCount?: number;
  timeleft?: string;
  servers?: SabServerStatus[];
  warnings?: string[];
  error?: string;
}

async function sabStatus(): Promise<SabStatus> {
  try {
    const client = await getRuntime().getClient<DownloadClient>("sabnzbd");
    const status = await client.getStatus();
    return {
      up: true,
      paused: status.paused,
      status: status.statusText,
      kbpersec: status.speedBytesPerSec / 1024,
      queueCount: status.queueCount,
      timeleft: status.etaSeconds === null ? undefined : formatEta(status.etaSeconds),
      servers: status.usenet?.servers,
      warnings: status.warnings.map((w) => w.message),
    };
  } catch (err) {
    return { up: false, error: err instanceof Error ? redact(err.message) : "unknown error" };
  }
}

interface PlexStatus {
  up: boolean;
  configured: boolean;
  /** Whether Wheelhouse can read session data (a working token). */
  authenticated?: boolean;
  /** The server itself isn't ready (an unclaimed Plex). */
  needsSetup?: boolean;
  /** Something for the user to know or do, in plain words. */
  notice?: string;
  version?: string;
  friendlyName?: string;
  activeSessions?: number;
  error?: string;
}

async function plexStatus(): Promise<PlexStatus> {
  const runtime = getRuntime();
  const resolved = (await runtime.listInstances()).find((r) => r.instance.id === "plex");
  if (!resolved) return { up: false, configured: false };
  if (resolved.status !== "ready") {
    return { up: false, configured: true, error: resolved.error ?? resolved.status };
  }
  try {
    const status = await (await runtime.getClient<MediaServer>("plex")).getStatus();
    return {
      up: true,
      configured: true,
      authenticated: status.authenticated,
      needsSetup: status.state === "needs-setup",
      notice: status.notice,
      version: status.version,
      friendlyName: status.name,
      activeSessions: status.activeSessions,
    };
  } catch (err) {
    return {
      up: false,
      configured: true,
      error: err instanceof Error ? redact(err.message) : "unknown error",
    };
  }
}

export async function GET() {
  const [sonarr, radarr, sabnzbd, plex] = await Promise.all([
    arrStatus("sonarr"),
    arrStatus("radarr"),
    sabStatus(),
    plexStatus(),
  ]);

  return NextResponse.json({ sonarr, radarr, sabnzbd, plex });
}
