import { z } from "zod";
import { defineProvider } from "../core/registry";
import { createArrBase, createArrHttp, LIBRARY_CACHE_TTL_MS } from "./arr-http";
import type {
  LookupResult,
  MediaManagerCapabilities,
  MovieManager,
  RadarrMovie,
  RadarrMovieDetail,
} from "./types";

const configSchema = z.object({
  url: z.url(),
  apiKey: z.string().min(1),
});

type RadarrConfig = z.infer<typeof configSchema>;

function createClient(config: RadarrConfig, label: string): MovieManager {
  const arr = createArrHttp(config, label);

  return {
    ...createArrBase(arr, "movies"),
    kind: "movies",

    getMovies: () => arr.get<RadarrMovie[]>("movie", { cacheTtlMs: LIBRARY_CACHE_TTL_MS }),
    getMovieDetail: (id) => arr.get<RadarrMovieDetail>(`movie/${id}`),

    lookup: (term) => arr.get<LookupResult[]>("movie/lookup", { query: { term } }),
    lookupByExternalId: (tmdbId) =>
      arr.get<LookupResult | undefined>("movie/lookup/tmdb", { query: { tmdbId } }),

    add: (payload) => arr.send<LookupResult>("POST", "movie", payload),
    async remove(id) {
      await arr.send("DELETE", `movie/${id}?deleteFiles=true&addImportListExclusion=false`);
    },
    async rescan(movieId) {
      await arr.send("POST", "command", { name: "RescanMovie", movieId });
    },
  };
}

export const radarrProvider = defineProvider<RadarrConfig, MovieManager, MediaManagerCapabilities>({
  id: "radarr",
  role: "mediaManager",
  displayName: "Radarr",
  configSchema,
  secretKeys: ["apiKey"],
  capabilities: { kind: "movies" },
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
