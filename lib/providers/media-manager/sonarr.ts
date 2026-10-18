import { z } from "zod";
import { defineProvider } from "../core/registry";
import { createArrBase, createArrHttp, LIBRARY_CACHE_TTL_MS } from "./arr-http";
import type {
  Episode,
  EpisodeFile,
  LookupResult,
  MediaManagerCapabilities,
  SeriesManager,
  SonarrSeries,
  SonarrSeriesDetail,
  SonarrSeriesFull,
} from "./types";

const configSchema = z.object({
  url: z.url(),
  apiKey: z.string().min(1),
});

type SonarrConfig = z.infer<typeof configSchema>;

function createClient(config: SonarrConfig, label: string): SeriesManager {
  const arr = createArrHttp(config, label);

  return {
    ...createArrBase(arr, "series"),
    kind: "series",

    getSeries: () => arr.get<SonarrSeries[]>("series", { cacheTtlMs: LIBRARY_CACHE_TTL_MS }),
    getSeriesById: (id) => arr.get<SonarrSeriesFull>(`series/${id}`),
    getSeriesDetail: (id) => arr.get<SonarrSeriesDetail>(`series/${id}`),
    getEpisodeFiles: (seriesId) => arr.get<EpisodeFile[]>("episodefile", { query: { seriesId } }),
    getEpisodes: (seriesId) => arr.get<Episode[]>("episode", { query: { seriesId } }),

    lookup: (term) => arr.get<LookupResult[]>("series/lookup", { query: { term } }),
    async lookupByExternalId(tvdbId) {
      const results = await arr.get<LookupResult[]>("series/lookup", {
        query: { term: `tvdb:${tvdbId}` },
      });
      return results[0];
    },

    add: (payload) => arr.send<LookupResult>("POST", "series", payload),
    async remove(id) {
      await arr.send("DELETE", `series/${id}?deleteFiles=true&addImportListExclusion=false`);
    },
    async rescan(seriesId) {
      await arr.send("POST", "command", { name: "RescanSeries", seriesId });
    },
    async deleteEpisodeFile(id) {
      await arr.send("DELETE", `episodefile/${id}`);
    },
    updateSeries: (series) => arr.send<SonarrSeriesFull>("PUT", `series/${series.id}`, series),
  };
}

export const sonarrProvider = defineProvider<SonarrConfig, SeriesManager, MediaManagerCapabilities>(
  {
    id: "sonarr",
    role: "mediaManager",
    displayName: "Sonarr",
    configSchema,
    secretKeys: ["apiKey"],
    capabilities: { kind: "series" },
    create: (config, ctx) => createClient(config, ctx.instanceId),
  }
);
