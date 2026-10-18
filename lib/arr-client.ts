import { env } from "./env";

export type ArrService = "sonarr" | "radarr";

interface ArrConfig {
  baseUrl: string;
  apiKey: string;
}

function configFor(service: ArrService): ArrConfig {
  return service === "sonarr"
    ? { baseUrl: env.sonarrUrl, apiKey: env.sonarrApiKey }
    : { baseUrl: env.radarrUrl, apiKey: env.radarrApiKey };
}

async function arrFetch<T>(service: ArrService, path: string, init?: RequestInit): Promise<T> {
  const { baseUrl, apiKey } = configFor(service);
  const res = await fetch(`${baseUrl}${path}`, {
    ...init,
    headers: { "X-Api-Key": apiKey, ...(init?.headers ?? {}) },
    cache: "no-store",
  });
  if (!res.ok) {
    throw new Error(`${service} ${path} failed: ${res.status} ${res.statusText}`);
  }
  // Some endpoints (e.g. Radarr's DELETE) return 200 with an empty body
  // rather than 204 — checking status alone isn't reliable, so check the
  // actual body content instead.
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

export interface HealthIssue {
  source: string;
  type: "ok" | "notice" | "warning" | "error";
  message: string;
  wikiUrl?: string;
}

export interface SystemStatus {
  version: string;
}

export interface QueueRecord {
  id: number;
  title: string;
  size: number;
  sizeleft: number;
  status: string;
  trackedDownloadStatus?: string;
  timeleft?: string;
}

export interface QueueSummary {
  totalRecords: number;
  records: QueueRecord[];
}

export interface Indexer {
  id: number;
  name: string;
  enableRss: boolean;
  protocol: string;
}

export interface RootFolder {
  id: number;
  path: string;
  accessible: boolean;
  freeSpace: number;
}

export interface SonarrSeries {
  id: number;
  title: string;
  monitored: boolean;
  rootFolderPath: string;
  added: string;
  statistics?: { sizeOnDisk: number; episodeFileCount: number };
}

export interface MediaInfo {
  videoCodec?: string;
  resolution?: string;
  videoBitDepth?: number;
}

export interface MovieFile {
  size: number;
  path: string;
  mediaInfo?: MediaInfo;
}

export interface RadarrMovie {
  id: number;
  title: string;
  monitored: boolean;
  rootFolderPath: string;
  added: string;
  sizeOnDisk: number;
  hasFile: boolean;
  tmdbId: number;
  movieFile?: MovieFile;
}

export interface ArrImage {
  coverType: string;
  remoteUrl?: string;
}

export interface ArrRatingSource {
  votes: number;
  value: number;
}

export interface SonarrSeriesDetail extends SonarrSeries {
  overview?: string;
  year?: number;
  genres?: string[];
  runtime?: number;
  certification?: string;
  status?: string;
  network?: string;
  images?: ArrImage[];
  ratings?: ArrRatingSource;
}

export interface RadarrMovieDetail extends RadarrMovie {
  overview?: string;
  year?: number;
  genres?: string[];
  runtime?: number;
  certification?: string;
  status?: string;
  studio?: string;
  images?: ArrImage[];
  ratings?: Record<string, ArrRatingSource & { type?: string }>;
}

export interface SonarrSeason {
  seasonNumber: number;
  monitored: boolean;
  statistics?: {
    episodeFileCount: number;
    episodeCount: number;
    totalEpisodeCount: number;
    sizeOnDisk: number;
  };
}

export interface SonarrSeriesFull extends SonarrSeries {
  seasons: SonarrSeason[];
}

export interface EpisodeFile {
  id: number;
  seriesId: number;
  seasonNumber: number;
  size: number;
  path: string;
  mediaInfo?: MediaInfo;
}

// Episode number/title live on Sonarr's separate `episode` resource, not
// `episodefile` — a file is linked back to its episode(s) via
// `episodeFileId` (plural for multi-episode files, e.g. an aired double
// episode sharing one file).
export interface Episode {
  id: number;
  seriesId: number;
  episodeFileId: number;
  seasonNumber: number;
  episodeNumber: number;
  title: string;
}

export interface QualityProfile {
  id: number;
  name: string;
}

// Lookup results are near-identical to the full resource shape, minus an id
// (null until added) — passed through mostly as-is when creating, so a loose
// record type is more honest than re-declaring every field.
export type LookupResult = Record<string, unknown> & {
  // Omitted entirely (not `null`) on lookup results not yet in the library.
  id?: number | null;
  title: string;
  year: number;
  overview?: string;
  images?: ArrImage[];
  tvdbId?: number;
  tmdbId?: number;
  network?: string;
  studio?: string;
};

export function getQualityProfiles(service: ArrService) {
  return arrFetch<QualityProfile[]>(service, "/api/v3/qualityprofile");
}

export function lookupSeries(term: string) {
  return arrFetch<LookupResult[]>(
    "sonarr",
    `/api/v3/series/lookup?term=${encodeURIComponent(term)}`
  );
}

export function lookupSeriesByTvdbId(tvdbId: number) {
  return arrFetch<LookupResult[]>(
    "sonarr",
    `/api/v3/series/lookup?term=${encodeURIComponent(`tvdb:${tvdbId}`)}`
  );
}

export function lookupMovies(term: string) {
  return arrFetch<LookupResult[]>(
    "radarr",
    `/api/v3/movie/lookup?term=${encodeURIComponent(term)}`
  );
}

export function lookupMovieByTmdbId(tmdbId: number) {
  return arrFetch<LookupResult>("radarr", `/api/v3/movie/lookup/tmdb?tmdbId=${tmdbId}`);
}

export function addSeries(payload: Record<string, unknown>) {
  return arrFetch<LookupResult>("sonarr", "/api/v3/series", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

export function addMovie(payload: Record<string, unknown>) {
  return arrFetch<LookupResult>("radarr", "/api/v3/movie", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

export function getSystemStatus(service: ArrService) {
  return arrFetch<SystemStatus>(service, "/api/v3/system/status");
}

export function getHealth(service: ArrService) {
  return arrFetch<HealthIssue[]>(service, "/api/v3/health");
}

export async function getQueueSummary(service: ArrService): Promise<QueueSummary> {
  const data = await arrFetch<{ totalRecords: number; records: QueueRecord[] }>(
    service,
    "/api/v3/queue?page=1&pageSize=25"
  );
  return { totalRecords: data.totalRecords, records: data.records };
}

export function getRootFolders(service: ArrService) {
  return arrFetch<RootFolder[]>(service, "/api/v3/rootfolder");
}

export function getIndexers(service: ArrService) {
  return arrFetch<Indexer[]>(service, "/api/v3/indexer");
}

export function getSeries() {
  return arrFetch<SonarrSeries[]>("sonarr", "/api/v3/series");
}

export function getMovies() {
  return arrFetch<RadarrMovie[]>("radarr", "/api/v3/movie");
}

export function deleteSeries(id: number) {
  return arrFetch<void>(
    "sonarr",
    `/api/v3/series/${id}?deleteFiles=true&addImportListExclusion=false`,
    { method: "DELETE" }
  );
}

export function deleteMovie(id: number) {
  return arrFetch<void>(
    "radarr",
    `/api/v3/movie/${id}?deleteFiles=true&addImportListExclusion=false`,
    { method: "DELETE" }
  );
}

export function getSeriesById(id: number) {
  return arrFetch<SonarrSeriesFull>("sonarr", `/api/v3/series/${id}`);
}

export function getSeriesDetail(id: number) {
  return arrFetch<SonarrSeriesDetail>("sonarr", `/api/v3/series/${id}`);
}

export function getMovieDetail(id: number) {
  return arrFetch<RadarrMovieDetail>("radarr", `/api/v3/movie/${id}`);
}

export function getEpisodeFiles(seriesId: number) {
  return arrFetch<EpisodeFile[]>("sonarr", `/api/v3/episodefile?seriesId=${seriesId}`);
}

export function getEpisodes(seriesId: number) {
  return arrFetch<Episode[]>("sonarr", `/api/v3/episode?seriesId=${seriesId}`);
}

export function deleteEpisodeFile(id: number) {
  return arrFetch<void>("sonarr", `/api/v3/episodefile/${id}`, {
    method: "DELETE",
  });
}

export function updateSeries(series: SonarrSeriesFull) {
  return arrFetch<SonarrSeriesFull>("sonarr", `/api/v3/series/${series.id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(series),
  });
}

// Triggered after an in-place file conversion so Radarr/Sonarr's own DB
// (size, mediaInfo, codec) picks up the new file instead of going stale —
// otherwise the next codec-analysis run would still show the old codec.
export function rescanMovie(movieId: number) {
  return arrFetch<unknown>("radarr", "/api/v3/command", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "RescanMovie", movieId }),
  });
}

export function rescanSeries(seriesId: number) {
  return arrFetch<unknown>("sonarr", "/api/v3/command", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "RescanSeries", seriesId }),
  });
}
