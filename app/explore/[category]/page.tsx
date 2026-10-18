"use client";

import { useEffect, useState } from "react";
import useSWRInfinite from "swr/infinite";
import Link from "next/link";
import { fetcher } from "@/lib/fetcher";
import type { AddMeta } from "@/app/api/add-meta/route";
import type { ExploreHit } from "@/app/api/explore/route";
import { TmdbDetailModal } from "@/components/TmdbDetailModal";
import { AddQualityModal } from "@/components/AddQualityModal";
import { TmdbAttribution } from "@/components/TmdbAttribution";

const CATEGORY_LABELS: Record<string, string> = {
  theaters: "In Theaters",
  bluray: "Recent Blu-ray / Digital Releases",
  popular: "Popular",
};

const MIN_AVAILABILITY_OPTIONS = [
  { value: "announced", label: "Announced" },
  { value: "inCinemas", label: "In cinemas" },
  { value: "released", label: "Released" },
];

// Per "Load more" batch — two TMDB pages.
const LIMIT = 40;

type ExplorePage = { results?: ExploreHit[]; hasMore?: boolean; error?: string };

export default function ExploreCategoryPage({ params }: { params: { category: string } }) {
  const { category } = params;
  const label = CATEGORY_LABELS[category];

  const { data, error, size, setSize, isValidating } = useSWRInfinite<ExplorePage>(
    (index, prev: ExplorePage | null) => {
      if (!label || (prev && !prev.hasMore)) return null;
      return `/api/explore?category=${category}&limit=${LIMIT}&page=${index + 1}`;
    },
    fetcher,
    // Without this, every Load more re-fetches page 1 as well.
    { revalidateFirstPage: false }
  );

  const [meta, setMeta] = useState<AddMeta | null>(null);
  const [monitored, setMonitored] = useState(true);
  const [searchNow, setSearchNow] = useState(true);
  const [minimumAvailability, setMinimumAvailability] = useState("released");

  const [pendingAdd, setPendingAdd] = useState<ExploreHit | null>(null);
  const [adding, setAdding] = useState(false);
  const [addedIds, setAddedIds] = useState<Set<number>>(new Set());
  const [addError, setAddError] = useState<string | null>(null);
  const [viewingId, setViewingId] = useState<number | null>(null);

  useEffect(() => {
    fetcher<AddMeta>("/api/add-meta?service=radarr")
      .then(setMeta)
      .catch(() => {});
  }, []);

  if (!label) {
    return (
      <div className="space-y-4">
        <Link href="/explore" className="text-sm text-slate-500 hover:text-slate-300">
          ‹ Explore
        </Link>
        <p className="text-sm text-red-400">Unknown category &quot;{category}&quot;.</p>
      </div>
    );
  }

  async function confirmAdd(qualityProfileId: number) {
    if (!pendingAdd || !meta?.rootFolderPath) return;
    setAdding(true);
    setAddError(null);
    try {
      const res = await fetch("/api/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          service: "radarr",
          id: pendingAdd.id,
          qualityProfileId,
          rootFolderPath: meta.rootFolderPath,
          monitored,
          searchNow,
          minimumAvailability,
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error ?? `add failed: ${res.status}`);
      setAddedIds((prev) => new Set(prev).add(pendingAdd.id));
      setPendingAdd(null);
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "Add failed.");
    } finally {
      setAdding(false);
    }
  }

  // TMDB lists shift between page requests (popularity re-ranks), so the
  // same movie can show up in two batches — drop repeats to keep keys unique.
  const seen = new Set<number>();
  const results = (data ?? [])
    .flatMap((p) => p.results ?? [])
    .filter((r) => !seen.has(r.id) && seen.add(r.id));
  const lastPage = data?.[data.length - 1];
  const pageError = data?.find((p) => p.error)?.error;
  const loadingMore = isValidating && size > (data?.length ?? 0);
  const hasMore = !!lastPage?.hasMore && !pageError;
  const viewingHit = results.find((r) => r.id === viewingId);

  return (
    <div className="space-y-6">
      <div>
        <Link href="/explore" className="text-sm text-slate-500 hover:text-slate-300">
          ‹ Explore
        </Link>
        <h1 className="mt-1 text-lg font-medium">{label}</h1>
      </div>

      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-slate-800 bg-slate-900 p-3">
        <select
          value={minimumAvailability}
          onChange={(e) => setMinimumAvailability(e.target.value)}
          className="rounded-md border border-slate-800 bg-slate-950 px-2 py-1.5 text-sm outline-none focus:border-slate-600"
        >
          {MIN_AVAILABILITY_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1.5 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={monitored}
            onChange={(e) => setMonitored(e.target.checked)}
          />
          Monitored
        </label>
        <label className="flex items-center gap-1.5 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={searchNow}
            onChange={(e) => setSearchNow(e.target.checked)}
          />
          Search now
        </label>
      </div>

      {(error || pageError) && (
        <p className="text-sm text-red-400">Failed to load: {pageError ?? "request failed"}</p>
      )}
      {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
        {results.map((hit) => {
          const added = hit.alreadyAdded || addedIds.has(hit.id);
          return (
            <div key={hit.id}>
              <button onClick={() => setViewingId(hit.id)} className="block w-full text-left">
                <div className="relative">
                  {hit.posterUrl ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      src={hit.posterUrl}
                      alt={hit.title}
                      className="aspect-[2/3] w-full rounded object-cover transition hover:opacity-80"
                    />
                  ) : (
                    <div className="flex aspect-[2/3] w-full items-center justify-center rounded bg-slate-800 text-xs text-slate-600">
                      No poster
                    </div>
                  )}
                  <div className="absolute bottom-1 right-1 rounded bg-black/70 px-1.5 py-0.5 text-xs text-amber-300">
                    ★ {hit.voteAverage.toFixed(1)}
                  </div>
                </div>
                <p className="mt-1 truncate text-xs font-medium" title={hit.title}>
                  {hit.title}
                </p>
                <p className="text-xs text-slate-500">{hit.year ?? "-"}</p>
              </button>
              {added ? (
                <span className="mt-1 block rounded bg-emerald-900 px-1.5 py-1 text-center text-xs text-emerald-300">
                  {hit.alreadyAdded ? "In library" : "Added ✓"}
                </span>
              ) : (
                <button
                  onClick={() => setPendingAdd(hit)}
                  disabled={!meta}
                  className="mt-1 w-full rounded bg-emerald-700 px-1.5 py-1 text-xs font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
                >
                  Add
                </button>
              )}
            </div>
          );
        })}
      </div>

      {hasMore && (
        <div className="flex justify-center">
          <button
            onClick={() => setSize(size + 1)}
            disabled={loadingMore}
            className="rounded-md border border-slate-700 bg-slate-900 px-4 py-2 text-sm text-slate-300 hover:bg-slate-800 disabled:opacity-50"
          >
            {loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>
      )}

      <TmdbAttribution />

      {viewingHit && (
        <TmdbDetailModal
          id={viewingHit.id}
          fallbackTitle={viewingHit.title}
          qualityProfiles={meta?.qualityProfiles ?? []}
          rootFolderPath={meta?.rootFolderPath ?? null}
          monitored={monitored}
          searchNow={searchNow}
          minimumAvailability={minimumAvailability}
          onClose={() => setViewingId(null)}
          onAdded={() => setAddedIds((prev) => new Set(prev).add(viewingHit.id))}
        />
      )}

      {pendingAdd && (
        <AddQualityModal
          title={pendingAdd.title}
          posterUrl={pendingAdd.posterUrl}
          qualityProfiles={meta?.qualityProfiles ?? []}
          busy={adding}
          error={addError}
          onConfirm={confirmAdd}
          onCancel={() => {
            setPendingAdd(null);
            setAddError(null);
          }}
        />
      )}
    </div>
  );
}
