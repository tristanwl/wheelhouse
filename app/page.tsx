"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import {
  capDownloads,
  describeDownloadRow,
  downloadTone,
  sortDownloads,
  type RowTone,
} from "@/lib/download-rows";
import { StatusCard } from "@/components/StatusCard";
import { StorageBar } from "@/components/StorageBar";
import type { DownloadsResponse } from "@/app/api/downloads/route";
import { STATUS_ROLES, describeInstance, serviceHref, type StatusResponse } from "@/lib/status";
import type { StorageResponse } from "@/app/api/storage/route";
import { describeDownloadState } from "@/lib/providers/download-client/types";

// Fallback for the initial server-rendered pass — swapped for whatever
// hostname the browser actually used to reach Wheelhouse once mounted, so
// the links work the same way you got here (IP, mDNS name, Tailscale, etc.)
// rather than assuming any particular hostname resolves for every client.
const DEFAULT_HOST = "localhost";

const TONE_TEXT: Record<RowTone, string> = {
  bad: "text-red-400",
  warn: "text-amber-400",
  good: "text-emerald-400",
  neutral: "text-slate-400",
};
const TONE_BAR: Record<RowTone, string> = {
  bad: "bg-red-500",
  warn: "bg-amber-500",
  good: "bg-emerald-500",
  neutral: "bg-sky-500",
};

export default function DashboardPage() {
  const [host, setHost] = useState(DEFAULT_HOST);
  useEffect(() => {
    setHost(window.location.hostname);
  }, []);
  const { data: status, error: statusError } = useSWR<StatusResponse>("/api/status", fetcher, {
    refreshInterval: 15000,
  });
  const { data: storage } = useSWR<StorageResponse>("/api/storage", fetcher, {
    refreshInterval: 30000,
  });
  const { data: downloads } = useSWR<DownloadsResponse>("/api/downloads", fetcher, {
    refreshInterval: 5000,
  });

  const { shown: shownDownloads, hidden: hiddenDownloads } = capDownloads(
    sortDownloads(downloads?.items ?? [])
  );

  return (
    <div className="space-y-8">
      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">
          Service status
        </h2>
        {statusError && <p className="text-sm text-red-400">Failed to load status.</p>}
        {status && status.instances.length === 0 && (
          <p className="text-sm text-slate-500">
            No services are configured yet. See the{" "}
            <a
              href="https://github.com/tristanwl/wheelhouse#configuration"
              target="_blank"
              rel="noopener noreferrer"
              className="underline"
            >
              Configuration section of the README
            </a>
            .
          </p>
        )}
        {STATUS_ROLES.map(({ role, heading }) => {
          const group = status?.instances.filter((i) => i.role === role) ?? [];
          if (group.length === 0) return null;
          return (
            <div key={role} className="mb-4 last:mb-0">
              <h3 className="mb-2 text-xs font-medium text-slate-500">{heading}</h3>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                {group.map((instance) => {
                  const { lines, warnings } = describeInstance(instance);
                  const card = (
                    <StatusCard
                      name={instance.name}
                      state={instance.state}
                      lines={lines}
                      warnings={warnings}
                      error={instance.error}
                    />
                  );
                  return instance.link ? (
                    <a
                      key={instance.id}
                      href={serviceHref(host, instance.link)}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="block min-w-0 transition hover:opacity-80"
                    >
                      {card}
                    </a>
                  ) : (
                    <div key={instance.id} className="min-w-0">
                      {card}
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </section>

      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">Storage</h2>
        {storage && storage.instances.length === 0 && (
          <p className="text-sm text-slate-500">No Sonarr or Radarr instance configured.</p>
        )}
        {storage?.instances
          .filter((i) => !i.up)
          .map((i) => (
            <p key={i.instanceId} className="text-sm text-red-400">
              {i.name}: {i.error ?? "unreachable"}
            </p>
          ))}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {storage?.entries.map((entry) => (
            <Link
              key={`${entry.instanceId}:${entry.path}`}
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
            {shownDownloads.map((item) => {
              const tone = downloadTone(item.state);
              const speed = downloads.instances.find(
                (i) => i.instanceId === item.instanceId
              )?.speedBytesPerSec;
              const text = describeDownloadRow(item, speed);
              return (
                <div
                  key={`${item.instanceId}:${item.id}`}
                  className="rounded-lg border border-slate-800 bg-slate-900 p-3"
                >
                  <div className="mb-1 flex items-center justify-between gap-4 text-sm">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="shrink-0 rounded bg-slate-800 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-slate-400">
                        {item.protocol}
                      </span>
                      <span className="truncate">{item.name}</span>
                    </span>
                    <span className={`shrink-0 text-xs ${TONE_TEXT[tone]}`}>
                      {describeDownloadState(item)}
                    </span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
                    <div
                      className={`h-full ${TONE_BAR[tone]} ${item.state === "postprocessing" ? "animate-pulse" : ""}`}
                      style={{ width: `${Math.min(item.progress * 100, 100)}%` }}
                    />
                  </div>
                  <div className="mt-1 flex justify-between gap-4 text-xs text-slate-500">
                    <span>{text.left}</span>
                    <span>{text.right}</span>
                  </div>
                </div>
              );
            })}
            {hiddenDownloads > 0 && (
              <p className="text-xs text-slate-500">+{hiddenDownloads} more</p>
            )}
          </div>
        )}
      </section>
    </div>
  );
}
