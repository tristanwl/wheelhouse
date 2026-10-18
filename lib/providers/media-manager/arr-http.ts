import {
  createHttpClient,
  ProviderError,
  type HttpClient,
  type RequestOptions,
} from "../core/http";
import type { ConnectionResult } from "../core/types";
import type {
  HealthIssue,
  Indexer,
  ManagerKind,
  MediaManagerBase,
  QualityProfile,
  QueueRecord,
  QueueSummary,
  RootFolder,
  SystemStatus,
} from "./types";

export interface ArrConfig {
  url: string;
  apiKey: string;
}

/**
 * How long a library list stays cached. Short on purpose: the point is to
 * collapse the burst of requests one page load makes (and repeat visits), not
 * to hide changes. Writes through the client clear it immediately.
 */
export const LIBRARY_CACHE_TTL_MS = 5000;

export interface ArrHttp {
  http: HttpClient;
  /** GET under /api/v3. */
  get<T>(path: string, options?: RequestOptions): Promise<T>;
  /** Writes under /api/v3. Clears the response cache, even if the call fails part-way. */
  send<T = void>(method: "POST" | "PUT" | "DELETE", path: string, body?: unknown): Promise<T>;
}

/** Sonarr and Radarr share one HTTP dialect: X-Api-Key header, JSON under /api/v3. */
export function createArrHttp(config: ArrConfig, label: string): ArrHttp {
  const http = createHttpClient({
    baseUrl: config.url,
    label,
    headers: { "X-Api-Key": config.apiKey },
    secrets: [config.apiKey],
  });

  async function call<T>(path: string, init: RequestOptions): Promise<T> {
    let text: string;
    try {
      text = await (await http.fetch(`/api/v3/${path}`, init)).text();
    } catch (err) {
      // A wrong key is a plain 401 in the *arr apps.
      if (err instanceof ProviderError && err.status === 401) {
        throw new ProviderError(`${label}: API key incorrect`, 401);
      }
      throw err;
    }
    // Some endpoints (Radarr's DELETE, for one) answer 200 with an empty body
    // rather than 204, so check the body itself, not just the status.
    if (!text) return undefined as T;
    try {
      return JSON.parse(text) as T;
    } catch {
      throw new ProviderError(`${label} ${path.split("?")[0]} returned invalid JSON`);
    }
  }

  return {
    http,
    get: (path, options) => call(path, options ?? {}),
    async send<T>(method: "POST" | "PUT" | "DELETE", path: string, body?: unknown) {
      try {
        return await call<T>(path, {
          method,
          ...(body === undefined
            ? {}
            : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
        });
      } finally {
        http.invalidate();
      }
    },
  };
}

/** The calls that are the same for Sonarr and Radarr. */
export function createArrBase(
  arr: ArrHttp,
  kind: ManagerKind
): Omit<MediaManagerBase, "lookup" | "lookupByExternalId" | "add" | "remove" | "rescan"> {
  const getSystemStatus = () => arr.get<SystemStatus>("system/status");
  const getHealth = () => arr.get<HealthIssue[]>("health");
  const getIndexers = () => arr.get<Indexer[]>("indexer");
  const getQueueSummary = async (): Promise<QueueSummary> => {
    const data = await arr.get<{ totalRecords: number; records: QueueRecord[] }>("queue", {
      query: { page: 1, pageSize: 25 },
    });
    return { totalRecords: data.totalRecords, records: data.records };
  };

  return {
    kind,
    getSystemStatus,
    getHealth,
    getIndexers,
    getQueueSummary,
    getRootFolders: () => arr.get<RootFolder[]>("rootfolder"),
    getQualityProfiles: () => arr.get<QualityProfile[]>("qualityprofile"),

    async testConnection(): Promise<ConnectionResult> {
      try {
        const status = await getSystemStatus();
        return { ok: true, version: status.version };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },

    async getStatus() {
      const [status, health, queue, indexers] = await Promise.all([
        getSystemStatus(),
        getHealth(),
        getQueueSummary(),
        getIndexers(),
      ]);
      return {
        version: status.version,
        health,
        queueCount: queue.totalRecords,
        indexers: {
          total: indexers.length,
          enabled: indexers.filter((i) => i.enableRss).length,
          issues: health.filter((h) => h.source.startsWith("Indexer")).map((h) => h.message),
        },
      };
    },
  };
}
