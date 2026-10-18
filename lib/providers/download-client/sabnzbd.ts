import { z } from "zod";
import { createHttpClient, ProviderError, redact } from "../core/http";
import { defineProvider } from "../core/registry";
import type {
  ClientWarning,
  DownloadClient,
  DownloadClientCapabilities,
  DownloadClientStatus,
  DownloadItem,
  DownloadState,
} from "./types";

const configSchema = z.object({
  url: z.url(),
  apiKey: z.string().min(1),
});

type SabnzbdConfig = z.infer<typeof configSchema>;

// SABnzbd reports sizes in MB where 1 MB = 1024 * 1024 bytes.
const MB = 1024 * 1024;

interface SabSlot {
  nzo_id: string;
  filename: string;
  status: string;
  percentage: string;
  mb: string;
  mbleft: string;
  timeleft: string;
}

interface SabQueue {
  status: string;
  paused: boolean;
  kbpersec: string;
  noofslots_total: number;
  timeleft: string;
  version?: string;
  slots: SabSlot[];
}

interface SabServer {
  name: string;
  enable: number;
  connections: number;
  ssl: number;
}

interface SabWarning {
  type: string;
  text: string;
  time: number;
}

interface SabHistorySlot {
  nzo_id: string;
  status: string;
}

// SABnzbd's queue view collapses every post-download phase (Extracting,
// Verifying, Repairing, Moving, Running scripts) into "Downloading"; that's
// baked into its API. The accurate per-job phase only appears in mode=history,
// which documents itself as "the jobs still in post-processing and the
// database history". So anything in history that isn't Completed or Failed is
// still being processed, and tells us the real phase.
const POSTPROC_DONE = new Set(["Completed", "Failed"]);

// "Refused connection from ..." is SABnzbd's host whitelist rejecting some other
// client's web UI access. It says nothing about download health and would
// read as a false alarm on a dashboard.
const IGNORED_WARNING = /^Refused connection/;

/** SABnzbd writes durations as H:MM:SS, or D:HH:MM:SS once past a day. 0 means "unknown". */
export function parseSabDuration(value: string | undefined): number | null {
  if (!value) return null;
  const parts = value.split(":").map(Number);
  if (parts.length < 3 || parts.length > 4 || parts.some((n) => !Number.isFinite(n) || n < 0)) {
    return null;
  }
  const [s, m, h, d = 0] = parts.reverse();
  const total = d * 86400 + h * 3600 + m * 60 + s;
  return total > 0 ? total : null;
}

function toNumber(value: string | number | undefined): number {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
}

function slotState(
  status: string,
  processingPhase: string | undefined
): { state: DownloadState; stateDetail?: string } {
  if (processingPhase) return { state: "postprocessing", stateDetail: processingPhase };
  switch (status) {
    case "Downloading":
    case "Fetching":
      return { state: "downloading" };
    case "Paused":
      return { state: "paused" };
    case "Queued":
      return { state: "queued" };
    case "Grabbing":
    case "Propagating":
      return { state: "queued", stateDetail: status };
    default:
      // A phase we haven't seen: keep the client's wording instead of guessing.
      return { state: "downloading", stateDetail: status };
  }
}

function createClient(config: SabnzbdConfig, label: string): DownloadClient {
  const http = createHttpClient({ baseUrl: config.url, label, secrets: [config.apiKey] });

  async function api<T>(mode: string, query: Record<string, string | number> = {}): Promise<T> {
    try {
      const data = await http.json<T & { error?: string }>("/api", {
        query: { mode, output: "json", apikey: config.apiKey, ...query },
      });
      // Some SABnzbd versions answer errors with 200 and an error field.
      if (data && typeof data === "object" && typeof data.error === "string" && data.error) {
        throw new ProviderError(redact(`${label}: ${data.error}`, [config.apiKey]));
      }
      return data;
    } catch (err) {
      // SABnzbd 4.x answers a wrong key with 403 and a plain-text body.
      if (err instanceof ProviderError && err.status === 403) {
        throw new ProviderError(`${label}: API key incorrect`, 403);
      }
      throw err;
    }
  }

  return {
    async testConnection() {
      try {
        const { queue } = await api<{ queue: SabQueue }>("queue", { limit: 1 });
        return { ok: true, version: queue.version };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },

    async getStatus(): Promise<DownloadClientStatus> {
      const [{ queue }, { config: cfg }, { warnings }] = await Promise.all([
        api<{ queue: SabQueue }>("queue"),
        api<{ config: { servers: SabServer[] } }>("get_config", { section: "servers" }),
        api<{ warnings: SabWarning[] }>("warnings"),
      ]);

      const recent: ClientWarning[] = warnings
        .filter((w) => !IGNORED_WARNING.test(w.text))
        .slice(-3)
        .map((w) => ({
          message: w.text,
          severity: w.type === "ERROR" ? "error" : "warning",
          at: Number.isFinite(w.time) ? new Date(w.time * 1000).toISOString() : undefined,
        }));

      return {
        paused: queue.paused,
        statusText: queue.status,
        speedBytesPerSec: Math.round(toNumber(queue.kbpersec) * 1024),
        queueCount: queue.noofslots_total,
        etaSeconds: parseSabDuration(queue.timeleft),
        version: queue.version,
        warnings: recent,
        usenet: {
          servers: cfg.servers.map((s) => ({
            name: s.name,
            connections: s.connections,
            ssl: Boolean(s.ssl),
            enabled: Boolean(s.enable),
          })),
        },
      };
    },

    async listItems(): Promise<DownloadItem[]> {
      const [{ queue }, { history }] = await Promise.all([
        api<{ queue: SabQueue }>("queue"),
        api<{ history: { slots: SabHistorySlot[] } }>("history", { limit: 25 }),
      ]);

      const phases = new Map<string, string>();
      for (const slot of history.slots) {
        if (!POSTPROC_DONE.has(slot.status)) phases.set(slot.nzo_id, slot.status);
      }

      return queue.slots.map((slot) => {
        const size = Math.round(toNumber(slot.mb) * MB);
        const left = Math.min(Math.round(toNumber(slot.mbleft) * MB), size);
        return {
          id: slot.nzo_id,
          name: slot.filename,
          protocol: "usenet" as const,
          ...slotState(slot.status, phases.get(slot.nzo_id)),
          progress: Math.min(Math.max(toNumber(slot.percentage) / 100, 0), 1),
          sizeBytes: size,
          remainingBytes: left,
          etaSeconds: parseSabDuration(slot.timeleft),
        };
      });
    },
  };
}

export const sabnzbdProvider = defineProvider<
  SabnzbdConfig,
  DownloadClient,
  DownloadClientCapabilities
>({
  id: "sabnzbd",
  role: "downloadClient",
  displayName: "SABnzbd",
  configSchema,
  secretKeys: ["apiKey"],
  capabilities: { protocol: "usenet", canPause: true, usenetServers: true, seeding: false },
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
