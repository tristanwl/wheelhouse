import { z } from "zod";
import { createHttpClient, ProviderError } from "../core/http";
import { defineProvider } from "../core/registry";
import type {
  DownloadClient,
  DownloadClientCapabilities,
  DownloadClientStatus,
  DownloadItem,
  DownloadState,
} from "./types";

const configSchema = z.object({
  url: z.url(),
  username: z.string().min(1),
  password: z.string().min(1),
});

type QbittorrentConfig = z.infer<typeof configSchema>;

interface QbTorrent {
  hash: string;
  name: string;
  state: string;
  progress: number;
  size: number;
  total_size: number;
  amount_left: number;
  eta: number;
  ratio: number;
  num_seeds: number;
  upspeed: number;
}

interface QbTransferInfo {
  dl_info_speed: number;
  up_info_speed: number;
}

// qBittorrent reports an unknown ETA as 100 days, in seconds.
const ETA_INFINITY = 8640000;

type Mapped = { state: DownloadState; stateDetail?: string };

// Both v4 (paused*) and v5 (stopped*) names; v5 renamed them.
const STATE_MAP: Record<string, Mapped> = {
  downloading: { state: "downloading" },
  forcedDL: { state: "downloading" },
  forcedMetaDL: { state: "downloading" },
  metaDL: { state: "downloading", stateDetail: "Fetching metadata" },
  allocating: { state: "downloading", stateDetail: "Allocating" },
  stalledDL: { state: "stalled" },
  queuedDL: { state: "queued" },
  queuedUP: { state: "queued" },
  checkingDL: { state: "postprocessing", stateDetail: "Checking" },
  checkingUP: { state: "postprocessing", stateDetail: "Checking" },
  checkingResumeData: { state: "postprocessing", stateDetail: "Checking" },
  moving: { state: "postprocessing", stateDetail: "Moving" },
  uploading: { state: "seeding" },
  forcedUP: { state: "seeding" },
  stalledUP: { state: "seeding" },
  pausedDL: { state: "paused" },
  stoppedDL: { state: "paused" },
  pausedUP: { state: "completed" },
  stoppedUP: { state: "completed" },
  error: { state: "failed" },
  missingFiles: { state: "failed", stateDetail: "Files missing" },
};

/** Maps a qBittorrent torrent state (v4 or v5 name) to the normalized state. */
export function mapQbittorrentState(state: string): Mapped {
  // A state we haven't seen: keep the client's wording instead of guessing.
  return STATE_MAP[state] ?? { state: "downloading", stateDetail: state };
}

function whole(value: unknown): number {
  const n = Math.round(Number(value));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function sizeOf(t: QbTorrent): number {
  // size is what's selected for download; a magnet without metadata has 0 and total_size -1.
  return whole(t.size) || whole(t.total_size);
}

function etaOf(eta: number): number | null {
  return eta > 0 && eta < ETA_INFINITY ? Math.round(eta) : null;
}

function toItem(t: QbTorrent): DownloadItem {
  const size = sizeOf(t);
  return {
    id: t.hash,
    name: t.name,
    protocol: "torrent",
    ...mapQbittorrentState(t.state),
    progress: Math.min(Math.max(Number(t.progress) || 0, 0), 1),
    sizeBytes: size,
    remainingBytes: Math.min(whole(t.amount_left), size),
    etaSeconds: etaOf(t.eta),
    torrent: {
      ratio: Math.max(Number(t.ratio) || 0, 0),
      seeders: whole(t.num_seeds),
      uploadBytesPerSec: whole(t.upspeed),
    },
  };
}

function createClient(config: QbittorrentConfig, label: string): DownloadClient {
  const http = createHttpClient({
    baseUrl: config.url,
    label,
    secrets: [config.password],
  });

  // The session cookie (SID in v4, QBT_SID_<port> in v5), kept for the life of the client.
  let cookie: string | undefined;
  let loginInFlight: Promise<void> | undefined;

  async function doLogin(): Promise<void> {
    let res: Response;
    try {
      res = await http.fetch("/api/v2/auth/login", {
        method: "POST",
        body: new URLSearchParams({ username: config.username, password: config.password }),
      });
    } catch (err) {
      // v4 answers a wrong login with 200 "Fails."; v5 with 401. A banned IP is 403 in both.
      if (err instanceof ProviderError && err.status === 401) {
        throw new ProviderError(`${label}: login failed (check the username and password)`, 401);
      }
      if (err instanceof ProviderError && err.status === 403) {
        throw new ProviderError(
          `${label}: login refused, this IP may be banned after too many failed attempts`,
          403
        );
      }
      throw err;
    }
    const body = (await res.text()).trim();
    if (res.status === 200 && body !== "Ok.") {
      throw new ProviderError(`${label}: login failed (check the username and password)`, 401);
    }
    cookie = res.headers
      .getSetCookie()
      .map((c) => c.split(";")[0])
      .join("; ");
  }

  function login(): Promise<void> {
    loginInFlight ??= doLogin().finally(() => {
      loginInFlight = undefined;
    });
    return loginInFlight;
  }

  async function authed(path: string): Promise<Response> {
    if (cookie === undefined) await login();
    const sent = cookie;
    try {
      return await http.fetch(path, { headers: sent ? { Cookie: sent } : {} });
    } catch (err) {
      // An expired session (qBittorrent restarted, or the one-hour timeout) is a 403; sign in once more.
      if (err instanceof ProviderError && (err.status === 403 || err.status === 401)) {
        // Parallel calls fail together: the first one signs in, the rest wait for that.
        if (cookie === sent) {
          cookie = undefined;
          await login();
        } else {
          await loginInFlight;
        }
        try {
          return await http.fetch(path, { headers: cookie ? { Cookie: cookie } : {} });
        } catch (retryErr) {
          if (retryErr instanceof ProviderError && retryErr.status === 403) {
            throw new ProviderError(`${label}: not authorized after signing in`, 403);
          }
          throw retryErr;
        }
      }
      throw err;
    }
  }

  async function json<T>(path: string): Promise<T> {
    const res = await authed(path);
    try {
      return (await res.json()) as T;
    } catch {
      throw new ProviderError(`${label} ${path} returned invalid JSON`);
    }
  }

  async function version(): Promise<string> {
    // Plain text: "v5.2.4".
    return (await (await authed("/api/v2/app/version")).text()).trim().replace(/^v/, "");
  }

  return {
    async testConnection() {
      try {
        return { ok: true, version: await version() };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },

    async getStatus(): Promise<DownloadClientStatus> {
      const [transfer, torrents, ver] = await Promise.all([
        json<QbTransferInfo>("/api/v2/transfer/info"),
        json<QbTorrent[]>("/api/v2/torrents/info"),
        version(),
      ]);

      const mapped = torrents.map((t) => ({ t, ...mapQbittorrentState(t.state) }));
      // "Queue" here means torrents that still have something to download.
      const incomplete = mapped.filter(({ t }) => t.progress < 1);
      const seeding = mapped.filter(({ state }) => state === "seeding").length;
      const speed = whole(transfer.dl_info_speed);
      const upload = whole(transfer.up_info_speed);
      const left = incomplete.reduce((sum, { t }) => sum + whole(t.amount_left), 0);
      // qBittorrent has no global pause, so "paused" means everything still downloading is stopped.
      const paused = incomplete.length > 0 && incomplete.every(({ state }) => state === "paused");

      return {
        paused,
        statusText: paused
          ? "Paused"
          : speed > 0
            ? "Downloading"
            : seeding > 0 || upload > 0
              ? "Seeding"
              : "Idle",
        speedBytesPerSec: speed,
        queueCount: incomplete.length,
        etaSeconds: speed > 0 && left > 0 ? Math.ceil(left / speed) : null,
        version: ver,
        warnings: [],
        torrent: { seeding, uploadBytesPerSec: upload },
      };
    },

    async listItems(): Promise<DownloadItem[]> {
      const torrents = await json<QbTorrent[]>("/api/v2/torrents/info");
      // A finished, stopped torrent is just a file on disk; everything else is worth a row.
      return torrents.map(toItem).filter((item) => item.state !== "completed");
    },
  };
}

export const qbittorrentProvider = defineProvider<
  QbittorrentConfig,
  DownloadClient,
  DownloadClientCapabilities
>({
  id: "qbittorrent",
  role: "downloadClient",
  displayName: "qBittorrent",
  configSchema,
  secretKeys: ["password"],
  capabilities: { protocol: "torrent", canPause: true, usenetServers: false, seeding: true },
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
