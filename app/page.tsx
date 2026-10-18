"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import { formatBytes, formatEta } from "@/lib/format";
import { StatusCard } from "@/components/StatusCard";
import { StorageBar } from "@/components/StorageBar";
import type { DownloadsResponse } from "@/app/api/downloads/route";
import { describeDownloadState } from "@/lib/providers/download-client/types";

interface IndexerSummary {
  total: number;
  enabled: number;
  issues: string[];
}

interface ArrStatus {
  up: boolean;
  version?: string;
  health: { type: string; message: string }[];
  queueCount: number;
  indexers?: IndexerSummary;
  error?: string;
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

interface StatusResponse {
  sonarr: ArrStatus;
  radarr: ArrStatus;
  sabnzbd: SabStatus;
  plex: PlexStatus;
}

// Fallback for the initial server-rendered pass — swapped for whatever
// hostname the browser actually used to reach Wheelhouse once mounted, so
// the links work the same way you got here (IP, mDNS name, Tailscale, etc.)
// rather than assuming any particular hostname resolves for every client.
const DEFAULT_HOST = "localhost";

interface StorageResponse {
  entries: {
    label: string;
    path: string;
    freeBytes: number;
    libraryUsedBytes: number;
  }[];
}

export default function DashboardPage() {
  const [host, setHost] = useState(DEFAULT_HOST);
  useEffect(() => {
    setHost(window.location.hostname);
  }, []);
  const SERVICE_URLS = useMemo(
    () => ({
      sonarr: `http://${host}:8989`,
      radarr: `http://${host}:7878`,
      sabnzbd: `http://${host}:8080`,
      plex: `http://${host}:32400/web`,
    }),
    [host]
  );

  const { data: status, error: statusError } = useSWR<StatusResponse>("/api/status", fetcher, {
    refreshInterval: 15000,
  });
  const { data: storage } = useSWR<StorageResponse>("/api/storage", fetcher, {
    refreshInterval: 30000,
  });
  const { data: downloads } = useSWR<DownloadsResponse>("/api/downloads", fetcher, {
    refreshInterval: 5000,
  });

  return (
    <div className="space-y-8">
      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">
          Service status
        </h2>
        {statusError && <p className="text-sm text-red-400">Failed to load status.</p>}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <a
            href={SERVICE_URLS.sonarr}
            target="_blank"
            rel="noopener noreferrer"
            className="block transition hover:opacity-80"
          >
            <StatusCard
              name="Sonarr"
              up={status?.sonarr.up ?? false}
              lines={[
                `queue: ${status?.sonarr.queueCount ?? "-"}`,
                status?.sonarr.indexers
                  ? `indexers: ${status.sonarr.indexers.enabled}/${status.sonarr.indexers.total}`
                  : "",
                status?.sonarr.version ? `v${status.sonarr.version}` : "",
              ].filter(Boolean)}
              warnings={status?.sonarr.health?.filter((h) => h.type !== "ok").map((h) => h.message)}
              error={status?.sonarr.error}
            />
          </a>
          <a
            href={SERVICE_URLS.radarr}
            target="_blank"
            rel="noopener noreferrer"
            className="block transition hover:opacity-80"
          >
            <StatusCard
              name="Radarr"
              up={status?.radarr.up ?? false}
              lines={[
                `queue: ${status?.radarr.queueCount ?? "-"}`,
                status?.radarr.indexers
                  ? `indexers: ${status.radarr.indexers.enabled}/${status.radarr.indexers.total}`
                  : "",
                status?.radarr.version ? `v${status.radarr.version}` : "",
              ].filter(Boolean)}
              warnings={status?.radarr.health?.filter((h) => h.type !== "ok").map((h) => h.message)}
              error={status?.radarr.error}
            />
          </a>
          <a
            href={SERVICE_URLS.sabnzbd}
            target="_blank"
            rel="noopener noreferrer"
            className="block transition hover:opacity-80"
          >
            <StatusCard
              name="SABnzbd"
              up={status?.sabnzbd.up ?? false}
              lines={[
                `status: ${status?.sabnzbd.status ?? "-"}`,
                status?.sabnzbd.paused ? "paused" : "",
                status?.sabnzbd.kbpersec ? `${formatBytes(status.sabnzbd.kbpersec * 1024)}/s` : "",
                status?.sabnzbd.queueCount ? `${status.sabnzbd.queueCount} in queue` : "",
                status?.sabnzbd.timeleft && status.sabnzbd.timeleft !== "0:00:00"
                  ? `${status.sabnzbd.timeleft} left`
                  : "",
                ...(status?.sabnzbd.servers ?? []).map(
                  (s) =>
                    `${s.enabled ? "server" : "server disabled"}: ${s.name} (${s.connections} conn${s.ssl ? ", ssl" : ""})`
                ),
              ].filter(Boolean)}
              warnings={status?.sabnzbd.warnings}
              error={status?.sabnzbd.error}
            />
          </a>
          <a
            href={SERVICE_URLS.plex}
            target="_blank"
            rel="noopener noreferrer"
            className="block transition hover:opacity-80"
          >
            <StatusCard
              name="Plex"
              up={status?.plex.up ?? false}
              lines={[
                status?.plex.friendlyName ?? "",
                status?.plex.version ? `v${status.plex.version}` : "",
                status?.plex.tokenConfigured
                  ? `${status.plex.activeSessions ?? 0} active session${
                      status.plex.activeSessions === 1 ? "" : "s"
                    }`
                  : "",
              ].filter(Boolean)}
              warnings={[
                status?.plex.up && !status.plex.claimed
                  ? "Not claimed yet - finish setup at :32400/web"
                  : "",
                status?.plex.up && status.plex.claimed && !status.plex.tokenConfigured
                  ? "PLEX_TOKEN not set - showing basic status only"
                  : "",
              ].filter(Boolean)}
              error={
                status?.plex.configured === false ? "PLEX_URL not configured" : status?.plex.error
              }
            />
          </a>
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">Storage</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {storage?.entries.map((entry) => (
            <Link
              key={entry.path}
              href={`/library?path=${encodeURIComponent(entry.path)}`}
              className="block transition hover:opacity-80"
            >
              <StorageBar {...entry} />
            </Link>
          ))}
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">
          Downloads
        </h2>
        {downloads && downloads.instances.length === 0 && (
          <p className="text-sm text-slate-500">No download client configured.</p>
        )}
        {downloads?.instances
          .filter((i) => !i.up)
          .map((i) => (
            <p key={i.instanceId} className="text-sm text-red-400">
              {i.name}: {i.error ?? "unreachable"}
            </p>
          ))}
        {downloads?.instances.some((i) => i.up) && downloads.items.length === 0 && (
          <p className="text-sm text-slate-500">No active downloads.</p>
        )}
        {downloads && downloads.items.length > 0 && (
          <div className="space-y-2">
            {downloads.instances
              .filter((i) => i.paused)
              .map((i) => (
                <p key={i.instanceId} className="text-xs font-medium text-amber-400">
                  {downloads.instances.length > 1 ? `${i.name}: queue paused` : "Queue paused"}
                </p>
              ))}
            {downloads.items.map((item) => {
              const processing = item.state === "postprocessing";
              const speed = downloads.instances.find(
                (i) => i.instanceId === item.instanceId
              )?.speedBytesPerSec;
              return (
                <div
                  key={`${item.instanceId}:${item.id}`}
                  className="rounded-lg border border-slate-800 bg-slate-900 p-3"
                >
                  <div className="mb-1 flex items-center justify-between gap-4 text-sm">
                    <span className="truncate">{item.name}</span>
                    <span
                      className={`shrink-0 text-xs ${processing ? "text-amber-400" : "text-slate-400"}`}
                    >
                      {describeDownloadState(item)}
                    </span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
                    <div
                      className={`h-full ${processing ? "animate-pulse bg-amber-500" : "bg-sky-500"}`}
                      style={{ width: `${Math.min(item.progress * 100, 100)}%` }}
                    />
                  </div>
                  <div className="mt-1 flex justify-between text-xs text-slate-500">
                    <span>
                      {processing
                        ? formatBytes(item.sizeBytes)
                        : `${Math.round(item.progress * 100)}% of ${formatBytes(item.sizeBytes)}`}
                    </span>
                    <span>
                      {!processing && speed ? `${formatBytes(speed)}/s · ` : ""}
                      {item.etaSeconds ? `${formatEta(item.etaSeconds)} left` : ""}
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
