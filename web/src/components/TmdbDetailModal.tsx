import { apiFetch } from "@/lib/http";
import { useState } from "react";
import useSWR from "swr";
import { youtubeWatchUrl } from "@/lib/api";
import { fetcher } from "@/lib/fetcher";
import { ModalCloseButton } from "./ModalCloseButton";
import { AddQualityModal } from "./AddQualityModal";
import type { QualityProfile } from "@/lib/quality";
import type { ExploreDetail } from "@/lib/types";

interface TmdbDetailModalProps {
  id: number;
  fallbackTitle: string;
  instanceId: string | null;
  qualityProfiles: QualityProfile[];
  rootFolderPath: string | null;
  monitored: boolean;
  searchNow: boolean;
  minimumAvailability: string;
  onClose: () => void;
  onAdded: () => void;
}

function formatRuntime(minutes: number | null): string | null {
  if (!minutes) return null;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

export function TmdbDetailModal({
  id,
  fallbackTitle,
  instanceId,
  qualityProfiles,
  rootFolderPath,
  monitored,
  searchNow,
  minimumAvailability,
  onClose,
  onAdded,
}: TmdbDetailModalProps) {
  const { data, error } = useSWR<ExploreDetail>(`/api/explore/${id}`, fetcher);
  const [showQualityPicker, setShowQualityPicker] = useState(false);
  const [adding, setAdding] = useState(false);
  const [added, setAdded] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);

  const trailerUrl = data ? youtubeWatchUrl(data.trailerKey) : null;
  const runtime = data ? formatRuntime(data.runtime) : null;
  const isAdded = data?.alreadyAdded || added;

  async function confirmAdd(qualityProfileId: number) {
    if (!rootFolderPath || !instanceId) return;
    setAdding(true);
    setAddError(null);
    try {
      const res = await apiFetch("/api/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          instanceId,
          id,
          qualityProfileId,
          rootFolderPath,
          monitored,
          searchNow,
          minimumAvailability,
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error ?? `add failed: ${res.status}`);
      setAdded(true);
      setShowQualityPicker(false);
      onAdded();
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "Add failed.");
    } finally {
      setAdding(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-2xl overflow-hidden rounded-lg border border-slate-800 bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        {data?.backdropUrl && (
          <div
            className="h-40 w-full bg-slate-800 bg-cover bg-center"
            style={{
              backgroundImage: `linear-gradient(to top, rgb(15 23 42), transparent 60%), url(${data.backdropUrl})`,
            }}
          />
        )}

        <div className="flex gap-4 p-5">
          <div className="w-32 shrink-0">
            {data?.posterUrl ? (
              <img src={data.posterUrl} alt={data.title} className="w-full rounded object-cover" />
            ) : (
              <div className="flex h-48 w-32 items-center justify-center rounded bg-slate-800 text-xs text-slate-600">
                No poster
              </div>
            )}
          </div>

          <div className="min-w-0 flex-1">
            <div className="mb-2 flex items-start justify-between gap-4">
              <h3 className="text-lg font-medium">{data?.title ?? fallbackTitle}</h3>
              <ModalCloseButton onClose={onClose} className="-mr-1 -mt-1" />
            </div>

            {error && <p className="text-sm text-red-400">Failed to load details.</p>}
            {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}

            {data && (
              <>
                <div className="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-400">
                  {data.year && <span>{data.year}</span>}
                  {runtime && <span>· {runtime}</span>}
                  <span>· ★ {data.voteAverage.toFixed(1)}</span>
                </div>

                {data.genres.length > 0 && (
                  <div className="mb-3 flex flex-wrap gap-1.5">
                    {data.genres.map((g) => (
                      <span
                        key={g}
                        className="rounded-full bg-slate-800 px-2 py-0.5 text-xs text-slate-300"
                      >
                        {g}
                      </span>
                    ))}
                  </div>
                )}

                <div className="mb-3 flex flex-wrap gap-2">
                  {trailerUrl && (
                    <a
                      href={trailerUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="rounded-md bg-red-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-600"
                    >
                      ▶ Watch Trailer
                    </a>
                  )}
                  {isAdded ? (
                    <span className="rounded-md bg-emerald-900 px-3 py-1.5 text-xs text-emerald-300">
                      {data.alreadyAdded ? "In library" : "Added ✓"}
                    </span>
                  ) : (
                    <button
                      onClick={() => setShowQualityPicker(true)}
                      disabled={qualityProfiles.length === 0}
                      className="rounded-md bg-emerald-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
                    >
                      Add to library
                    </button>
                  )}
                </div>

                <p className="text-sm leading-relaxed text-slate-300">
                  {data.overview || "No overview available."}
                </p>
              </>
            )}
          </div>
        </div>
      </div>

      {showQualityPicker && data && (
        <AddQualityModal
          title={data.title}
          posterUrl={data.posterUrl}
          qualityProfiles={qualityProfiles}
          busy={adding}
          error={addError}
          onConfirm={confirmAdd}
          onCancel={() => {
            setShowQualityPicker(false);
            setAddError(null);
          }}
        />
      )}
    </div>
  );
}
