import { useState } from "react";
import { ModalCloseButton } from "./ModalCloseButton";
import { pickDefaultQualityProfileId, type QualityProfile } from "@/lib/quality";

interface AddQualityModalProps {
  title: string;
  posterUrl?: string | null;
  qualityProfiles: QualityProfile[];
  busy?: boolean;
  error?: string | null;
  /** Name of the instance being added to; shown only when the user has a choice. */
  target?: string | null;
  onConfirm: (qualityProfileId: number) => void;
  onCancel: () => void;
}

export function AddQualityModal({
  title,
  posterUrl,
  qualityProfiles,
  busy,
  error,
  target,
  onConfirm,
  onCancel,
}: AddQualityModalProps) {
  const [qualityProfileId, setQualityProfileId] = useState<number | null>(
    pickDefaultQualityProfileId(qualityProfiles)
  );

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={onCancel}
    >
      <div
        className="w-full max-w-sm rounded-lg border border-slate-800 bg-slate-900 p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-start justify-between gap-4">
          <div className="flex items-center gap-3">
            {posterUrl && (
              <img
                src={posterUrl}
                alt={title}
                className="h-16 w-11 shrink-0 rounded object-cover"
              />
            )}
            <h3 className="font-medium">{title}</h3>
          </div>
          <ModalCloseButton onClose={onCancel} className="-mr-1 -mt-1 shrink-0" />
        </div>

        {target && <p className="mb-3 text-xs text-slate-400">Adding to {target}</p>}
        <label className="mb-1 block text-xs text-slate-400">Quality</label>
        <select
          value={qualityProfileId ?? ""}
          onChange={(e) => setQualityProfileId(Number(e.target.value))}
          autoFocus
          className="mb-4 w-full rounded-md border border-slate-800 bg-slate-950 px-3 py-1.5 text-sm outline-hidden focus:border-slate-600"
        >
          {qualityProfiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>

        {error && <p className="mb-3 text-xs text-red-400">{error}</p>}

        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            disabled={busy}
            className="rounded-md px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          >
            Cancel
          </button>
          <button
            onClick={() => qualityProfileId && onConfirm(qualityProfileId)}
            disabled={busy || !qualityProfileId}
            className="rounded-md bg-emerald-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-600 disabled:opacity-60"
          >
            {busy ? "Adding…" : "Add"}
          </button>
        </div>
      </div>
    </div>
  );
}
