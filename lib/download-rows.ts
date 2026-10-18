import { formatBytes, formatEta } from "@/lib/format";
import type { DownloadItem, DownloadState } from "@/lib/providers/download-client/types";

// Pure helpers for the dashboard's Downloads list. Type imports only, so the
// browser bundle doesn't pull in server code.

/** The most rows the dashboard draws; the rest become a "+N more" line. */
export const MAX_DOWNLOAD_ROWS = 50;

// Most important first: failures need attention, then what is moving right
// now, then what waits, then what only seeds.
const STATE_RANK: Record<DownloadState, number> = {
  failed: 0,
  postprocessing: 1,
  downloading: 2,
  stalled: 3,
  queued: 4,
  paused: 5,
  seeding: 6,
  completed: 7,
};

/** A copy sorted by state importance; items in the same state keep their order. */
export function sortDownloads<T extends Pick<DownloadItem, "state">>(items: readonly T[]): T[] {
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => STATE_RANK[a.item.state] - STATE_RANK[b.item.state] || a.index - b.index)
    .map(({ item }) => item);
}

/** The first `max` rows and how many were left out. */
export function capDownloads<T>(
  items: readonly T[],
  max: number = MAX_DOWNLOAD_ROWS
): { shown: T[]; hidden: number } {
  return { shown: items.slice(0, max), hidden: Math.max(items.length - max, 0) };
}

export type RowTone = "bad" | "warn" | "good" | "neutral";

/** How loudly a row's state label and bar should speak. */
export function downloadTone(state: DownloadState): RowTone {
  switch (state) {
    case "failed":
      return "bad";
    case "postprocessing":
    case "stalled":
      return "warn";
    case "seeding":
      return "good";
    default:
      return "neutral";
  }
}

/** The two small text lines under a row's bar. */
export function describeDownloadRow(
  item: DownloadItem,
  /** The client's current download speed, shown only on rows that are downloading. */
  speedBytesPerSec?: number
): { left: string; right: string } {
  if (item.state === "seeding") {
    const parts = [];
    if (item.torrent) parts.push(`ratio ${item.torrent.ratio.toFixed(2)}`);
    if (item.torrent?.uploadBytesPerSec) {
      parts.push(`↑ ${formatBytes(item.torrent.uploadBytesPerSec)}/s`);
    }
    return { left: formatBytes(item.sizeBytes), right: parts.join(" · ") };
  }
  if (item.state === "postprocessing") {
    // A check or move has no download progress worth a percentage.
    return { left: formatBytes(item.sizeBytes), right: "" };
  }
  const left =
    item.sizeBytes > 0
      ? `${Math.round(item.progress * 100)}% of ${formatBytes(item.sizeBytes)}`
      : `${Math.round(item.progress * 100)}%`;
  const speed =
    item.state === "downloading" && speedBytesPerSec ? `${formatBytes(speedBytesPerSec)}/s` : "";
  const eta = item.etaSeconds ? `${formatEta(item.etaSeconds)} left` : "";
  return { left, right: [speed, eta].filter(Boolean).join(" · ") };
}
