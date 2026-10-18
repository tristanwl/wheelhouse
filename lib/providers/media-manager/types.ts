import type { ConnectionResult } from "../core/types";

// Media-manager types stay arr-shaped on purpose: the role is Sonarr/Radarr
// family only, so a neutral Movie/Series model would add nothing.

/** "music" | "books" would join these if Lidarr/Readarr ever land. */
export type ManagerKind = "movies" | "series";

/** Static, per provider. */
export interface MediaManagerCapabilities {
  kind: ManagerKind;
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

export interface IndexerSummary {
  total: number;
  enabled: number;
  /** Messages of the health issues that concern indexers. */
  issues: string[];
}

export interface MediaManagerStatus {
  version: string;
  health: HealthIssue[];
  queueCount: number;
  indexers: IndexerSummary;
}

export interface RootFolder {
  id: number;
  path: string;
  accessible: boolean;
  freeSpace: number;
}

export interface QualityProfile {
  id: number;
  name: string;
}

export interface SonarrSeries {
  id: number;
  title: string;
  monitored: boolean;
  path: string;
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
  path: string;
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
// `episodefile`: a file is linked back to its episode(s) via
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

// Lookup results are near-identical to the full resource shape, minus an id
// (null until added), passed through mostly as-is when creating, so a loose
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

/** What every Sonarr/Radarr-family client can do, whatever the kind. */
export interface MediaManagerBase {
  readonly kind: ManagerKind;
  testConnection(): Promise<ConnectionResult>;
  /** Version, health, queue length and indexers in one call. Throws a ProviderError on failure. */
  getStatus(): Promise<MediaManagerStatus>;
  getSystemStatus(): Promise<SystemStatus>;
  getHealth(): Promise<HealthIssue[]>;
  getQueueSummary(): Promise<QueueSummary>;
  getIndexers(): Promise<Indexer[]>;
  getRootFolders(): Promise<RootFolder[]>;
  getQualityProfiles(): Promise<QualityProfile[]>;
  /** Free-text search for things to add. */
  lookup(term: string): Promise<LookupResult[]>;
  /** By the id the add flow uses: tmdbId for movies, tvdbId for series. */
  lookupByExternalId(externalId: number): Promise<LookupResult | undefined>;
  /** Adds a library item; the payload is a lookup result plus the user's choices. */
  add(payload: Record<string, unknown>): Promise<LookupResult>;
  /** Removes an item and its files from disk. */
  remove(id: number): Promise<void>;
  /**
   * Asks the app to rescan an item's files. Called after an in-place file
   * conversion so its own DB (size, mediaInfo, codec) doesn't go stale;
   * otherwise the next codec analysis would still show the old codec.
   */
  rescan(id: number): Promise<void>;
}

export interface MovieManager extends MediaManagerBase {
  readonly kind: "movies";
  getMovies(): Promise<RadarrMovie[]>;
  getMovieDetail(id: number): Promise<RadarrMovieDetail>;
}

export interface SeriesManager extends MediaManagerBase {
  readonly kind: "series";
  getSeries(): Promise<SonarrSeries[]>;
  getSeriesById(id: number): Promise<SonarrSeriesFull>;
  getSeriesDetail(id: number): Promise<SonarrSeriesDetail>;
  getEpisodeFiles(seriesId: number): Promise<EpisodeFile[]>;
  getEpisodes(seriesId: number): Promise<Episode[]>;
  deleteEpisodeFile(id: number): Promise<void>;
  updateSeries(series: SonarrSeriesFull): Promise<SonarrSeriesFull>;
}

export type MediaManager = MovieManager | SeriesManager;

export type ManagerOfKind<K extends ManagerKind> = K extends "movies"
  ? MovieManager
  : SeriesManager;
