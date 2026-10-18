# Wheelhouse — open-source spec, Phases 0–1

Status: draft. Scope: everything needed before auth/DB/admin work (Phase 2)
can start. Decisions already made: GPL-3.0, torrents supported from day one,
auth = local users + optional local-network bypass (Phase 2), name =
Wheelhouse.

## Goals

1. Providers are swappable. Plex↔Jellyfin, SABnzbd↔qBittorrent, and multiple
   instances of the same thing (HD + 4K Radarr) all work without touching UI
   code.
2. Features are modular. A feature (Dashboard, Explore, Library, Codecs)
   declares what it needs; the nav shows only what the current setup can run.
3. Existing behavior is preserved. Phase 1 is a refactor plus two new
   adapters (qBittorrent, Jellyfin). The current env-var config keeps working.

Non-goals for Phases 0–1: auth, database, admin UI, setup wizard, Docker
publishing, Lidarr/Readarr, TVDB, i18n.

## Scope statement (put in the README)

Wheelhouse is a companion for the *arr stack, not a backend-agnostic media
manager. The media-manager role is Sonarr/Radarr-family only. Everything else
(download client, media server, metadata) is swappable.

## Positioning

Several tools overlap with parts of Wheelhouse. It is a deliberate mix of
three things (a dashboard, library management, and codec-aware conversion),
and each piece should be honest about where a specialist does it better:

- **Dashboards and request tools** (Homarr, Overseerr/Jellyseerr and similar)
  cover browsing, requesting and status. Wheelhouse's differentiator is
  that status, library management and conversion sit in one place and share
  the *arr apps' own data.
- **Tdarr** is the better tool for *unattended bulk* transcoding: a server plus
  worker nodes, plugin and node-graph "Flow" pipelines, persistent queues,
  health checks. Wheelhouse does not try to compete there. What Wheelhouse
  adds is the **decision step**: it works out from Radarr/Sonarr's own media
  info which files are worth converting for your preferred codec per
  resolution tier, shows them to you, and converts only what you approve,
  with a backup and an *arr rescan afterwards.

Consequences:

- The built-in converter stays small on purpose: serial, one host, video
  codec only. No distributed workers, flows or audio/subtitle pipelines.
  Safety work (the hardlink check below, queue persistence) is in scope;
  feature breadth is not.
- Heavy lifting can be delegated: see "Phase 3" for Tdarr as an optional
  conversion backend.
- The README should say plainly that Tdarr is the right tool for automation
  and that the two work well together.

## Phase 0 — prep

- [x] New repo from the existing app only, fresh history.
- [x] Rename package, UI title, container name and systemd unit to
      `wheelhouse`.
- [x] `LICENSE` (GPL-3.0); TMDB attribution in the README and Explore UI;
      "not affiliated with" disclaimer in the README.
- [x] Remove host-specific assumptions (default hostname, host paths in docs
      and the example systemd unit, host-specific code comments).
- [x] `SECURITY.md`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md` (DCO sign-off),
      issue and PR templates.
- [x] Example Compose file and Podman unit; `.dockerignore`;
      `HOSTNAME=0.0.0.0` in the image.
- [ ] TMDB logo in the Explore UI (text attribution is in place).
- [ ] Enable GitHub private vulnerability reporting on the repo (referenced
      by `SECURITY.md` and `CODE_OF_CONDUCT.md`).

Known leftovers for Phase 1/2: the convert feature's allowed roots
(`MEDIA_ROOTS = ["/movies", "/tv"]` in `lib/convert-queue.ts`) are hardcoded,
and the dashboard's "open service" links assume each service runs on the same
host at its default port. Both become configuration.

## Phase 1 — provider and feature layers

### Directory layout

```
lib/
  providers/
    core/
      types.ts          # Role, ProviderDefinition, Instance, ConnectionResult
      registry.ts       # static list of all ProviderDefinitions
      instances.ts      # getInstances(role), getInstance(id)
      http.ts           # providerFetch (timeout, redaction, URL base, TLS opt)
      config-source.ts  # ConfigSource interface + EnvConfigSource
    download-client/    # types.ts, sabnzbd.ts, qbittorrent.ts
    media-manager/      # types.ts, arr-http.ts, sonarr.ts, radarr.ts
    media-server/       # types.ts, plex.ts, jellyfin.ts
    metadata/           # tmdb.ts (no interface yet — see below)
  features/
    registry.ts         # FeatureDefinition list + getAvailableFeatures()
```

Existing `lib/arr-client.ts`, `sabnzbd-client.ts`, `plex-client.ts` are moved,
not rewritten: their HTTP calls become the internals of the adapters.

### Core types

```ts
type Role = "downloadClient" | "mediaManager" | "mediaServer" | "metadata";

interface ProviderDefinition<TConfig, TClient, TCaps> {
  id: string;                      // "sabnzbd", "qbittorrent", "plex", ...
  role: Role;
  displayName: string;
  configSchema: ZodType<TConfig>;  // also drives the Phase 2 admin form
  capabilities: TCaps;             // static, per provider
  create(config: TConfig, ctx: ProviderContext): TClient;
}

interface Instance {
  id: string;           // stable, URL-safe: "sonarr", "radarr-4k"
  providerId: string;
  name: string;         // display name
  enabled: boolean;
  config: unknown;      // validated against the provider's configSchema
}

interface ConnectionResult {
  ok: boolean;
  version?: string;
  error?: string;       // already redacted
}
```

Every client has `testConnection(): Promise<ConnectionResult>`. This is what
the Phase 2 "Test connection" button and the setup wizard call.

Instance ids are in place from day one so multi-instance never needs a
breaking migration. The env source produces ids `sonarr`, `radarr`, `sabnzbd`,
`plex`, `tmdb`, which keeps `/api/library/[service]` working unchanged until
the route param is renamed to `[instanceId]`.

### Shared HTTP helper (`providerFetch`)

Today's `fetch` calls have no timeout, so one dead service can hang
`/api/status`. The helper adds:

- Default 8s timeout via `AbortSignal.timeout`, overridable per call.
- URL-base support (`http://host:8989/sonarr`) and trailing-slash
  normalization.
- Optional `allowInsecureTls` per instance (self-signed certs are common).
- Error redaction: API keys and tokens are stripped from URLs and messages
  before they reach logs or API responses (SABnzbd puts `apikey` in the query
  string).
- A `User-Agent: Wheelhouse/<version>`.

### Response caching

Measured on a real instance (Radarr with 659 movies): `/api/storage`,
`/api/library`, Explore and codec analysis each take about 0.55-0.6 s, and
almost all of that is waiting on Radarr/Sonarr, because every request
refetches the full movie or series list. The app itself uses 77 MB of memory
and answers `/api/status` in about 60 ms. The fix is a cache, not a different
language.

The HTTP client gets an optional response cache, used by the media-manager
adapters first (PR 5):

- **GET only**, keyed by instance + path + query, with a short TTL per call
  (for example 5-10 s for library lists; none for status and queue data).
- **In-flight de-duplication**: concurrent requests for the same key share one
  upstream call, so a page that loads three endpoints that each want the
  library makes one request to Radarr, not three.
- **Invalidation on writes**: add, delete and rescan clear that instance's
  entries, so the UI never shows a deleted movie because of the cache.
- Errors are never cached.
- Per-instance and in memory. No shared cache and no persistence.

Acceptance: a second `/api/library` request inside the TTL makes no upstream
call (test against the counting local server used for the HTTP helper), the
first request after a delete does, and warm `/api/library` on the reference
instance drops from about 0.6 s to well under 100 ms.

### Config source

```ts
interface ConfigSource {
  getInstances(): Promise<Instance[]>;
}
```

Phase 1 ships `EnvConfigSource`, built from the current variables
(`SONARR_URL`, `SONARR_API_KEY`, …). New optional variables: `QBITTORRENT_URL`,
`QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD`, `JELLYFIN_URL`,
`JELLYFIN_API_KEY`. A provider is an instance only if its required variables
are set; currently-required Sonarr/Radarr/SABnzbd become optional. Phase 2
adds `DbConfigSource` behind the same interface, with env still overriding.

### Role: DownloadClient

```ts
interface DownloadClient {
  testConnection(): Promise<ConnectionResult>;
  getStatus(): Promise<DownloadClientStatus>;
  listItems(): Promise<DownloadItem[]>;
}

interface DownloadClientCaps {
  protocol: "usenet" | "torrent";
  canPause: boolean;
  usenetServers: boolean;   // SABnzbd NNTP server list
  seeding: boolean;
}

interface DownloadClientStatus {
  paused: boolean;
  speedBytesPerSec: number;     // current download speed
  queueCount: number;
  version?: string;
  warnings: string[];
  usenet?: { servers: { name: string; connections: number; ssl: boolean; enabled: boolean }[] };
  torrent?: { seeding: number; uploadBytesPerSec: number };
}

interface DownloadItem {
  id: string;
  name: string;
  protocol: "usenet" | "torrent";
  state: "queued" | "downloading" | "stalled" | "postprocessing"
       | "seeding" | "paused" | "completed" | "failed";
  stateDetail?: string;         // "Extracting", "Checking", "Moving"
  progress: number;             // 0..1
  sizeBytes: number;
  remainingBytes: number;
  etaSeconds: number | null;
  torrent?: { ratio: number; seeders?: number; uploadBytesPerSec: number };
}
```

Sizes are bytes everywhere. SABnzbd reports MB strings, which the adapter
converts; the UI no longer does unit juggling. The existing SABnzbd
post-processing correction (queue status collapses to "Downloading", real
status only in history) moves inside the SABnzbd adapter and surfaces as
`state: "postprocessing"` + `stateDetail`.

`/api/downloads` aggregates all download-client instances and tags each item
with `instanceId`. The UI shows a protocol badge and seeding rows.

**qBittorrent adapter notes**
- Cookie auth: `POST /api/v2/auth/login`, keep the `SID` cookie, re-login once
  on 403.
- State mapping (v4 and v5 differ — v5 renamed `pausedDL/pausedUP` to
  `stoppedDL/stoppedUP`; accept both):

  | qBittorrent state | state | stateDetail |
  |---|---|---|
  | `downloading`, `forcedDL`, `forcedMetaDL` | downloading | |
  | `metaDL` | downloading | Fetching metadata |
  | `stalledDL` | stalled | |
  | `queuedDL`, `queuedUP` | queued | |
  | `checkingDL`, `checkingUP`, `checkingResumeData` | postprocessing | Checking |
  | `moving` | postprocessing | Moving |
  | `uploading`, `forcedUP`, `stalledUP` | seeding | |
  | `pausedDL`, `stoppedDL` | paused | |
  | `pausedUP`, `stoppedUP` | completed | |
  | `error`, `missingFiles` | failed | |

- `eta == 8640000` means infinity → `etaSeconds: null`.
- Status from `/api/v2/transfer/info` plus the torrent list.

### Role: MediaManager

Decision: the media-manager role is Sonarr/Radarr-family only, so its types
stay arr-shaped (`RadarrMovie`, `SonarrSeries`, …). Nothing is gained by
inventing a neutral `Movie` type for a single API family. What changes is
that it becomes instance-based and kind-aware:

```ts
type ManagerKind = "movies" | "series";   // "music" | "books" later

interface MediaManager {
  kind: ManagerKind;
  testConnection(): Promise<ConnectionResult>;
  getStatus(): Promise<{ version: string; health: HealthIssue[]; queueCount: number; indexers: IndexerSummary }>;
  getRootFolders(): Promise<RootFolder[]>;
  getQualityProfiles(): Promise<QualityProfile[]>;
  // + the existing library, lookup, add, delete, rescan methods
}
```

`ArrService = "sonarr" | "radarr"` is replaced with instance ids. Anything
that hardcodes `"sonarr"` or `"radarr"` (status route, storage route, explore's
`getMovies()`) iterates instances of the right kind instead. Explore's
"already in library" check merges `getMovies()` across all movie instances.

Fix while porting `/api/storage`: it assigns the whole library's `sizeOnDisk`
to *every* root folder entry (`libraryUsedBytes: tvUsed`), which double-counts
with more than one root folder or instance. Attribute usage per root by path
prefix.

### Role: MediaServer

```ts
interface MediaServer {
  testConnection(): Promise<ConnectionResult>;
  getStatus(): Promise<MediaServerStatus>;
}

interface MediaServerStatus {
  state: "ok" | "needs-setup" | "down";   // needs-setup: unclaimed Plex, missing token
  name?: string;
  version?: string;
  activeSessions?: number;
  error?: string;
}
```

Plex maps its `claimed`/`tokenConfigured` logic onto `state`. Jellyfin uses
`/System/Info/Public` (unauthenticated) and, with an API key, `/System/Info`
and `/Sessions`. Library refresh after a codec conversion is the obvious next
method, but it is deferred to Phase 3 so Phase 1 doesn't grow past
status-only.

### Role: Metadata

TMDB stays a single module (`metadata/tmdb.ts`) with no interface. A second
provider doesn't exist yet, and Explore's categories (now playing, physical
releases) are TMDB-specific, so an interface now would be invented. It still
registers as a provider so it appears in config, `testConnection`, and
feature gating. Revisit when TVDB is on the table.

### Feature registry

```ts
interface FeatureDefinition {
  id: "dashboard" | "explore" | "library" | "codecs";
  label: string;
  route: string;
  requires: Requirement[];   // e.g. { role: "mediaManager", kind: "movies" }
}

getAvailableFeatures(instances): { feature: FeatureDefinition; available: boolean; missing: Requirement[] }[]
```

| Feature | Requires |
|---|---|
| dashboard | nothing (shows whatever is configured) |
| explore | `metadata` + a `mediaManager` of kind `movies` |
| library | any `mediaManager` |
| codecs | any `mediaManager` + ffmpeg present at startup |

The layout nav is built from this. Unavailable features hide, or show a
"needs X configured" stub, instead of the ad-hoc "TMDB not configured"
handling. Phase 2 adds the admin toggle on top (`enabled` flag per feature).

### `/api/status` shape

Replace the fixed `{ sonarr, radarr, sabnzbd, plex }` with:

```ts
{ instances: Array<{
    id: string; providerId: string; role: Role; name: string;
    state: "ok" | "degraded" | "down" | "needs-setup";
    version?: string; error?: string;
    detail: MediaManagerStatus | DownloadClientStatus | MediaServerStatus;
}> }
```

The dashboard renders one card per instance, grouped by role. This is a
breaking API change, which is fine pre-release; the only consumer is our UI.

### Torrent-specific issue for the converter

The codec converter rewrites library files. With torrents, a library file is
often a hardlink of the file the client is seeding. Replacing it by rename
creates a new inode in the library and leaves the original for seeding (safe,
but disk use doubles). Converting in place would break the seed. Phase 1
minimum: before converting, `stat` the file; if `nlink > 1`, or the download
client reports the path as seeding, show a confirm explaining the extra disk
use. A `listSeedingPaths()` capability on `DownloadClient` is the longer-term
fix and can wait for Phase 3.

### Testing

- Vitest, ESLint, Prettier added in the first PR.
- `__fixtures__/` per provider with sanitized real responses.
- Contract suites, parameterized over adapters:
  `describeDownloadClientContract(createAdapter)` asserts normalization
  invariants (bytes not MB, `progress` in 0..1, valid `state`,
  `etaSeconds` null for infinity). Same for MediaServer. A new adapter passes
  the suite or it doesn't merge.
- `scripts/mock-servers/`: small HTTP servers replaying fixtures. These double
  as the contributor dev environment and the future demo mode, so nobody needs
  a real *arr stack to work on Wheelhouse.
- CI (GitHub Actions): typecheck, lint, test, `next build`.

### PR sequence

Each PR keeps the app working.

1. **Tooling**: Vitest, ESLint, Prettier, CI workflow. (`zod` arrives in PR 2,
   where it's first used.)
2. **Provider core**: types, registry, `providerFetch`, `EnvConfigSource`. No
   behavior change yet.
3. **SABnzbd → DownloadClient**: adapter, `/api/downloads` aggregation, byte
   normalization, fixtures + contract tests.
4. **Plex → MediaServer**: adapter, `state` mapping, contract tests.
5. **Sonarr/Radarr → MediaManager instances**: `[service]` → `[instanceId]`,
   iterate instances in status/storage/explore, fix storage double-count, and
   add the response cache (see "Response caching").
6. **`/api/status` + dashboard**: instance-based cards.
7. **qBittorrent adapter**: fixtures, state mapping, UI protocol badge and
   seeding rows, hardlink check in the converter.
8. **Jellyfin adapter**: proves the MediaServer interface with a second
   implementation.
9. **Feature registry + nav gating**.

PRs 3–6 can't be skipped past: after PR 5, the first non-default instance
(e.g. `radarr-4k`) has to work end to end, and that is the acceptance test
for the multi-instance design.

### Acceptance criteria for Phase 1

- With only the old env vars set, the app behaves as it does today.
- Setting `QBITTORRENT_*` shows qBittorrent downloads, including seeding,
  alongside or instead of SABnzbd.
- Setting `JELLYFIN_*` instead of `PLEX_*` shows a Jellyfin card.
- Two Radarr instances both appear in status, storage, and library, and
  Explore's "In library" badge reflects both.
- With no Sonarr/Radarr configured at all, the app boots, Dashboard works,
  and Explore/Library/Codecs are hidden or stubbed.
- No hang longer than the timeout when a service is down.
- No API key or token appears in any API response or log line.

## Phase 3 (sketch) — Tdarr as a conversion backend

Experimental, and not started. This adds a fifth role, `converter`, with the
built-in ffmpeg engine as the default and Tdarr as an optional alternative.
Wheelhouse keeps the analysis and the approval step; Tdarr does the encoding.

The `converter` interface is extracted from the built-in engine only once the
second engine is being written (the rule of two, as with download clients).
Nothing in Phase 1 should anticipate it.

### What Tdarr's API offers

Tdarr has an HTTP API under `/api/v2/`, but it is **not officially documented**
(docs.tdarr.io has no API reference). What is known comes from the maintainer's
replies in GitHub issues and from community clients, so it can change between
Tdarr versions. Endpoints seen in practice:

| Need | Endpoint |
|---|---|
| Queue specific files | `POST /api/v2/scan-files` with `{"data": {"scanConfig": {"dbID": "<library id>", "arrayOrPath": ["/path/file.mkv"], "mode": "scanFolderWatcher"}}}` (the maintainer's recommended way to queue individual files, Tdarr issue #577) |
| Progress | nodes endpoint: each worker's current `file`, `percentage`, `ETA`, `status` |
| File outcome | `POST /api/v2/search-db` / `cruddb` on the file database (`transcodeDecisionMaker`, `healthCheck`, ...) |
| Control | cancel-worker-item, kill-worker, server log |
| Auth | optional `x-api-key` header; request bodies are wrapped in a `data` object |

### Limits that shape the design

1. **No per-job parameters.** Tdarr decides *how* to convert from the
   library's flow or plugin stack, so Wheelhouse cannot send "HEVC, quality
   20". Target codecs are expressed by routing to a library whose flow does
   the right thing (for example one library per tier). Codec preferences
   become a mapping to Tdarr libraries, not ffmpeg arguments.
2. **No job id comes back** from `scan-files`. Wheelhouse correlates its
   request with workers and database rows by file path.
3. **Libraries are folder-bound**: the file must sit inside a library's
   folders, at the path Tdarr sees. This needs the path-mapping setting
   already listed under open questions.
4. **The `.bak` safety net does not carry over.** Tdarr replaces the original
   unless the flow keeps it. The confirm dialog must say so, and Wheelhouse
   cannot enforce it.
5. **API instability.** Endpoints have changed or returned invalid JSON
   between versions (Tdarr issues #1072, #1260). Mitigate with recorded
   fixtures and contract tests pinned to specific Tdarr versions, and label
   the integration experimental.
6. **Bursts of single-file scans can lock up Tdarr's UI** (Tdarr issue #1192).
   Batch paths into one `scan-files` call and throttle.
7. The torrent hardlink issue (above) applies unchanged: in-place
   replacement breaks hardlinks to seeding files.

### Minimum scope

- Config: URL, optional API key, a Tdarr library per resolution tier, and a
  path mapping between Wheelhouse's paths and Tdarr's.
- A "Send to Tdarr" action in place of "Convert" when that engine is
  selected; progress by polling nodes and `search-db`; when a file finishes,
  trigger the same Radarr/Sonarr rescan the built-in engine does.
- Out of scope: creating or managing Tdarr libraries and flows. The user sets
  those up in Tdarr.

### First step: a spike

Before committing, run Tdarr locally in Docker, create a library, and
exercise `scan-files` (single file and a batch), `search-db` and the nodes
endpoint. Record the real request and response shapes as fixtures. Confirm the
free tier covers what we would rely on (sources disagree on which features
need Pro). The result decides go or no-go.

## Open questions

- **Plex token acquisition.** A pasted token works for Phase 1. A PIN-based
  link flow is nicer for the Phase 2 wizard.
- **Prowlarr.** Indexer info currently comes from each *arr's health data.
  A first-class IndexerManager role could come later.
- **Path mapping.** The converter assumes Wheelhouse sees the same paths the
  *arr reports. A remote-path-mapping setting belongs to Phase 2 config.
- **Other torrent/usenet clients.** Transmission, Deluge, NZBGet should be
  straightforward once the qBittorrent and SABnzbd adapters set the pattern;
  community PRs.
- **HDR and Dolby Vision in the built-in converter.** Checked in the code
  (`ffmpegArgsFor` in `lib/hw-encode.ts`): audio and subtitles are copied
  untouched (`-map 0 -c:a copy -c:s copy`), which is good. But every backend
  forces **8-bit 4:2:0** output (`format=nv12` for VAAPI, `-pix_fmt yuv420p`
  for NVENC and software), with no tone mapping and no HDR metadata handling.
  Converting an HDR10 or Dolby Vision source therefore produces an 8-bit file
  that still carries HDR colour tags (washed-out and banded), and Dolby Vision
  data is dropped. With the default preferences (UHD stays HEVC) HDR files are
  never candidates, so this only bites when a user sets the UHD preference to
  H.264, or converts a 1080p HDR file. Radarr/Sonarr already report
  `videoDynamicRange` and `videoDynamicRangeType` in `mediaInfo` (confirmed on
  a real Radarr: 6 of 659 files are HDR, 3 of them Dolby Vision), but the app
  does not read those fields yet. **Decision:** read them, and exclude HDR/DV
  files from conversion candidates (show them as "HDR, skipped") until proper
  tone-mapping or HDR-preserving encodes exist. Also note that 10-bit sources
  are deliberately reduced to 8-bit for playback compatibility; say so in the
  confirm dialog.
- **Queue persistence.** Job state is in memory, so a restart discards
  running and queued conversions. Decide whether to persist it (SQLite in
  Phase 2 makes this cheap).
- **Next.js versus a lighter stack (decide at the start of Phase 2).** Staying
  on TypeScript is the settled choice: the app is I/O-bound (ffmpeg does the
  heavy work as a separate process), uses 77 MB, and TypeScript gives shared
  client/server types and zod schemas that will drive the admin forms. The
  open question is the framework. The UI is almost entirely client-rendered
  with SWR and barely uses server rendering, while Next.js has cost us 23
  security advisories and the 14 to 15 breaking changes in a week. The
  alternative is a Vite single-page app with a small Node server (Hono or
  Fastify). Next's middleware and API routes make the auth work in Phase 2
  easier, so weigh that against the churn and attack surface before building
  auth on top of it. A rewrite to Go was considered and rejected (no
  measurable gain, large cost); revisit only if a no-runtime single binary
  becomes a goal.
