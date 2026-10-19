import { Link } from "react-router";
import { useSession } from "@/lib/session-context";
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
import type { DownloadsResponse } from "@/lib/types";
import { STATUS_ROLES, describeInstance, serviceHref, type StatusResponse } from "@/lib/status";
import type { StorageResponse } from "@/lib/types";
import { describeDownloadState } from "@/lib/download-rows";

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

/**
 * The first-run checklist, for administrators only: until at least one service
 * exists (stored or from the environment) it points at the Services page. It
 * disappears by itself when the first service is added.
 */
function SetupChecklist() {
  const { isAdmin } = useSession();
  const { data } = useSWR<{ instances: unknown[] }>(
    isAdmin ? "/api/admin/instances" : null,
    fetcher
  );
  if (!isAdmin || !data || data.instances.length > 0) return null;
  return (
    <aside
      aria-label="Getting started"
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-sky-900 bg-sky-950/40 p-4 text-sm"
    >
      <div className="space-y-1">
        <p>
          <strong className="font-medium">Add your services.</strong> Wheelhouse is set up but does
          not know about Radarr, Sonarr, your download client or your media server yet.
        </p>
        <p className="text-slate-400">
          Add one and the dashboard fills in. You can add more any time, and invite other people
          under Users.
        </p>
      </div>
      <Link
        to="/admin/services/new"
        className="rounded-md bg-sky-700 px-3 py-1.5 font-medium text-white hover:bg-sky-600"
      >
        Add your services
      </Link>
    </aside>
  );
}

export default function DashboardPage() {
  const { isAdmin } = useSession();
  // Service links use whatever hostname the browser reached Wheelhouse by (IP, mDNS
  // name, Tailscale, ...), so they work the same way you got here.
  const host = window.location.hostname;
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
      <h1 className="sr-only">Dashboard</h1>
      <SetupChecklist />
      <section>
        <h2 className="mb-3 text-sm font-medium uppercase tracking-wide text-slate-500">
          Service status
        </h2>
        {statusError && <p className="text-sm text-red-400">Failed to load status.</p>}
        {status && status.instances.length === 0 && (
          <p className="text-sm text-slate-500">
            No services are configured yet.{" "}
            {isAdmin ? (
              <>
                Add them under{" "}
                <Link to="/admin/services" className="underline">
                  Admin, Services
                </Link>
                .
              </>
            ) : (
              "Ask an administrator to add them."
            )}
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
              to={`/library?path=${encodeURIComponent(entry.path)}`}
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
