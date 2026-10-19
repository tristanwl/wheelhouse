// The JSON the Go server answers with (docs/architecture.md, server/README.md).
// The server is the source of truth; these are the shapes the UI relies on.

export type ManagerKind = "movies" | "series";
export type PreferableFamily = "h264" | "hevc";

// ---- /api/features

export type FeatureId = "dashboard" | "explore" | "library" | "codecs";

export interface FeaturesResponse {
  features: {
    id: FeatureId;
    label: string;
    route: string;
    available: boolean;
    /** Human-readable reasons, e.g. "a Radarr instance". */
    missing: string[];
  }[];
}

// ---- /api/status

export type InstanceState = "ok" | "degraded" | "needs-setup" | "down";

/** What the browser needs to build an "open this service" link (the host it takes from its own address bar). */
export interface ServiceLink {
  /** The configured URL's scheme, so an https service is not linked as http. */
  protocol: "http" | "https";
  port?: number;
  path: string;
}

export interface HealthIssue {
  source: string;
  type: "ok" | "notice" | "warning" | "error";
  message: string;
  wikiUrl?: string;
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

export interface MediaServerStatus {
  state: "ok" | "needs-setup";
  /** Something the user should know or do, in plain words. */
  notice?: string;
  name?: string;
  version?: string;
  /** Sessions actually playing something; undefined when the server can't say. */
  activeSessions?: number;
  /** Whether Wheelhouse is authenticated to the server (so session data is available). */
  authenticated: boolean;
}

interface StatusInstanceBase {
  id: string;
  providerId: string;
  name: string;
  state: InstanceState;
  version?: string;
  /** Why the instance is down. Redacted. */
  error?: string;
  /** Absent when the configured URL is missing or unusable. */
  link?: ServiceLink;
}

/** One configured instance and its normalized status; `detail` is absent while it is down. */
export type StatusInstance = StatusInstanceBase &
  (
    | { role: "mediaManager"; detail?: MediaManagerStatus }
    | { role: "downloadClient"; detail?: DownloadClientStatus }
    | { role: "mediaServer"; detail?: MediaServerStatus }
  );

export interface StatusResponse {
  instances: StatusInstance[];
}

// ---- /api/downloads

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
  /** The client's own wording where it adds information: "Extracting", "Checking". */
  stateDetail?: string;
  /** 0..1 */
  progress: number;
  sizeBytes: number;
  remainingBytes: number;
  /** null when unknown (stalled, paused, or the client doesn't say). */
  etaSeconds: number | null;
  torrent?: { ratio: number; seeders?: number; uploadBytesPerSec: number };
}

export interface DownloadsInstance {
  instanceId: string;
  name: string;
  protocol?: DownloadProtocol;
  up: boolean;
  paused?: boolean;
  speedBytesPerSec?: number;
  error?: string;
}

export type DownloadRow = DownloadItem & { instanceId: string };

export interface DownloadsResponse {
  /** One entry per configured download client, working or not. */
  instances: DownloadsInstance[];
  items: DownloadRow[];
}

// ---- library, storage, search, add

/** What routes tell the UI about each instance, so a broken one is visible and not just missing. */
export interface ManagerSummary {
  instanceId: string;
  name: string;
  kind: ManagerKind;
  up: boolean;
  error?: string;
}

export interface ManagerProblem {
  instanceId: string;
  name: string;
  error: string;
}

export interface LibraryItem {
  instanceId: string;
  instanceName: string;
  kind: ManagerKind;
  id: number;
  title: string;
  sizeOnDisk: number;
  monitored: boolean;
  rootFolderPath: string;
  added: string;
}

export interface LibraryResponse {
  items: LibraryItem[];
  /** Every configured media manager, so the UI can say which are down or missing. */
  instances: ManagerSummary[];
}

export interface StorageEntry {
  instanceId: string;
  label: string;
  path: string;
  freeBytes: number;
  libraryUsedBytes: number;
}

/** Library an instance holds outside every root folder it reports. */
export interface UnplacedStorage {
  instanceId: string;
  label: string;
  items: number;
  bytes: number;
}

export interface StorageResponse {
  entries: StorageEntry[];
  unplaced?: UnplacedStorage[];
  instances: ManagerSummary[];
}

export interface SeasonSummary {
  seasonNumber: number;
  monitored: boolean;
  episodeFileCount: number;
  episodeCount: number;
  totalEpisodeCount: number;
  sizeOnDisk: number;
}

export interface ItemRating {
  source: string;
  value: number;
  votes: number;
}

export interface ItemDetails {
  title: string;
  overview: string | null;
  year: number | null;
  genres: string[];
  runtime: number | null;
  certification: string | null;
  status: string | null;
  network: string | null;
  studio: string | null;
  posterUrl: string | null;
  fanartUrl: string | null;
  ratings: ItemRating[];
}

export interface SearchHit {
  instanceId: string;
  instanceName: string;
  /** tvdbId (series) or tmdbId (movies): the stable external id used to add. */
  id: number;
  alreadyAdded: boolean;
  title: string;
  year: number;
  overview: string;
  posterUrl: string | null;
  /** Network (series) or studio (movies). */
  subtitle: string | null;
}

export interface AddMeta {
  instanceId: string;
  instanceName: string;
  qualityProfiles: { id: number; name: string }[];
  rootFolderPath: string | null;
  /** Every ready instance of this kind: the choices of the instance picker. */
  instances?: InstanceRef[];
}

export interface InstanceRef {
  instanceId: string;
  name: string;
}

// ---- explore

export interface ExploreHit {
  /** tmdbId */
  id: number;
  alreadyAdded: boolean;
  /** The movie instances that have it. */
  libraryInstances?: InstanceRef[];
  title: string;
  year: number | null;
  overview: string;
  posterUrl: string | null;
  voteAverage: number;
}

export interface ExploreDetail {
  id: number;
  alreadyAdded: boolean;
  libraryInstances?: InstanceRef[];
  title: string;
  overview: string;
  year: number | null;
  genres: string[];
  runtime: number | null;
  voteAverage: number;
  posterUrl: string | null;
  backdropUrl: string | null;
  trailerKey: string | null;
}

// ---- codecs

export type CodecFamily = "h264" | "hevc" | "legacy";
export type Tier = "hd" | "uhd";
export type ConversionReason = "legacy-codec" | "codec-mismatch";

export interface CodecPreferences {
  hd: PreferableFamily;
  uhd: PreferableFamily;
}

export interface CodecFileInfo {
  title: string;
  codec: string;
  resolution: string | null;
  bitDepth: number | null;
  size: number;
  instanceId: string;
  itemId: number;
  filePath: string;
}

export interface CodecBucketSummary {
  count: number;
  totalSize: number;
  percentOfLibrary: number;
}

export interface ConversionCandidate extends CodecFileInfo {
  tier: Tier;
  /** The codec this file's tier prefers: what "Convert" targets. */
  targetFamily: PreferableFamily;
  reason: ConversionReason;
}

export interface MatchingItem extends CodecFileInfo {
  tier: Tier;
}

export interface CodecAnalysisResponse {
  totalCount: number;
  totalSize: number;
  preferences: CodecPreferences;
  buckets: Record<CodecFamily, CodecBucketSummary>;
  candidates: ConversionCandidate[];
  matching: MatchingItem[];
  /** Library items with no scanned file yet, left out of the analysis. */
  skippedCount: number;
  /** Instances that couldn't be read; the others are still analyzed. */
  problems?: ManagerProblem[];
}

export type Backend = "software" | "vaapi" | "nvenc";

export interface EncoderOption {
  backend: Backend;
  family: PreferableFamily;
  label: string;
  available: boolean;
  reason?: string;
}

export type ConversionStatus = "queued" | "probing" | "running" | "success" | "failed" | "canceled";

export interface ConversionJob {
  id: string;
  instanceId: string;
  itemId: number;
  filePath: string;
  title: string;
  originalSize: number;
  backend: Backend;
  targetFamily: PreferableFamily;
  status: ConversionStatus;
  progressPercent: number;
  error?: string;
  newSize?: number;
  backupPath?: string;
  log: string;
  createdAt: number;
  startedAt?: number;
  finishedAt?: number;
}
