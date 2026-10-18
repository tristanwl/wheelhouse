"use client";

import { useEffect, useRef, useState } from "react";
import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import { formatBytes } from "@/lib/format";
import type { CodecAnalysisResponse } from "@/app/api/codec-analysis/route";
import type { CodecFamily, ConversionCandidate, Tier } from "@/lib/codec";
import type { ConversionJob } from "@/lib/convert-queue";
import type { CodecPreferences, PreferableFamily } from "@/lib/settings";
import { ConvertModal } from "@/components/ConvertModal";

type Service = "radarr" | "sonarr";

const BUCKET_LABELS: Record<CodecFamily, string> = {
  h264: "H.264",
  hevc: "HEVC",
  legacy: "Legacy / Other",
};

const FAMILY_LABEL: Record<PreferableFamily, string> = { h264: "H.264", hevc: "HEVC" };
const TIER_LABEL: Record<Tier, string> = { hd: "HD", uhd: "UHD (4K+)" };

const ACTIVE_STATUSES = new Set(["queued", "probing", "running"]);

function reasonLabel(c: ConversionCandidate): string {
  if (c.reason === "legacy-codec") {
    return `Legacy codec → ${FAMILY_LABEL[c.targetFamily]}`;
  }
  return `Prefer ${FAMILY_LABEL[c.targetFamily]} for ${TIER_LABEL[c.tier]}`;
}

export default function CodecsPage() {
  const [service, setService] = useState<Service>("radarr");
  const [copied, setCopied] = useState(false);
  const [converting, setConverting] = useState<{
    candidate: ConversionCandidate;
    jobId?: string;
  } | null>(null);
  const { data, error, mutate } = useSWR<CodecAnalysisResponse & { error?: string }>(
    `/api/codec-analysis?service=${service}`,
    fetcher
  );

  const { data: prefs, mutate: mutatePrefs } = useSWR<CodecPreferences>(
    "/api/codec-analysis/settings",
    fetcher
  );

  async function updatePreference(tier: Tier, family: PreferableFamily) {
    if (!prefs) return;
    const next = { ...prefs, [tier]: family };
    await mutatePrefs(
      fetch("/api/codec-analysis/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      }).then((r) => r.json()),
      { optimisticData: next, revalidate: false }
    );
    mutate();
  }

  // Polled independently of the modal so row-level progress indicators
  // (and reattaching after closing the modal) work even if you never had
  // it open in this tab, or a job kept running across a page reload.
  const { data: jobsData } = useSWR<{ jobs: ConversionJob[] }>(
    "/api/codec-analysis/convert",
    fetcher,
    { refreshInterval: 2000 }
  );
  const jobsByPath = new Map(
    (jobsData?.jobs ?? []).filter((j) => ACTIVE_STATUSES.has(j.status)).map((j) => [j.filePath, j])
  );

  // A job can finish while the modal isn't open to notice via onConverted —
  // catch that here so the candidates list still refreshes and the row
  // reverts to a plain "Convert" button.
  const handledSuccessIds = useRef(new Set<string>());
  useEffect(() => {
    for (const job of jobsData?.jobs ?? []) {
      if (job.status === "success" && !handledSuccessIds.current.has(job.id)) {
        handledSuccessIds.current.add(job.id);
        mutate();
      }
    }
  }, [jobsData, mutate]);

  function copyList() {
    if (!data) return;
    const text = data.candidates
      .map(
        (c) =>
          `${c.title}\t${c.codec}\t${c.resolution ?? "?"}\t${formatBytes(c.size)}\t${reasonLabel(c)}`
      )
      .join("\n");
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-medium">Codec Analysis</h1>
          <p className="text-sm text-slate-500">
            What&apos;s actually worth converting, not everything that isn&apos;t your preferred
            codec.
          </p>
        </div>
        <div className="flex rounded-md border border-slate-700 text-sm">
          <button
            onClick={() => setService("radarr")}
            className={`rounded-l-md px-3 py-1.5 ${
              service === "radarr"
                ? "bg-violet-900 text-violet-200"
                : "text-slate-400 hover:bg-slate-800"
            }`}
          >
            Movies
          </button>
          <button
            onClick={() => setService("sonarr")}
            className={`rounded-r-md px-3 py-1.5 ${
              service === "sonarr" ? "bg-sky-900 text-sky-200" : "text-slate-400 hover:bg-slate-800"
            }`}
          >
            TV
          </button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-4 rounded-lg border border-slate-800 bg-slate-900 p-3 text-sm">
        <span className="text-xs uppercase tracking-wide text-slate-500">Preferred codec</span>
        {(["hd", "uhd"] as Tier[]).map((tier) => (
          <label key={tier} className="flex items-center gap-2 text-slate-300">
            {TIER_LABEL[tier]}
            <select
              value={prefs?.[tier] ?? (tier === "hd" ? "h264" : "hevc")}
              onChange={(e) => updatePreference(tier, e.target.value as PreferableFamily)}
              disabled={!prefs}
              className="rounded-md border border-slate-800 bg-slate-950 px-2 py-1 text-sm outline-none focus:border-slate-600"
            >
              <option value="h264">H.264</option>
              <option value="hevc">HEVC</option>
            </select>
          </label>
        ))}
        <span className="text-xs text-slate-600">
          UHD defaults to HEVC (its compression advantage is largest at 4K); HD defaults to H.264
          for the broadest playback compatibility.
        </span>
      </div>

      {(error || data?.error) && (
        <p className="text-sm text-red-400">Failed to load: {data?.error ?? "request failed"}</p>
      )}
      {!data && !error && <p className="text-sm text-slate-500">Analyzing…</p>}

      {data && (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            {(Object.keys(BUCKET_LABELS) as CodecFamily[]).map((family) => {
              const bucket = data.buckets[family];
              return (
                <div key={family} className="rounded-lg border border-slate-800 bg-slate-900 p-4">
                  <p className="text-xs uppercase tracking-wide text-slate-500">
                    {BUCKET_LABELS[family]}
                  </p>
                  <p className="mt-1 text-2xl font-semibold">{bucket.count}</p>
                  <p className="text-sm text-slate-400">
                    {formatBytes(bucket.totalSize)} · {bucket.percentOfLibrary.toFixed(1)}%
                  </p>
                </div>
              );
            })}
          </div>

          {data.skippedCount > 0 && (
            <p className="text-xs text-slate-500">
              {data.skippedCount} file{data.skippedCount === 1 ? "" : "s"} skipped: no file, or no
              media info scanned yet.
            </p>
          )}

          <section>
            <div className="mb-2 flex items-center justify-between">
              <h2 className="text-sm font-medium uppercase tracking-wide text-slate-500">
                Conversion candidates ({data.candidates.length})
              </h2>
              {data.candidates.length > 0 && (
                <button onClick={copyList} className="text-xs text-slate-400 hover:text-slate-200">
                  {copied ? "Copied ✓" : "Copy list"}
                </button>
              )}
            </div>
            {data.candidates.length === 0 ? (
              <p className="text-sm text-slate-500">
                Nothing worth converting: everything already matches your preferred codec for its
                resolution tier (see below).
              </p>
            ) : (
              <div className="overflow-x-auto rounded-lg border border-slate-800">
                <table className="w-full min-w-[720px] text-sm">
                  <thead className="bg-slate-900 text-left text-xs uppercase tracking-wide text-slate-500">
                    <tr>
                      <th className="px-4 py-2">Title</th>
                      <th className="px-4 py-2">Codec</th>
                      <th className="px-4 py-2">Resolution</th>
                      <th className="px-4 py-2">Size</th>
                      <th className="px-4 py-2">Reason</th>
                      <th className="px-4 py-2" />
                    </tr>
                  </thead>
                  <tbody>
                    {data.candidates.map((c, i) => {
                      const activeJob = jobsByPath.get(c.filePath);
                      return (
                        <tr key={i} className="border-t border-slate-800">
                          <td className="px-4 py-2">{c.title}</td>
                          <td className="px-4 py-2 text-slate-400">{c.codec}</td>
                          <td className="px-4 py-2 text-slate-400">{c.resolution ?? "-"}</td>
                          <td className="px-4 py-2 text-slate-400">{formatBytes(c.size)}</td>
                          <td className="px-4 py-2">
                            <span
                              className={`rounded px-1.5 py-0.5 text-xs ${
                                c.reason === "legacy-codec"
                                  ? "bg-red-950 text-red-300"
                                  : "bg-amber-950 text-amber-300"
                              }`}
                            >
                              {reasonLabel(c)}
                            </span>
                          </td>
                          <td className="px-4 py-2 text-right">
                            {activeJob ? (
                              <button
                                onClick={() => setConverting({ candidate: c, jobId: activeJob.id })}
                                className="ml-auto flex items-center gap-2 rounded-md px-2 py-1 text-xs font-medium text-sky-400 hover:bg-sky-950"
                              >
                                {activeJob.status === "queued" ? (
                                  "Queued…"
                                ) : (
                                  <>
                                    <span className="h-1.5 w-12 overflow-hidden rounded-full bg-slate-800">
                                      <span
                                        className="block h-full bg-sky-600"
                                        style={{ width: `${activeJob.progressPercent}%` }}
                                      />
                                    </span>
                                    {activeJob.progressPercent}%
                                  </>
                                )}
                              </button>
                            ) : (
                              <button
                                onClick={() => setConverting({ candidate: c })}
                                className="rounded-md px-2 py-1 text-xs font-medium text-sky-400 hover:bg-sky-950"
                              >
                                Convert
                              </button>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {data.matching.length > 0 && (
            <section>
              <h2 className="mb-1 text-sm font-medium uppercase tracking-wide text-slate-500">
                Already matches preference ({data.matching.length})
              </h2>
              <p className="mb-2 text-xs text-slate-500">
                Already using the preferred codec for their resolution tier, nothing to do.
              </p>
              <div className="overflow-x-auto rounded-lg border border-slate-800">
                <table className="w-full min-w-[480px] text-sm">
                  <thead className="bg-slate-900 text-left text-xs uppercase tracking-wide text-slate-500">
                    <tr>
                      <th className="px-4 py-2">Title</th>
                      <th className="px-4 py-2">Resolution</th>
                      <th className="px-4 py-2">Size</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.matching.map((c, i) => (
                      <tr key={i} className="border-t border-slate-800">
                        <td className="px-4 py-2">{c.title}</td>
                        <td className="px-4 py-2 text-slate-400">{c.resolution ?? "-"}</td>
                        <td className="px-4 py-2 text-slate-400">{formatBytes(c.size)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </>
      )}

      {converting && (
        <ConvertModal
          service={converting.candidate.service}
          itemId={converting.candidate.itemId}
          filePath={converting.candidate.filePath}
          title={converting.candidate.title}
          codec={converting.candidate.codec}
          originalSize={converting.candidate.size}
          targetFamily={converting.candidate.targetFamily}
          existingJobId={converting.jobId}
          onClose={() => setConverting(null)}
          onConverted={() => mutate()}
        />
      )}
    </div>
  );
}
