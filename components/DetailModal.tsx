"use client";

import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import { ModalCloseButton } from "./ModalCloseButton";
import type { ItemDetails } from "@/app/api/library/[service]/[id]/details/route";

interface DetailModalProps {
  service: "sonarr" | "radarr";
  id: number;
  fallbackTitle: string;
  onClose: () => void;
}

function formatRuntime(minutes: number | null): string | null {
  if (!minutes) return null;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

export function DetailModal({
  service,
  id,
  fallbackTitle,
  onClose,
}: DetailModalProps) {
  const { data, error } = useSWR<ItemDetails>(
    `/api/library/${service}/${id}/details`,
    fetcher
  );

  const runtime = data ? formatRuntime(data.runtime) : null;

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-4"
      onClick={onClose}
    >
      <div
        className="flex w-full max-w-2xl overflow-hidden rounded-lg border border-slate-800 bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="w-40 shrink-0 self-stretch bg-slate-800">
          {data?.posterUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={data.posterUrl}
              alt={data.title}
              className="h-full w-full object-cover"
            />
          ) : (
            <div className="flex h-full min-h-[220px] items-center justify-center px-2 text-center text-xs text-slate-600">
              {error ? "" : "No poster"}
            </div>
          )}
        </div>

        <div className="flex-1 p-5">
          <div className="mb-2 flex items-start justify-between gap-4">
            <h3 className="text-lg font-medium">{data?.title ?? fallbackTitle}</h3>
            <ModalCloseButton onClose={onClose} className="-mr-1 -mt-1" />
          </div>

          {error && <p className="text-sm text-red-400">Failed to load details.</p>}
          {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}

          {data && (
            <>
              <div className="mb-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-400">
                {data.year && <span>{data.year}</span>}
                {runtime && <span>· {runtime}</span>}
                {data.certification && (
                  <span className="rounded border border-slate-700 px-1.5 py-0.5">
                    {data.certification}
                  </span>
                )}
                {data.status && <span className="capitalize">· {data.status}</span>}
                {(data.network || data.studio) && (
                  <span>· {data.network ?? data.studio}</span>
                )}
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

              {data.ratings.length > 0 && (
                <div className="mb-3 flex flex-wrap gap-3 text-xs text-slate-300">
                  {data.ratings.map((r) => (
                    <span key={r.source}>
                      <span className="text-slate-500">{r.source}</span>{" "}
                      {r.value.toFixed(1)}
                      {r.votes ? ` (${r.votes.toLocaleString()})` : ""}
                    </span>
                  ))}
                </div>
              )}

              <p className="text-sm leading-relaxed text-slate-300">
                {data.overview || "No overview available."}
              </p>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
