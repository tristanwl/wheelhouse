import { z } from "zod";
import { createHttpClient, ProviderError } from "../core/http";
import { defineProvider } from "../core/registry";
import type { ConnectionResult } from "../core/types";

// TMDB is the only metadata provider, so it has no interface: Explore's
// categories are TMDB-specific. It is a provider so that it shows up in
// config, testConnection and feature gating. Revisit when TVDB is on the table.

const configSchema = z.object({ apiKey: z.string().min(1) });

type TmdbConfig = z.infer<typeof configSchema>;

export interface TmdbClient {
  /** GET a TMDB v3 path; the API key is added here and never leaves this client. */
  get<T>(path: string, params?: Record<string, string>): Promise<T>;
  testConnection(): Promise<ConnectionResult>;
}

const TMDB_BASE_URL = "https://api.themoviedb.org/3";

function createClient(config: TmdbConfig, label: string): TmdbClient {
  // TMDB takes its key as an `api_key` query parameter. The http helper never
  // echoes URLs in errors, and the key is redacted from them as well.
  const http = createHttpClient({
    baseUrl: TMDB_BASE_URL,
    label,
    headers: { Accept: "application/json" },
    secrets: [config.apiKey],
  });

  const get = <T>(path: string, params: Record<string, string> = {}) =>
    http.json<T>(path, { query: { ...params, api_key: config.apiKey } });

  return {
    get,

    async testConnection() {
      try {
        // The cheapest authenticated call: a few hundred bytes of static config.
        await get("/configuration");
        return { ok: true };
      } catch (err) {
        if (err instanceof ProviderError && (err.status === 401 || err.status === 403)) {
          return { ok: false, error: `${label}: API key rejected` };
        }
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },
  };
}

export const tmdbProvider = defineProvider<TmdbConfig, TmdbClient, Record<string, never>>({
  id: "tmdb",
  role: "metadata",
  displayName: "TMDB",
  configSchema,
  secretKeys: ["apiKey"],
  capabilities: {},
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
