import type { ConnectionResult } from "../core/types";

export type DownloadProtocol = "usenet" | "torrent";

export type DownloadState =
  | "queued"
  | "downloading"
  | "stalled"
  | "postprocessing"
  | "seeding"
  | "paused"
  | "completed"
  | "failed";

export interface DownloadItem {
  id: string;
  name: string;
  protocol: DownloadProtocol;
  state: DownloadState;
  /** The client's own wording where it adds information: "Extracting", "Checking", "Grabbing". */
  stateDetail?: string;
  /** 0..1 */
  progress: number;
  sizeBytes: number;
  remainingBytes: number;
  /** null when unknown (stalled, paused, or the client doesn't say). */
  etaSeconds: number | null;
  torrent?: { ratio: number; seeders?: number; uploadBytesPerSec: number };
}

export interface ClientWarning {
  message: string;
  severity: "warning" | "error";
  /** ISO timestamp, when the client reports one. */
  at?: string;
}

export interface DownloadClientStatus {
  paused: boolean;
  /** The client's own one-word status, e.g. SABnzbd's "Idle" / "Downloading". */
  statusText?: string;
  speedBytesPerSec: number;
  queueCount: number;
  /** Time left for the whole queue. null when unknown or nothing is queued. */
  etaSeconds: number | null;
  version?: string;
  warnings: ClientWarning[];
  usenet?: {
    servers: { name: string; connections: number; ssl: boolean; enabled: boolean }[];
  };
  torrent?: { seeding: number; uploadBytesPerSec: number };
}

/** Static, per provider: lets the UI decide what to render without calling the service. */
export interface DownloadClientCapabilities {
  protocol: DownloadProtocol;
  canPause: boolean;
  usenetServers: boolean;
  seeding: boolean;
}

export interface DownloadClient {
  testConnection(): Promise<ConnectionResult>;
  getStatus(): Promise<DownloadClientStatus>;
  listItems(): Promise<DownloadItem[]>;
}

/** One-line label for an item's state, for list rows. */
export function describeDownloadState(item: Pick<DownloadItem, "state" | "stateDetail">): string {
  if (item.state === "postprocessing") return item.stateDetail ?? "Processing";
  const base: Record<Exclude<DownloadState, "postprocessing">, string> = {
    queued: "Queued",
    downloading: "Downloading",
    stalled: "Stalled",
    seeding: "Seeding",
    paused: "Paused",
    completed: "Completed",
    failed: "Failed",
  };
  return item.stateDetail ?? base[item.state];
}
