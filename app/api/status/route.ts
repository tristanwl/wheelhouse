import { NextResponse } from "next/server";
import {
  getHealth,
  getIndexers,
  getQueueSummary,
  getSystemStatus,
  type HealthIssue,
} from "@/lib/arr-client";
import {
  getSabnzbdQueue,
  getSabnzbdServers,
  getSabnzbdWarnings,
} from "@/lib/sabnzbd-client";
import { env } from "@/lib/env";
import {
  getPlexIdentity,
  getPlexServerInfo,
  getPlexSessions,
} from "@/lib/plex-client";

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
        issues: health
          .filter((h) => h.source.startsWith("Indexer"))
          .map((h) => h.message),
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
    const [queue, servers, warnings] = await Promise.all([
      getSabnzbdQueue(),
      getSabnzbdServers(),
      getSabnzbdWarnings(),
    ]);
    return {
      up: true,
      paused: queue.paused,
      status: queue.status,
      kbpersec: Number(queue.kbpersec),
      queueCount: queue.noofslots_total,
      timeleft: queue.timeleft,
      servers: servers.map((s) => ({
        name: s.name,
        connections: s.connections,
        ssl: Boolean(s.ssl),
        enabled: Boolean(s.enable),
      })),
      // Excludes "Refused connection..." — that's the web UI's host
      // whitelist rejecting client access, unrelated to server/download
      // health, and would read as a false alarm on the dashboard.
      warnings: warnings
        .filter((w) => !w.text.startsWith("Refused connection"))
        .slice(-3)
        .map((w) => w.text),
    };
  } catch (err) {
    return { up: false, error: err instanceof Error ? err.message : "unknown error" };
  }
}

interface PlexStatus {
  up: boolean;
  configured: boolean;
  tokenConfigured: boolean;
  claimed?: boolean;
  version?: string;
  friendlyName?: string;
  activeSessions?: number;
  error?: string;
}

async function plexStatus(): Promise<PlexStatus> {
  const tokenConfigured = Boolean(env.plexToken);
  if (!env.plexUrl) {
    return {
      up: false,
      configured: false,
      tokenConfigured,
      error: "PLEX_URL not configured",
    };
  }

  try {
    const identity = await getPlexIdentity();
    const base: PlexStatus = {
      up: true,
      configured: true,
      tokenConfigured,
      claimed: identity.claimed,
      version: identity.version,
    };

    if (identity.claimed && tokenConfigured) {
      try {
        const [info, sessions] = await Promise.all([
          getPlexServerInfo(),
          getPlexSessions(),
        ]);
        return {
          ...base,
          friendlyName: info.friendlyName,
          activeSessions: sessions.size,
        };
      } catch {
        // Token present but invalid/expired — fall back to unauthenticated info.
        return base;
      }
    }

    return base;
  } catch (err) {
    return {
      up: false,
      configured: true,
      tokenConfigured,
      error: err instanceof Error ? err.message : "unknown error",
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
