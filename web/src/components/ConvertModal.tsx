import { useEffect, useRef, useState } from "react";
import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import { formatBytes } from "@/lib/format";
import { ModalCloseButton } from "./ModalCloseButton";
import type { ConversionJob } from "@/lib/types";
import type { Backend, EncoderOption } from "@/lib/types";
import type { PreferableFamily } from "@/lib/types";

interface ConvertModalProps {
  instanceId: string;
  itemId: number;
  filePath: string;
  title: string;
  codec: string;
  originalSize: number;
  // Determined by the analysis (the file's resolution tier vs. your codec
  // preferences), not freely chosen here — this modal only lets you pick
  // *how* to encode to it (software vs. whatever hardware is available).
  targetFamily: PreferableFamily;
  // If set, the modal reattaches to an already-running/queued job instead
  // of showing the confirm screen — used when reopening via the row's
  // progress indicator rather than a fresh "Convert" click.
  existingJobId?: string;
  onClose: () => void;
  onConverted: () => void;
}

const DONE_STATUSES = new Set(["success", "failed", "canceled"]);
const FAMILY_LABEL: Record<PreferableFamily, string> = { h264: "H.264", hevc: "HEVC" };

function statusLine(job: ConversionJob): string {
  switch (job.status) {
    case "queued":
      return "Queued…";
    case "probing":
      return "Reading file…";
    case "running":
      return `Converting… ${job.progressPercent}%`;
    case "success":
      return "Done";
    case "failed":
      return "Failed";
    case "canceled":
      return "Stopped";
  }
}

function backendLabel(backend: Backend): string {
  if (backend === "vaapi") return "VAAPI (hardware)";
  if (backend === "nvenc") return "NVENC (hardware)";
  return "Software";
}

export function ConvertModal({
  instanceId,
  itemId,
  filePath,
  title,
  codec,
  originalSize,
  targetFamily,
  existingJobId,
  onClose,
  onConverted,
}: ConvertModalProps) {
  const [jobId, setJobId] = useState<string | null>(existingJobId ?? null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  // The server found other hard links to this file; waiting for the user to confirm.
  const [hardlinked, setHardlinked] = useState(false);
  const [backend, setBackend] = useState<Backend>("software");
  const [backendTouched, setBackendTouched] = useState(false);

  const { data: encodersData } = useSWR<{ encoders: EncoderOption[] }>(
    jobId ? null : "/api/codec-analysis/encoders",
    fetcher
  );
  // Only options that can actually produce this file's target codec —
  // software for that family is always offered by the API; hardware
  // options are whatever real ffmpeg test encodes succeeded on this box.
  const options = (encodersData?.encoders ?? []).filter((e) => e.family === targetFamily);
  const bestHardware = options.find((e) => e.backend !== "software" && e.available);

  // Default to the best available hardware option once detection resolves,
  // unless the user already picked something themselves.
  useEffect(() => {
    if (!backendTouched && bestHardware) setBackend(bestHardware.backend);
  }, [bestHardware, backendTouched]);

  const [stopping, setStopping] = useState(false);
  const stopRequested = useRef(false);

  const { data: job, mutate: mutateJob } = useSWR<ConversionJob>(
    jobId ? `/api/codec-analysis/convert/${jobId}` : null,
    fetcher,
    {
      refreshInterval: (latest) => (latest && DONE_STATUSES.has(latest.status) ? 0 : 2000),
    }
  );

  useEffect(() => {
    if (job?.status === "success") onConverted();
    // Nothing to review after a stop (unlike a completed conversion's size
    // summary) — close automatically once it's actually confirmed stopped,
    // rather than leaving the user to notice and close it themselves.
    if (job?.status === "canceled" && stopRequested.current) onClose();
    // Only a change of status may fire these, not a parent re-render handing in new callbacks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job?.status]);

  const logRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [job?.log]);

  async function start(acknowledgeHardlink = false) {
    setStarting(true);
    setStartError(null);
    try {
      const res = await fetch("/api/codec-analysis/convert", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          instanceId,
          itemId,
          filePath,
          title,
          originalSize,
          backend,
          targetFamily,
          ...(acknowledgeHardlink ? { acknowledgeHardlink: true } : {}),
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.status === 409 && body.code === "hardlinked") {
        setHardlinked(true);
        return;
      }
      if (!res.ok) throw new Error(body.error ?? `start failed: ${res.status}`);
      setHardlinked(false);
      setJobId(body.id);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : "Failed to start conversion.");
    } finally {
      setStarting(false);
    }
  }

  async function cancel() {
    if (!jobId) return;
    stopRequested.current = true;
    setStopping(true);
    try {
      await fetch(`/api/codec-analysis/convert/${jobId}/cancel`, {
        method: "POST",
      });
      // Don't wait for the next 2s poll — refetch right away so "Stopping…"
      // doesn't linger longer than it has to.
      await mutateJob();
    } finally {
      setStopping(false);
    }
  }

  const isDone = !!job && DONE_STATUSES.has(job.status);
  const isActive = !!job && !isDone;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={isDone || !jobId ? onClose : undefined}
    >
      <div
        className={`w-full rounded-lg border border-slate-800 bg-slate-900 p-5 ${
          jobId ? "max-w-lg" : "max-w-md"
        }`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-start justify-between gap-4">
          <h3 className="font-medium">{title}</h3>
          <ModalCloseButton onClose={onClose} className="-mr-1 -mt-1" />
        </div>

        {!jobId && (
          <>
            <p className="mb-1 text-sm text-slate-300">
              Re-encode this file from <span className="font-medium">{codec}</span> to{" "}
              <span className="font-medium">{FAMILY_LABEL[targetFamily]}</span>, in place.
            </p>
            <p className="mb-4 text-xs text-slate-500">
              Current size {formatBytes(originalSize)}. Runs on the server with ffmpeg and can take
              a long time for large files. Only one conversion runs at a time. The original is kept
              alongside as a <code>.bak</code> file after a successful conversion; nothing deletes
              it automatically, so clean it up once you&apos;ve confirmed playback.
            </p>

            <label className="mb-1 block text-xs text-slate-400">Encoder</label>
            <div className="mb-4 space-y-1.5">
              {options.map((opt) => (
                <label key={opt.backend} className="flex items-start gap-2 text-sm text-slate-300">
                  <input
                    type="radio"
                    className="mt-0.5"
                    checked={backend === opt.backend}
                    disabled={!opt.available}
                    onChange={() => {
                      setBackendTouched(true);
                      setBackend(opt.backend);
                    }}
                  />
                  <span>
                    {backendLabel(opt.backend)}
                    {opt.backend !== "software" && (
                      <span className="text-slate-500"> - {opt.label}, faster</span>
                    )}
                    {opt.backend === "software" && (
                      <span className="text-slate-500"> - slower, always available</span>
                    )}
                    {!opt.available && (
                      <span className="block text-xs text-slate-600">
                        Not available{opt.reason ? `: ${opt.reason}` : ""}
                      </span>
                    )}
                  </span>
                </label>
              ))}
              {!encodersData && (
                <p className="text-xs text-slate-600">Checking hardware encode support…</p>
              )}
            </div>

            {startError && <p className="mb-3 text-xs text-red-400">{startError}</p>}
            {hardlinked && (
              <div
                role="alert"
                className="mb-4 rounded-md border border-amber-700 bg-amber-950/40 p-3 text-xs text-amber-200"
              >
                <p className="mb-1 font-medium">This file is also stored somewhere else</p>
                <p>
                  Another file on this disk shares this one&apos;s data, most likely the copy your
                  torrent client is seeding. Converting replaces the library file with a new one, so
                  the seeding copy stays as it is and the disk use for this title roughly doubles.
                  If you were relying on that link to keep seeding without extra space, the seeding
                  copy will now take its own space too.
                </p>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <button
                onClick={onClose}
                disabled={starting}
                className="rounded-md px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
              >
                Cancel
              </button>
              <button
                onClick={() => start(hardlinked)}
                disabled={starting}
                className="rounded-md bg-emerald-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-600 disabled:opacity-60"
              >
                {starting ? "Starting…" : hardlinked ? "Convert anyway" : "Convert"}
              </button>
            </div>
          </>
        )}

        {jobId && job && (
          <div className="space-y-3">
            <p
              className={`text-sm ${
                job.status === "success"
                  ? "text-emerald-400"
                  : job.status === "failed"
                    ? "text-red-400"
                    : "text-slate-300"
              }`}
            >
              {statusLine(job)}
            </p>
            <p className="text-xs text-slate-500">
              {FAMILY_LABEL[job.targetFamily]} via {backendLabel(job.backend)}
            </p>

            {isActive && (
              <div className="h-2 overflow-hidden rounded-full bg-slate-800">
                <div
                  className="h-full bg-sky-600 transition-all"
                  style={{ width: `${job.progressPercent}%` }}
                />
              </div>
            )}

            {job.status === "success" && job.newSize != null && (
              <p className="text-xs text-slate-400">
                {formatBytes(originalSize)} → {formatBytes(job.newSize)}
                {originalSize > 0 &&
                  ` (${(100 - (job.newSize / originalSize) * 100).toFixed(0)}% smaller)`}
                . Original kept at <code className="break-all">{job.backupPath}</code>.
              </p>
            )}
            {job.status === "failed" && job.error && (
              <p className="text-xs text-red-400">{job.error}</p>
            )}

            {job.log && (
              <div>
                <p className="mb-1 text-xs uppercase tracking-wide text-slate-500">Output</p>
                <pre
                  ref={logRef}
                  className="max-h-56 overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-black/40 p-2 font-mono text-[11px] leading-snug text-slate-400"
                >
                  {job.log}
                </pre>
              </div>
            )}

            <div className="flex justify-end gap-2">
              {isActive && (
                <button
                  onClick={cancel}
                  disabled={stopping}
                  className="rounded-md px-3 py-1.5 text-sm text-red-400 hover:bg-red-950 disabled:opacity-60"
                >
                  {stopping ? "Stopping…" : job.status === "queued" ? "Cancel" : "Stop"}
                </button>
              )}
              {isDone && (
                <button
                  onClick={onClose}
                  className="rounded-md bg-slate-800 px-3 py-1.5 text-sm text-slate-200 hover:bg-slate-700"
                >
                  Close
                </button>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
