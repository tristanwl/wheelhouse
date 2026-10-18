"use client";

import { Suspense, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import useSWR, { mutate as globalMutate } from "swr";
import { fetcher } from "@/lib/fetcher";
import { formatAge, formatBytes } from "@/lib/format";
import { ConfirmModal } from "@/components/ConfirmModal";
import { SeriesModal } from "@/components/SeriesModal";
import { DetailModal } from "@/components/DetailModal";
import type { LibraryItem, LibraryResponse } from "@/app/api/library/route";

type SortKey = "title" | "size" | "age";
type SortDir = "asc" | "desc";

const DEFAULT_SORT_DIR: Record<SortKey, SortDir> = {
  title: "asc",
  size: "desc",
  age: "desc",
};

function volumeLabel(path: string): string {
  if (path === "/tv") return "TV";
  if (path === "/movies") return "Movies";
  return path;
}

function LibraryContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const volumePath = searchParams.get("path") ?? "";

  const { data, error, mutate } = useSWR<LibraryResponse>("/api/library", fetcher);
  const [query, setQuery] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("size");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [pending, setPending] = useState<LibraryItem | null>(null);
  const [managingSeries, setManagingSeries] = useState<LibraryItem | null>(null);
  const [viewingDetails, setViewingDetails] = useState<LibraryItem | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const volumes = useMemo(() => {
    const paths = new Set((data?.items ?? []).map((i) => i.rootFolderPath));
    return Array.from(paths).sort();
  }, [data]);

  const items = useMemo(() => {
    let all = data?.items ?? [];
    if (volumePath) {
      all = all.filter((item) => item.rootFolderPath === volumePath);
    }
    if (query.trim()) {
      const q = query.toLowerCase();
      all = all.filter((item) => item.title.toLowerCase().includes(q));
    }

    const sorted = [...all].sort((a, b) => {
      let cmp = 0;
      if (sortKey === "title") cmp = a.title.localeCompare(b.title);
      else if (sortKey === "size") cmp = a.sizeOnDisk - b.sizeOnDisk;
      else cmp = new Date(a.added).getTime() - new Date(b.added).getTime();
      return sortDir === "asc" ? cmp : -cmp;
    });
    return sorted;
  }, [data, query, volumePath, sortKey, sortDir]);

  function toggleSort(key: SortKey) {
    if (key === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir(DEFAULT_SORT_DIR[key]);
    }
  }

  function sortIndicator(key: SortKey) {
    if (key !== sortKey) return null;
    return <span className="text-slate-500">{sortDir === "asc" ? " ▲" : " ▼"}</span>;
  }

  function setVolume(path: string) {
    if (path) router.push(`/library?path=${encodeURIComponent(path)}`);
    else router.push("/library");
  }

  function refreshEverything() {
    mutate();
    globalMutate("/api/storage");
  }

  async function confirmDelete() {
    if (!pending) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/library/${pending.instanceId}/${pending.id}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error ?? `delete failed: ${res.status}`);
      }
      await mutate(
        (current) =>
          current && {
            ...current,
            items: current.items.filter(
              (i) => !(i.instanceId === pending.instanceId && i.id === pending.id)
            ),
          },
        { revalidate: true }
      );
      globalMutate("/api/storage");
      setPending(null);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : "delete failed");
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <h2 className="text-sm font-medium uppercase tracking-wide text-slate-500">
            Library ({items.length})
          </h2>
          <select
            value={volumePath}
            onChange={(e) => setVolume(e.target.value)}
            className="rounded-md border border-slate-800 bg-slate-900 px-2 py-1.5 text-sm outline-none focus:border-slate-600"
          >
            <option value="">All volumes</option>
            {volumes.map((v) => (
              <option key={v} value={v}>
                {volumeLabel(v)}
              </option>
            ))}
          </select>
        </div>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search titles…"
          className="w-64 rounded-md border border-slate-800 bg-slate-900 px-3 py-1.5 text-sm outline-none focus:border-slate-600"
        />
      </div>

      {error && <p className="text-sm text-red-400">Failed to load library.</p>}
      {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}
      {data?.instances.length === 0 && (
        <p className="text-sm text-slate-500">
          No Sonarr or Radarr instance is configured. Set SONARR_URL / SONARR_API_KEY or RADARR_URL
          / RADARR_API_KEY.
        </p>
      )}
      {data?.instances
        .filter((i) => !i.up)
        .map((i) => (
          <p key={i.instanceId} className="text-sm text-red-400">
            {i.name}: {i.error ?? "unreachable"}
          </p>
        ))}

      {items.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-slate-800">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="bg-slate-900 text-left text-xs uppercase tracking-wide text-slate-500">
              <tr>
                <th
                  className="cursor-pointer select-none px-4 py-2 hover:text-slate-300"
                  onClick={() => toggleSort("title")}
                >
                  Title{sortIndicator("title")}
                </th>
                <th className="px-4 py-2">Instance</th>
                <th
                  className="cursor-pointer select-none px-4 py-2 hover:text-slate-300"
                  onClick={() => toggleSort("size")}
                >
                  Size on disk{sortIndicator("size")}
                </th>
                <th
                  className="cursor-pointer select-none px-4 py-2 hover:text-slate-300"
                  onClick={() => toggleSort("age")}
                >
                  Age{sortIndicator("age")}
                </th>
                <th className="px-4 py-2">Monitored</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr
                  key={`${item.instanceId}-${item.id}`}
                  className="border-t border-slate-800 hover:bg-slate-900/60"
                >
                  <td className="px-4 py-2">
                    <button
                      onClick={() => setViewingDetails(item)}
                      className="text-left hover:text-sky-400 hover:underline"
                    >
                      {item.title}
                    </button>
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={`rounded px-1.5 py-0.5 text-xs ${
                        item.kind === "series"
                          ? "bg-sky-900 text-sky-300"
                          : "bg-violet-900 text-violet-300"
                      }`}
                    >
                      {item.instanceName}
                    </span>
                  </td>
                  <td className="px-4 py-2">{formatBytes(item.sizeOnDisk)}</td>
                  <td className="px-4 py-2 text-slate-400" title={item.added}>
                    {formatAge(item.added)}
                  </td>
                  <td className="px-4 py-2 text-slate-400">{item.monitored ? "yes" : "no"}</td>
                  <td className="px-4 py-2 text-right">
                    {item.kind === "series" ? (
                      <button
                        onClick={() => setManagingSeries(item)}
                        className="rounded-md px-2 py-1 text-xs font-medium text-red-400 hover:bg-red-950"
                      >
                        Manage
                      </button>
                    ) : (
                      <button
                        onClick={() => {
                          setDeleteError(null);
                          setPending(item);
                        }}
                        className="rounded-md px-2 py-1 text-xs font-medium text-red-400 hover:bg-red-950"
                      >
                        Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {pending && (
        <ConfirmModal
          title={`Delete "${pending.title}"?`}
          body={`This removes it from ${pending.instanceName} and deletes ${formatBytes(
            pending.sizeOnDisk
          )} of files from disk. This can't be undone.${deleteError ? ` (${deleteError})` : ""}`}
          busy={deleting}
          onConfirm={confirmDelete}
          onCancel={() => setPending(null)}
        />
      )}

      {managingSeries && (
        <SeriesModal
          instanceId={managingSeries.instanceId}
          instanceName={managingSeries.instanceName}
          seriesId={managingSeries.id}
          title={managingSeries.title}
          onClose={() => setManagingSeries(null)}
          onDeletedSeries={() => {
            mutate(
              (current) =>
                current && {
                  ...current,
                  items: current.items.filter(
                    (i) =>
                      !(i.instanceId === managingSeries.instanceId && i.id === managingSeries.id)
                  ),
                },
              { revalidate: true }
            );
            globalMutate("/api/storage");
            setManagingSeries(null);
          }}
          onSeasonChanged={refreshEverything}
        />
      )}

      {viewingDetails && (
        <DetailModal
          instanceId={viewingDetails.instanceId}
          id={viewingDetails.id}
          fallbackTitle={viewingDetails.title}
          onClose={() => setViewingDetails(null)}
        />
      )}
    </div>
  );
}

export default function LibraryPage() {
  return (
    <Suspense fallback={<p className="text-sm text-slate-500">Loading…</p>}>
      <LibraryContent />
    </Suspense>
  );
}
