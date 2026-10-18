import { useState } from "react";
import useSWR from "swr";
import { confirmedDeleteUrl } from "@/lib/api";
import { fetcher } from "@/lib/fetcher";
import { formatBytes } from "@/lib/format";
import { ConfirmModal } from "./ConfirmModal";
import { ModalCloseButton } from "./ModalCloseButton";
import type { SeasonSummary } from "@/lib/types";

interface SeriesModalProps {
  instanceId: string;
  instanceName: string;
  seriesId: number;
  title: string;
  onClose: () => void;
  onDeletedSeries: () => void;
  onSeasonChanged: () => void;
}

interface SeasonsResponse {
  title: string;
  seasons: SeasonSummary[];
}

export function SeriesModal({
  instanceId,
  instanceName,
  seriesId,
  title,
  onClose,
  onDeletedSeries,
  onSeasonChanged,
}: SeriesModalProps) {
  const { data, error, mutate } = useSWR<SeasonsResponse>(
    `/api/library/${instanceId}/${seriesId}/seasons`,
    fetcher
  );

  const [pendingSeason, setPendingSeason] = useState<SeasonSummary | null>(null);
  const [confirmingWholeSeries, setConfirmingWholeSeries] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const totalSize = data?.seasons.reduce((sum, s) => sum + s.sizeOnDisk, 0) ?? 0;

  async function deleteWholeSeries() {
    setBusy(true);
    setActionError(null);
    try {
      const res = await fetch(confirmedDeleteUrl(instanceId, seriesId), { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error ?? `delete failed: ${res.status}`);
      }
      onDeletedSeries();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "delete failed");
      setBusy(false);
    }
  }

  async function deleteSeason() {
    if (!pendingSeason) return;
    setBusy(true);
    setActionError(null);
    try {
      const res = await fetch(
        confirmedDeleteUrl(instanceId, seriesId, pendingSeason.seasonNumber),
        {
          method: "DELETE",
        }
      );
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error ?? `delete failed: ${res.status}`);
      }
      await mutate();
      onSeasonChanged();
      setPendingSeason(null);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "delete failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-lg rounded-lg border border-slate-800 bg-slate-900 p-5">
        <div className="mb-4 flex items-start justify-between gap-4">
          <div>
            <h3 className="font-medium">{title}</h3>
            {data && (
              <p className="text-xs text-slate-500">{formatBytes(totalSize)} total on disk</p>
            )}
          </div>
          <ModalCloseButton onClose={onClose} className="-mr-1 -mt-1" />
        </div>

        {error && <p className="text-sm text-red-400">Failed to load seasons.</p>}
        {!data && !error && <p className="text-sm text-slate-500">Loading…</p>}

        {data && (
          <div className="max-h-80 space-y-1 overflow-y-auto">
            {data.seasons.map((season) => (
              <div
                key={season.seasonNumber}
                className="flex items-center justify-between rounded-md px-2 py-1.5 hover:bg-slate-800/60"
              >
                <div>
                  <p className="text-sm">
                    {season.seasonNumber === 0 ? "Specials" : `Season ${season.seasonNumber}`}
                  </p>
                  <p className="text-xs text-slate-500">
                    {season.episodeFileCount}/{season.totalEpisodeCount} episodes ·{" "}
                    {formatBytes(season.sizeOnDisk)}
                    {!season.monitored ? " · unmonitored" : ""}
                  </p>
                </div>
                <button
                  disabled={season.episodeFileCount === 0}
                  onClick={() => {
                    setActionError(null);
                    setPendingSeason(season);
                  }}
                  className="rounded-md px-2 py-1 text-xs font-medium text-red-400 hover:bg-red-950 disabled:cursor-not-allowed disabled:text-slate-600 disabled:hover:bg-transparent"
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        )}

        <div className="mt-4 border-t border-slate-800 pt-4">
          <button
            onClick={() => {
              setActionError(null);
              setConfirmingWholeSeries(true);
            }}
            className="text-sm font-medium text-red-400 hover:text-red-300"
          >
            Delete entire series…
          </button>
        </div>
      </div>

      {pendingSeason && (
        <ConfirmModal
          title={`Delete Season ${pendingSeason.seasonNumber}?`}
          body={`This deletes ${formatBytes(
            pendingSeason.sizeOnDisk
          )} of files for this season and stops monitoring it. This can't be undone.${
            actionError ? ` (${actionError})` : ""
          }`}
          busy={busy}
          onConfirm={deleteSeason}
          onCancel={() => setPendingSeason(null)}
        />
      )}

      {confirmingWholeSeries && (
        <ConfirmModal
          title={`Delete "${title}"?`}
          body={`This removes the whole series from ${instanceName} and deletes ${formatBytes(
            totalSize
          )} of files from disk. This can't be undone.${actionError ? ` (${actionError})` : ""}`}
          busy={busy}
          onConfirm={deleteWholeSeries}
          onCancel={() => setConfirmingWholeSeries(false)}
        />
      )}
    </div>
  );
}
