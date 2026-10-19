import { apiFetch } from "@/lib/http";
import { useRef, useState } from "react";
import useSWR from "swr";
import { Link } from "react-router";
import { fetcher } from "@/lib/fetcher";
import type { ExploreHit, InstanceRef } from "@/lib/types";
import { TmdbDetailModal } from "@/components/TmdbDetailModal";
import { AddQualityModal } from "@/components/AddQualityModal";
import type { QualityProfile } from "@/lib/quality";
import { libraryStatus } from "@/lib/explore-library";

interface ExploreSectionProps {
  category: "theaters" | "bluray" | "popular";
  label: string;
  instanceId: string | null;
  /** Ready movie instances (the picker's choices) and the name of the chosen one. */
  instances?: InstanceRef[];
  instanceName?: string | null;
  qualityProfiles: QualityProfile[];
  rootFolderPath: string | null;
  monitored: boolean;
  searchNow: boolean;
  minimumAvailability: string;
}

const SCROLL_AMOUNT = 600;

export function ExploreSection({
  category,
  label,
  instanceId,
  instances = [],
  instanceName = null,
  qualityProfiles,
  rootFolderPath,
  monitored,
  searchNow,
  minimumAvailability,
}: ExploreSectionProps) {
  const { data, error } = useSWR<{ results?: ExploreHit[]; error?: string }>(
    `/api/explore?category=${category}`,
    fetcher
  );
  const scrollRef = useRef<HTMLDivElement>(null);
  const [pendingAdd, setPendingAdd] = useState<ExploreHit | null>(null);
  const [adding, setAdding] = useState(false);
  const [addedIds, setAddedIds] = useState<Set<number>>(new Set());
  const [addError, setAddError] = useState<string | null>(null);
  const [viewingId, setViewingId] = useState<number | null>(null);

  function scroll(direction: -1 | 1) {
    scrollRef.current?.scrollBy({
      left: direction * SCROLL_AMOUNT,
      behavior: "smooth",
    });
  }

  async function confirmAdd(qualityProfileId: number) {
    if (!pendingAdd || !rootFolderPath || !instanceId) return;
    setAdding(true);
    setAddError(null);
    try {
      const res = await apiFetch("/api/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          instanceId,
          id: pendingAdd.id,
          qualityProfileId,
          rootFolderPath,
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

  const results = data?.results ?? [];
  const viewingHit = results.find((r) => r.id === viewingId);

  return (
    <section>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-medium uppercase tracking-wide text-slate-500">{label}</h2>
        <Link
          to={`/explore/${category}`}
          className="text-xs font-medium text-slate-400 hover:text-slate-200"
        >
          All ›
        </Link>
      </div>
      {(error || data?.error) && (
        <p className="text-sm text-red-400">Failed to load: {data?.error ?? "request failed"}</p>
      )}
      {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}

      {results.length > 0 && (
        <div className="relative">
          <button
            onClick={() => scroll(-1)}
            aria-label="Scroll left"
            className="absolute left-0 top-1/2 z-10 -translate-y-1/2 -translate-x-3 flex h-9 w-9 items-center justify-center rounded-full border border-slate-700 bg-slate-900 text-lg text-slate-300 shadow-lg hover:bg-slate-800 hover:text-white"
          >
            ‹
          </button>
          <div
            ref={scrollRef}
            className="scrollbar-hide flex gap-3 overflow-x-auto scroll-smooth pb-2"
          >
            {results.map((hit) => {
              const lib = libraryStatus(
                hit.alreadyAdded,
                hit.libraryInstances,
                instanceId,
                instances
              );
              const added = lib.inTarget || addedIds.has(hit.id);
              return (
                <div key={hit.id} className="w-36 shrink-0">
                  <button onClick={() => setViewingId(hit.id)} className="block w-full text-left">
                    <div className="relative">
                      {hit.posterUrl ? (
                        <img
                          src={hit.posterUrl}
                          alt={hit.title}
                          className="h-52 w-36 rounded object-cover transition hover:opacity-80"
                        />
                      ) : (
                        <div className="flex h-52 w-36 items-center justify-center rounded bg-slate-800 text-xs text-slate-600">
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
                      {lib.inTarget ? lib.label : "Added ✓"}
                    </span>
                  ) : (
                    <>
                      <button
                        onClick={() => setPendingAdd(hit)}
                        disabled={qualityProfiles.length === 0}
                        className="mt-1 w-full rounded bg-emerald-700 px-1.5 py-1 text-xs font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
                      >
                        Add
                      </button>
                      {lib.elsewhere && (
                        <p className="mt-1 text-center text-xs text-slate-500">{lib.elsewhere}</p>
                      )}
                    </>
                  )}
                </div>
              );
            })}
          </div>
          <button
            onClick={() => scroll(1)}
            aria-label="Scroll right"
            className="absolute right-0 top-1/2 z-10 -translate-y-1/2 translate-x-3 flex h-9 w-9 items-center justify-center rounded-full border border-slate-700 bg-slate-900 text-lg text-slate-300 shadow-lg hover:bg-slate-800 hover:text-white"
          >
            ›
          </button>
        </div>
      )}

      {viewingHit && (
        <TmdbDetailModal
          id={viewingHit.id}
          fallbackTitle={viewingHit.title}
          instanceId={instanceId}
          instances={instances}
          qualityProfiles={qualityProfiles}
          rootFolderPath={rootFolderPath}
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
          qualityProfiles={qualityProfiles}
          target={instances.length > 1 ? instanceName : null}
          busy={adding}
          error={addError}
          onConfirm={confirmAdd}
          onCancel={() => {
            setPendingAdd(null);
            setAddError(null);
          }}
        />
      )}
    </section>
  );
}
