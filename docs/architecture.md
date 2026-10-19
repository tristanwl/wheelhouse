# Wheelhouse architecture

Wheelhouse is a self-hosted dashboard and companion for the *arr media stack
(Sonarr, Radarr, SABnzbd, qBittorrent, Plex or Jellyfin). It shows live service status and storage
usage per media volume, lets you browse and delete library items — including
deleting individual TV seasons rather than whole series — directly through
Sonarr/Radarr's own APIs, and includes a codec analysis view that recommends
which files are actually worth converting (based on your own preferred codec
per resolution tier) and can run the conversion itself via ffmpeg, using
hardware encoding when available.

The released 0.2.x has no authentication, so run it only on a trusted network.
The development branch enforces it on the server (accounts, sessions, login,
CSRF, throttling, an audit log; see "Accounts and sessions" below) while the web
UI has its login, setup and account pages (see "Web sign-in" below); the
admin pages and a database-backed configuration are still to come — see [wheelhouse-spec.md](./wheelhouse-spec.md),
[wheelhouse-spec-phase2.md](./wheelhouse-spec-phase2.md) and
[wheelhouse-phase2-plan.md](./wheelhouse-phase2-plan.md). Operating a server
(backups, the master key, recovery) is in [operations.md](./operations.md).

## Stack

One Go binary (`server/`, standard library plus the pure-Go SQLite driver) with the Vite single-page UI
(`web/`: React 19, React Router, SWR, Tailwind CSS) embedded in it, shipped in
one container image together with ffmpeg and the VA-API drivers. The server is a
thin read/aggregate/proxy layer over the services' own APIs. Its state is a
SQLite database in `/config` (accounts, sessions and the audit log; stored
configuration follows), `settings.json` in `/config`, the master key
(`/config/secret.key`); the conversion queue and converter settings are in the database. The
server opens the database and the key at start and refuses to run without them;
see "Accounts and sessions" and "Storage and secrets" below. What is protected
and from whom is in the [threat model](threat-model.md).

Since release 0.3.0 this is the whole product. Releases up to 0.1.x were a
Next.js app with the same URLs, API and environment variables; the migration is
described in the phase 2 spec. Comments in the Go code that mention `lib/...` or
`app/...` refer to that code, which is in git history (tag `0.1.1`).

```
cmd/wheelhouse        main: config, signals, listener; healthcheck, version, backup and rekey commands
internal/app          the router (every route states its access level), middleware chain, graceful Serve
internal/config       listen address, database path
internal/store        SQLite: open (modes, WAL, pools), embedded migrations, backup, secret-column rekey
internal/secrets      AES-256-GCM envelopes, master key loading, redacting key and secret types
internal/features     feature gating (what the nav shows)
internal/status       GET /api/status
internal/downloads    GET /api/downloads
internal/library      library, storage, search, add, details, seasons, deletes
internal/explore      GET /api/explore and /api/explore/{id}
internal/codecs       codec analysis and the preferences in settings.json
internal/converter    ffmpeg queue (SQLite-backed), settings, encoder detection, path containment, HDR probe, hard-link check
internal/middleware   request ID, security headers, access log, panic recovery
internal/providers    core (config, runtime, HTTP client) and one package per provider
internal/ui           the embedded UI (the Vite build, or a placeholder page)
internal/version      the release version, stamped in at build time
```

See [server/README.md](../server/README.md) for the server and
[web/README.md](../web/README.md) for the UI.

### Wiring (`server/internal/app`, `providers/all`)

- **One registry.** `providers/all` builds the registry from every provider's
  `Definition()` and a runtime over a `ConfigSource`; `app.Options{Source}` (or a
  ready `Runtime`) injects it, `main` passes the process environment, tests pass
  a fake one. Routes never build a service client themselves: they ask the
  runtime for the ready instances of a role and iterate them, so every route
  works with several instances (Radarr and Radarr 4K) and with none.
- **Access levels.** Every route is registered through `app.Router.Handle(pattern,
  level, handler)` with `Public`, `Authenticated` or `Admin`, and every request
  passes the `Guard` (`guard.go`), which enforces it from the session: 401 JSON for
  no or an invalid session, 403 for a user on an admin route, 503 `setup required`
  while no account exists. `router_test.go` lists every route with its level in
  one table, and fails for a route that is missing or not listed, or that builds
  its own mux; a second test keeps that table equal to `acceptance/routes.ts`,
  whose enumerating tests then check the behaviour over HTTP on the real binary
  and image. Public: `/healthz`, the setup status and submit, login, the UI and
  the JSON 404s. Everything the app shows (features, status, downloads, library
  including adds and deletes, storage, search, Explore, codec analysis and
  conversions) and the own account is `Authenticated`; saving codec settings, the
  accounts and the audit log are `Admin`.
- **Everything else is the UI.** Any non-`/api` path that is not a file gets
  `index.html` (client-side routing); an unknown `/api/...` path is a JSON 404.
- **Listening.** `PORT` (default 3000; the image sets 3050) and, to choose the
  interface, `WHEELHOUSE_LISTEN` (`host:port`). `HOSTNAME` is never read:
  container runtimes set it to the container id (or the host's name under host
  networking), and the Next app needed `HOSTNAME=0.0.0.0` forced in the image
  for the opposite reason.
- **Shutdown.** SIGINT and SIGTERM stop the server gracefully (10 s for
  in-flight requests, running conversions are killed).

### Provider core (`server/internal/providers/core`)

- **Definitions and registry.** A `Definition` has an id, a `Role`
  (`downloadClient`, `mediaManager`, `mediaServer`, `metadata`), a display name,
  a `Config` struct (zero value) and a `Create` factory. `NewRegistry` rejects
  duplicate ids and malformed definitions.
- **Config.** A provider's settings are a struct with `json`, `label` and
  `config:"required,secret,url"` tags. The core validates raw settings against
  it (unknown keys ignored, empty strings rejected, optional `Validate()` hook)
  and builds a JSON Schema description of it (`Definition.Schema()`, with
  `writeOnly` and `x-secret` on credentials) for the future admin page.
  Validation errors name the field and the rule, never the value.
- **Config sources.** `ConfigSource.Instances(ctx)`. `EnvConfigSource` reads the
  environment variables (see Config below), including extra Sonarr and Radarr
  instances (`RADARR_4K_URL` gives `radarr-4k`). A database source comes in
  phase 2.
- **Runtime.** `ListInstances`, `Clients(role)` and `Client(id)` with four
  statuses (ready, disabled, unknown-provider, invalid-config). Clients are
  cached per instance and rebuilt when the validated config, provider or name
  changes. A configured URL with `user:pass@` is an invalid config.
- **HTTP client.** `NewHTTPClient`: 8 s timeout per request (overridable),
  path-prefix safe `JoinURL`, `Wheelhouse/<version>` user agent, redaction of
  every form of a configured secret (plain, URL-encoded, base64, JSON-escaped)
  plus `apikey=`-style parameters and `user:pass@` URLs, `ProviderError` with a
  status, GET cache with TTL, in-flight de-duplication and `Invalidate()`. At
  most 8 MiB is read from upstream by default (an endpoint sets its own cap:
  96 MiB for library and torrent lists, 2 MiB for status calls), the body is
  scanned before it is decoded (`core.DecodeJSON`: elements per array, values in
  total, nesting depth, string length, and a process-wide gate for large
  decodes, so a few bytes of `{}` cannot become gigabytes), redirects to another host (or from https to
  http) are refused, and TLS verification cannot be switched off. Upstream
  bodies and reason phrases never reach an error or a log line.
- **Adapters normalize.** Sizes in bytes, progress 0..1, durations in seconds,
  states from the shared enums. An unreachable service is a `ProviderError`
  (reported as down); a wrong key reads "API key incorrect".

## Conventions

Every modal (`ConfirmModal`, `SeriesModal`, `DetailModal`, `TmdbDetailModal`)
uses the shared `web/src/components/ModalCloseButton.tsx` — an × in the corner,
not just "click the backdrop to dismiss". On mobile a modal often fills the
whole viewport, leaving no backdrop to tap, so a dedicated close target
matters. Add it to any new modal. The Wheelhouse logo in the header links to
`/` (Dashboard).

The UI's CSP allows no inline script or style, and images only from
`image.tmdb.org` and `artworks.thetvdb.com`; a trailer opens on YouTube in a
new tab instead of an embedded frame. Library deletes send `?confirm=true`,
and only after the UI's own confirm dialog (`web/README.md`, "Security
constraints").

Parsers and validators of untrusted input (upstream JSON, redaction, URLs and
addresses, the forwarded-header walk, the Host allow-list, accounts, path
containment, ffmpeg arguments, the secrets envelope, the whole HTTP surface) are
covered by Go fuzz targets that assert invariants against independent oracles;
their seed corpora (`testdata/fuzz`, from the fakes' fixtures and the hostile
cases of the tests) run as ordinary tests, and `.github/workflows/fuzz.yml` runs
every target weekly. See [server/README.md](../server/README.md#fuzz-tests).

## Pages

- `/` — dashboard: one status card per configured instance, grouped under
  Media managers, Download clients and Media servers (so Radarr and Radarr 4K
  each get a card), driven by `/api/status`. A card shows its state dot (ok,
  degraded, needs setup, down), the role's details (queue size and indexer
  counts for Sonarr/Radarr; status, speed, queue and usenet servers for
  download clients; name, version and active sessions for media servers) and
  its warnings. With nothing configured the section points at the README's
  Configuration section. Warnings show severity and a relative age ("1h
  ago"), those older than 24 hours are hidden, and every piece of text on a
  card wraps so a long path can't overflow it. Every card links out to that
  service's own web UI. The server sends only the configured scheme, port and
  path (`link`, never host, credentials or query); the browser adds
  `window.location.hostname`, so the links work the same way you reached
  Wheelhouse (by IP, mDNS name, Tailscale, whatever) and the configured host
  may be a container-internal name. Below the cards is a storage bar per root
  folder; clicking one navigates to `/library?path=<rootFolderPath>`,
  pre-filtered to that volume. A Downloads section shows the download clients'
  queues live (protocol badge, name, state, progress bar, speed, ETA; ratio and
  upload speed for seeding torrents), polled every 5 s, faster than the 15 s
  status poll.
- `/library` — a searchable, sortable table of every series (Sonarr) and movie
  (Radarr): title, size on disk, age (time since added), monitored state.
  Sorted by size descending by default; the Title/Size/Age headers sort by that
  column (click again to flip). The table scrolls horizontally inside its own
  `overflow-x-auto` wrapper on mobile. A volume dropdown filters to a single
  root folder. Movies get a delete with confirm. Series get a "Manage" button
  opening a modal listing every season (episode count, size, monitored state)
  with per-season delete, plus "Delete entire series". Clicking a title opens a
  detail card — poster, year, runtime, certification, genres, ratings,
  overview — pulled live from Sonarr/Radarr's own metadata.
- `/explore` — movies-only browse-and-add: three horizontally-scrolling poster
  rows (In Theaters, Recent Blu-ray/Digital Releases, Popular) sourced from
  TMDB, since Sonarr/Radarr have no browse/trending API of their own (see
  "Explore"). `/explore/:category` is the poster grid behind a row's "All" link.
- `/codecs` — video codec breakdown of the library, with a Movies/TV toggle, a
  settings panel for your preferred codec per resolution tier (HD/UHD), and a
  "Convert" button per candidate that re-encodes the file in place via ffmpeg
  (see "Codec analysis"). This is a real destructive action, gated by an
  in-app confirm step.

The navigation only shows pages that can work with what is configured (see
"Feature registry"); opening an unavailable page directly shows a stub saying
what is missing.

### Add to library

`AddSearch.tsx` is rendered in the layout so it is on every page. TV
Show/Movie toggle, a search box hitting Sonarr's/Radarr's own lookup
(TheTVDB/TMDB — the same catalog their "Add New" screen searches), and inline
controls for monitored, search-now, and (movies only) minimum availability.
Each result shows poster/title/year/overview and either an "Add" button or an
"In library" badge. With more than one instance of a kind (say Radarr and
Radarr 4K), the search covers all of them and each hit is one instance's
answer, labeled with the instance name; Add posts to that instance, and its
quality profiles and root folder load when you click it.

With more than one ready movie instance, the Explore pages (`ExplorePage.tsx`,
`ExploreCategoryPage.tsx`) show an "Add to" picker (`InstancePicker.tsx`, hidden
with one instance). `useAddMeta` loads `GET /api/add-meta?kind=movies` (the
first instance that answers, plus the list of every ready one in `instances`)
and, when the user picks another, `?instanceId=`; the add dialog says which
instance it adds to, and `POST /api/add` carries that `instanceId`. The "In
library" badge names the instances that have the movie (`libraryInstances` in
the Explore answers); a movie that is only in another instance keeps its Add
button, with a note saying where it is (`web/src/lib/explore-library.ts`).

Quality profile is **not** an always-visible control. Clicking Add (here, on
Explore, or in the TMDB detail card) opens `AddQualityModal.tsx`, a small
confirm dialog (poster, title, a "Quality" select, Cancel/Add), and the actual
`POST /api/add` only fires once that is confirmed. The select defaults via
`pickDefaultQualityProfileId()` (`web/src/lib/quality.ts`): profile names are
arbitrary per-install strings, so it name-matches "HD - 720p/1080p" first,
then any name containing "HD" but not "Ultra", then the first profile.

Sonarr/Radarr's lookup response is the full resource shape a not-yet-added item
*would* have, plus an `id` field that is **omitted entirely** (not `id: null`)
when the item is not in the library. Detect "already added" by the field being
a number, never by `!== null`.

Adding re-fetches the exact item by external id right before create —
`GET /api/v3/series/lookup?term=tvdb:<id>` (Sonarr, an array) or
`GET /api/v3/movie/lookup/tmdb?tmdbId=<id>` (Radarr, the object) — then
overlays the user's choices (`qualityProfileId`, `rootFolderPath`, `monitored`,
`addOptions`) and `POST`s the whole object to `/api/v3/series` or
`/api/v3/movie`. This mirrors what each app's own web UI does; building the
create payload from scratch tends to miss required fields.

### Explore

Neither Sonarr nor Radarr has a native browse/trending endpoint. Radarr has
Theaters/Popular/Top/Upcoming only through its Import List mechanism (a
persistent auto-add config, not a browse-and-preview API) and no "Blu-ray"
category. So Explore goes straight to TMDB (`internal/explore`, needs
`TMDB_API_KEY`):

- **In Theaters** — `/movie/now_playing?region=US`.
- **Recent Blu-ray/Digital Releases** — TMDB has no "Blu-ray" concept; the
  closest equivalent is `with_release_type=5` (physical release) on
  `/discover/movie`, sorted `primary_release_date.desc` and capped at today's
  date so announced-but-unreleased physical dates don't show up as "recent".
- **Popular** — `/movie/popular`.

"Already in library" cross-references results against every Radarr instance's
`tmdbId` (one movie-list call per instance, then a set lookup); an instance
that cannot answer is left out of that check. Adding reuses `POST /api/add`
with the TMDB id, since Radarr's `/api/v3/movie/lookup/tmdb?tmdbId=` accepts
any valid TMDB movie id.

Each row is a horizontally-scrolling strip with left/right arrow buttons.
Clicking a poster or title (not the Add button) opens `TmdbDetailModal.tsx`:
backdrop banner, poster, runtime/rating/genres, overview, an Add button and a
"Watch Trailer" link (`GET /api/explore/{id}`, TMDB's
`/movie/{id}?append_to_response=videos`). TMDB movies often carry dozens of
promo videos; the server prefers an official video literally named "Official
Trailer", then official Trailer, any Trailer, Teaser, none; `trailerKey` is a
YouTube id or null and the UI links to YouTube.

The category grid appends 40 at a time (two TMDB pages) with a "Load more"
button while TMDB has pages (it caps at page 500). Popular re-ranks between
requests, so later batches can repeat a movie; the page de-dupes by tmdbId.

### Codec analysis

`GET /api/codec-analysis?kind=` (`internal/codecs`). Deliberately **not** a
"convert everything to one fixed codec" tool: the recommendation is driven by
**user-configurable preferences** (`GET`/`PUT /api/codec-analysis/settings`,
persisted to `/config/settings.json`):

- Every file is classified into a resolution **tier**, `hd` or `uhd` (the long
  edge in pixels against a 3000 px threshold, e.g. `3840x2076` is uhd), and each
  tier has a preferred codec family. Defaults: **HD → H.264** (broadest playback
  compatibility), **UHD → HEVC** (its compression advantage over H.264 is
  largest at 4K). Editable from a small settings panel on `/codecs`.
- **Legacy** (vc1, xvid, mpeg2, mpeg4, anything not H.264/HEVC) is always a
  conversion candidate, converting to whatever its tier prefers.
- **H.264/HEVC** is a candidate only if it does not already match its tier's
  preferred family. Files that already match land in a `matching` list.

Radarr/Sonarr's `mediaInfo.videoCodec` reports the *encoder* name, not a clean
family: `x264`, `h264`, `avc` are H.264; `x265`, `h265`, `hevc` are HEVC (case
varies). The classifier groups by a fixed name set per family rather than
trusting the raw string.

Movies: one `GET /api/v3/movie` already returns `movieFile.mediaInfo` inline.
TV has no bulk endpoint, so one `episodefile?seriesId=` call and one
`episode?seriesId=` call are made per series, in parallel. TV candidates are
labeled by episode (`Band of Brothers — S01E01 "Currahee"`): an episode file
only carries a season number, so episodes are matched to files by
`episodeFileId` (an array match, since an aired double episode can share a file,
`S03E07-E08`), falling back to a season-only label. Movies or files with no
`movieFile` or no scanned `mediaInfo` are excluded and counted in
`skippedCount`.

The "Copy list" button on the candidates table is a client-side clipboard copy
of a tab-separated dump; nothing about a conversion list is stored.

#### Convert

Each candidate row has a "Convert" button opening `ConvertModal.tsx`, a confirm
step (title, codec, size, the `.bak` safety net, and that conversions are
serialized) before anything runs. Confirming starts a real ffmpeg encode
against the file and polls for progress.

**The container must mount the media at the same paths the *arr apps use**
(`/movies` and `/tv` in the example [compose file](../docker-compose.example.yml)
and [systemd unit](../deploy/wheelhouse.service.example)). Only Convert needs
this; everything else talks to Sonarr/Radarr through their APIs. A file path in
a request must resolve inside a media root. The roots come from the database
(an administrator's `PUT /api/admin/converter`, validated on save), else
`WHEELHOUSE_MEDIA_ROOTS` (a read-only seed), else `/movies` and `/tv`; a change
applies at once, without a restart. The file path from
`movieFile.path` / `episodeFile.path` is then used as is, with no translation.

**Why ffmpeg and not HandBrakeCLI:** HandBrakeCLI does not install cleanly on
Alpine; ffmpeg does and covers every codec/backend combination needed (libx264
and libx265 in software, VAAPI, NVENC).

`converter.Args(backend, family, filePath, tempPath)` builds the command, one of
three shapes by backend (`software`, `vaapi`, `nvenc`) and target family (`h264`,
`hevc`). Software copies every stream untouched (`-map 0 -c copy`) and
overrides only the video codec (`libx264`/`libx265`, `-preset medium -crf 20`,
`-pix_fmt yuv420p` to force 8-bit output, since the point is playback
compatibility). VAAPI and NVENC cannot combine that with their own filter
setup, so their shapes differ; `-qp 20` (VAAPI) and `-cq 20 -preset p5` (NVENC)
are the usual practical equivalents of `-crf 20`. All keep the source's
container (the temp file reuses the original's extension) to avoid
subtitle/audio-vs-container mismatches. ffmpeg and ffprobe are run directly
(never through a shell) with arguments built from validated values.

**Job model.** The queue (`converter.JobQueue` behind the `converter.Queue`
interface) is kept in memory and written through to the `conversion_jobs` table,
so the job list survives a restart (recovery policy below). Each job records its
owner (the user id, `bypass` for the local-network bypass, or `cli`), the
acknowledgements the request carried, and its HDR class. Only one job runs at a time: transcoding is
CPU-heavy and the host usually runs Sonarr/Radarr/a media server live, so
further conversions queue. A file that already has an active job returns that
job instead of starting a duplicate (a second job would rename whatever is
*currently* at the path, possibly the first job's output, to `.bak` and clobber
the true original; and the backup itself is never overwritten, see File safety); the same rule lets the UI reattach to a job after the modal
is closed. Each candidate row polls the job list every 2 s and shows a live
progress bar. The modal shows the job's raw ffmpeg output (the last 8000 chars).
Progress comes from ffmpeg's `-progress pipe:1` (`out_time_ms` against the
duration probed up front with `ffprobe`).

**Hard links.** A file with more than one hard link may also be what a torrent
client is seeding. The convert route answers `409 {error, code: "hardlinked",
linkCount}` and starts nothing, unless the body has `acknowledgeHardlink: true`;
the modal explains that the conversion replaces the library file with a new one
(the seeding copy stays and disk use roughly doubles) and offers "Convert
anyway".

**Hardware encoding.** `converter.Detector` decides, per (backend, family)
pair, whether this box can really hardware-encode: VAAPI-H.264, VAAPI-HEVC,
NVENC-H.264 and NVENC-HEVC are probed independently, and so are the software
encoders (libx264 and libx265), and the six results are returned by `GET
/api/codec-analysis/encoders`. Without a working ffmpeg nothing is available,
software included, which agrees with `/api/features` marking Codecs unavailable.
Nothing is inferred from device presence or
`ffmpeg -encoders`, since both can be true while the path is broken (missing
driver, permissions, GPU too old). Each is confirmed with a real 1-second
throwaway encode, cached for the process's lifetime. The modal lists the
options for the family the candidate needs and defaults to the best available,
falling back to software. VAAPI uses `/dev/dri/renderD128`. A 4th-generation
(Haswell) Intel iGPU, for instance, reports VAAPI-H.264 available and
VAAPI-HEVC not (Haswell's Quick Sync has no hardware HEVC encode; that came
with Skylake), which is exactly what the probe finds. NVENC is untested on real
hardware.

Hardware encode needs a few things outside the code:

1. `--device /dev/dri` (`devices: [/dev/dri]` in compose), which passes the
   render node into the container.
2. A VA-API driver in the image (the Dockerfile): `mesa-va-gallium` (AMD and
   others), and on x86 `libva-intel-driver` (i965, up to Haswell and a few
   later generations) and `intel-media-driver` (iHD, Broadwell and newer).
   Neither Intel driver exists for arm64.
3. **Rootless Podman only: `--group-add keep-groups`**, together with
   `--userns=keep-id --user=%U:%G` in the example unit, which maps the
   container user to your host user. Rootless Podman does not pass the host
   user's supplementary groups into the container, so even a user in the
   host's `render`/`video` groups sees the device node as `nobody:nobody` and
   ffmpeg fails with "No VA display found for device". Docker users use
   `group_add` with the render group's numeric GID (`getent group render`).
4. A writable `/config` volume for the codec preferences.

If hardware encode doesn't light up, the Codecs page shows each encoder's
availability with the reason from the real test encode.

**File safety.** ffmpeg writes to a dotfile temp path in the same directory
(`.wheelhouse-convert-<jobId>.ext`), which Radarr/Sonarr scanners ignore, so a
mid-conversion scan cannot pick up a half-written file as a second copy. Only
after ffmpeg exits 0 *and* the output is non-empty is the original kept as
`<original>.bak` (a non-video extension so scanners do not treat it as an
alternate version) and the temp file renamed into its place. **The backup never
replaces an existing file:** it is made with a hard link (or, where the
filesystem has none, a checked rename with a tiny race window), and a taken name
moves on to `.bak.1`, `.bak.2`, so converting a file twice keeps both originals;
the job's `backupPath` says which. The swap is first recorded in the database
(`conversion_jobs.swap_path` and `swap_backup`) and the record is cleared when
it finishes or is rolled back. **A backup is never deleted automatically.** On failure or cancellation only the temp file is
removed. The path is resolved (`..` refused, symlinks evaluated, regular file,
inside a media root) when the request arrives, when the job starts and just
before the swap. After a successful swap the manager is asked to rescan the
movie or series, so its own database (size, mediaInfo, codec) picks up the new
file.

**HDR and Dolby Vision.** Conversion writes SDR 8-bit (`-pix_fmt yuv420p`) and
does not tone map, so an HDR source would come out flattened and lose its HDR10,
HLG or Dolby Vision metadata. Therefore such files are not conversion
candidates by default (`hdrExcluded` counts them). An administrator can set
`includeHdr`; the candidates then carry `hdr` (`hdr10`, `hlg`, `dolby-vision`,
`unknown`) and the UI badges them. A conversion request for an HDR file is a
409 `hdr` until it has `acknowledgeHdr: true`, and the server decides from the
file itself (ffprobe: PQ or HLG transfer, Dolby Vision side data; a probe it
cannot read is `unknown`), not from the request. The runner probes again before
ffmpeg. Tone mapping is out of scope.

**Restart recovery.** Interrupted encodes are not resumed. At start the saved
jobs are loaded: a job that was probing or running is marked `failed` with
`interrupted: the server stopped while this conversion was running` (a graceful
shutdown does the same), queued jobs stay queued and run in the order they were
added once the clean-up is done, and finished jobs are pruned (older than 30 days,
or beyond the newest 200). Jobs read from the database are never trusted: the
backend and codec are validated again and the path is resolved against the
current media roots before a job runs. Once per process, at start, a job that still has a swap record (the process died
during the swap) is restored from the recorded paths, which are checked against
the roots in force; a stray `.bak` that no record names is never touched, and a
row from before the record existed restores nothing. Then the media roots are
walked and any `.wheelhouse-convert-*` temp file is deleted.

## API routes (all server-side proxies — API keys never reach the browser)

- `GET /healthz` — `{"status":"ok","version":"0.3.0"}`; the container's
  `HEALTHCHECK` runs `wheelhouse healthcheck`, which asks it.
- `GET /api/features` — the feature registry's answer: which pages are available
  and what the others still need. No hosts, keys or instance names.
- `GET /api/status` — `{ instances: [...] }`, one entry per enabled instance with
  `id`, `providerId`, `role`, `name`, `state` (`ok`, `degraded`, `needs-setup`,
  `down`), `version`, `error`, `link` and the role's normalized `detail`.
  Instances are queried in parallel and each fails independently (`down` plus a
  redacted `error`, no `detail`) rather than taking the route down; a
  misconfigured instance is `down` too. The state is `degraded` for manager
  health warnings or errors and for SABnzbd-style errors younger than 24 h, and
  `needs-setup` for an unclaimed Plex or unconfigured Jellyfin. Plex degrades in
  stages: unclaimed → `needs-setup`; no or rejected `PLEX_TOKEN` → `ok` with
  basic version info from the unauthenticated `/identity` and a `notice`;
  claimed and tokened → also the server name and active session count. Jellyfin
  follows the same stages with its own signals (setup wizard not completed →
  `needs-setup`; no or rejected key → `ok` with `/System/Info/Public` data and
  a `notice`; with a key, the name and the number of sessions that are playing
  something). With nothing configured the answer is `{ instances: [] }`.
  Sonarr/Radarr's indexer summary (`{ total, enabled, issues }`) comes from
  `/api/v3/indexer` cross-referenced with `/api/v3/health` entries whose
  `source` starts with `Indexer` (there is no per-indexer live health endpoint:
  `/api/v3/indexerstatus` 404s on Sonarr 4.0.15 and Radarr 5.26.2). SABnzbd's
  `warnings` exclude "Refused connection..." entries, which are its web UI's host
  whitelist rejecting a client and unrelated to server health.
- `GET /api/downloads` — every configured download client's items in the
  normalized shape (bytes, `progress` 0..1, `state`, `protocol`), each tagged
  with its `instanceId`: `{ instances: [{ instanceId, name, protocol, up,
  paused?, speedBytesPerSec?, error? }], items }`. A client that fails is
  reported in `instances` with its redacted error and does not affect the
  others. It is separate from `/api/status` so it can be polled faster.

  The dashboard sorts the rows by state (failed, post-processing or checking,
  downloading, stalled, queued, paused, seeding), draws at most 50 and adds a
  "+N more" line.

  qBittorrent logs in with a cookie session (`POST /api/v2/auth/login`) and
  signs in again once on a 403. v4's `paused*` and v5's `stopped*` states both
  map to the normalized ones. Every torrent is listed except finished, stopped
  ones, so seeding torrents appear with ratio and upload speed. `paused` is true
  only when every unfinished torrent is stopped; `queueCount` counts torrents
  below 100 %.

  A SABnzbd download keeps showing (amber, pulsing bar) through its
  post-processing phases (Extracting/Verifying/Repairing/Moving/Running) until
  it is done. SABnzbd's own `mode=queue` collapses all of those phases to
  "Downloading"; the accurate status is only in `mode=history`, so the adapter
  overlays the still-active history entries onto the matching queue slot by
  `nzo_id` (`state: "postprocessing"`, phase in `stateDetail`). It is an
  enrichment of the queue list, never a second list, so nothing is counted twice.
- `GET /api/storage` — `{ entries, unplaced?, instances }`. For every media manager its
  `/api/v3/rootfolder` entries (`freeSpace`) plus the `sizeOnDisk` of the
  library items inside it (placed by `path ?? rootFolderPath` in the deepest
  root folder that contains them, matched on whole path segments). The API has
  no `totalSpace`, so this reports free space plus library-used space rather
  than a true used/total percentage. An instance that cannot be read is listed
  in `instances` with `up: false` and the others are still returned. An item
  that lies inside none of the instance's reported root folders (a path mapping
  that differs from the folder it lists, or a removed root folder) cannot be
  tied to a disk, so it is not added to any root's `libraryUsedBytes`; per
  instance, `unplaced: [{ instanceId, label, items, bytes }]` says how much
  there is (the field is left out when nothing is unplaced).
- `GET /api/library` — `{ items, instances }` where an item is `{ instanceId,
  instanceName, kind, id, title, sizeOnDisk, monitored, rootFolderPath, added }`
  and `instances` lists every configured media manager with `up`/`error`. Ids
  are only unique within an instance: `instanceId` + `id` is the key.
- `DELETE /api/library/{instanceId}/{id}` — whole-item delete
  (`deleteFiles=true`) through Sonarr/Radarr's delete endpoint. **It requires
  `?confirm=true`** (issue #41): without it the answer is 400 and nothing is
  sent upstream. Each confirmed delete writes an `audit` log line (action,
  instance, ids; no query string, no user yet).
- `GET /api/library/{instanceId}/{id}/details` — the detail card: `{ title,
  overview, year, genres, runtime, certification, status, network, studio,
  posterUrl, fanartUrl, ratings }`. The image URLs are the `remoteUrl` of each
  item's `images[]` (TheTVDB / TMDB CDN), so the browser loads them directly
  with no proxying or key. Radarr exposes several rating sources, Sonarr one;
  both are normalized to `{ source, value, votes }[]`.
- `GET /api/library/{instanceId}/{id}/seasons` — series only: `{ seasonNumber,
  monitored, episodeFileCount, episodeCount, totalEpisodeCount, sizeOnDisk }`
  per season, straight from the series resource.
- `DELETE /api/library/{instanceId}/{id}/seasons/{season}?confirm=true` — series
  only. Looks up the series' episode files, deletes each
  (`DELETE /api/v3/episodefile/{id}`), then `PUT`s the series back with that
  season's `monitored` set to `false`, so Sonarr does not re-grab it.
- `GET /api/search?kind=movies|series&term=` — the lookup of every instance of
  that kind, normalized to `{ results, problems? }`, `results` being
  `{ instanceId, instanceName, id, alreadyAdded, title, year, overview,
  posterUrl, subtitle }` (`id` is the external tvdbId/tmdbId that goes to
  `/api/add`; `subtitle` is the network for series and the studio for movies).
  At most 8 results per instance, so a movie in two Radarrs shows up once per
  instance. An instance that fails goes to `problems`; if all fail the answer is
  502. A missing term answers `{"results": []}`.
- `GET /api/add-meta?instanceId=` (or `?kind=movies|series`, which Explore uses,
  for the first instance of that kind that answers: one that is misconfigured or
  down is skipped while another works, and the first error is reported only when
  none does; an explicit `instanceId` reports its own error) — `{ instanceId,
  instanceName, qualityProfiles: [{id, name}], rootFolderPath, instances: [{
  instanceId, name }] }`. `instances` lists every ready instance of the kind, for
  a picker. The first root folder is used rather than offered as a choice.
- `POST /api/add` — body `{ instanceId, id, qualityProfileId, rootFolderPath,
  monitored, searchNow, minimumAvailability? }`; see "Add to library".
- `GET /api/explore?category=theaters|bluray|popular[&limit=N][&page=P]` —
  `limit` defaults to 20 (max 60); `page` is 1-based in units of `limit`.
  `{ results: [{ id, alreadyAdded, libraryInstances: [{ instanceId, name }],
  title, year, overview, posterUrl, voteAverage }], hasMore }`. Without `TMDB_API_KEY` it is a 502 (not a 404).
- `GET /api/explore/{id}` — `{ id, alreadyAdded, libraryInstances, title, overview, year, genres,
  runtime, voteAverage, posterUrl, backdropUrl, trailerKey }`.
- `GET /api/codec-analysis?kind=movies|series` — across every instance of that
  kind: `{ totalCount, totalSize, preferences: { hd, uhd }, buckets: { h264,
  hevc, legacy }, candidates: [{ title, codec, resolution, bitDepth, size,
  instanceId, itemId, filePath, hdr, tier, targetFamily, reason }], matching: [...],
  skippedCount, hdrExcluded, problems? }`. Read-only.
- `GET`/`PUT /api/codec-analysis/settings` — `{ hd: "h264"|"hevc", uhd:
  "h264"|"hevc" }`. `PUT` validates both fields and writes
  `/config/settings.json` (defaults `h264`/`hevc` when the file is absent). The
  file is written to a temporary name and renamed over the old one; if that is
  impossible (the file is bind-mounted, or the directory is read-only around a
  writable file) it is written in place. A failure is a 500 that tells the user
  to check the `/config` volume.
- `GET /api/codec-analysis/encoders` — `{ encoders: [{ backend, family, label,
  available, reason? }] }`, six entries; see "Hardware encoding".
- `POST /api/codec-analysis/convert` — `{ instanceId, itemId, filePath, title,
  originalSize, backend, targetFamily }` (a candidate's fields plus the modal's
  backend choice). Enqueues a job and returns it at once (`status: "queued"`);
  a path outside the media roots or not an existing regular file is a 400, a
  hard-linked file a 409 (see above), an HDR or Dolby Vision file a 409 with
  `code: "hdr-excluded"` (not allowed) or `"hdr"` (send `acknowledgeHdr: true`).
  `GET` on the same path lists all jobs.
- `GET /api/codec-analysis/convert/{jobId}` — one job: `{ id, instanceId,
  itemId, filePath, title, originalSize, backend, targetFamily, status,
  progressPercent, error?, newSize?, backupPath?, log, createdAt, startedAt?,
  finishedAt?, hdr, owner, ownerName, acknowledgedHardlink, acknowledgedHdr }`,
  polled every 2 s until `success`/`failed`/`canceled`.
- `GET`/`PUT /api/admin/converter` (admin) — `{ mediaRoots, includeHdr,
  effectiveRoots, source }`, `source` being `database`, `environment` or
  `default`. `PUT` takes `mediaRoots` and/or `includeHdr`; roots are validated
  (absolute, no `..`, no system folder, must exist as directories, no
  duplicates or nesting) and an empty list removes the saved roots.
- `POST /api/codec-analysis/convert/{jobId}/cancel` — cancels a queued job, or
  kills the running ffmpeg. A job that does not exist is a 404; one that has
  already finished is a 400.

The acceptance suite (`acceptance/`) lists every route in `routes.ts` and checks
its status with nothing configured and with the full fake stack.

Provider specifics: Sonarr and Radarr (`internal/providers/mediamanager`) add
`X-Api-Key` and the `/api/v3` prefix and turn an HTTP 401 into "API key
incorrect". SABnzbd uses query-parameter authentication (a wrong key is an HTTP
403 with a plain-text body). qBittorrent uses a cookie session (a wrong login is
a 401 on v5 and a 200 "Fails." on v4). Plex sends `Accept: application/json` and
`X-Plex-Token` for anything beyond `/identity`. Jellyfin sends
`Authorization: MediaBrowser Token="<key>"` for anything beyond
`/System/Info/Public` (Jellyfin 12 rejects `X-Emby-Token`). TMDB (role
`metadata`, no shared interface: Explore's categories are TMDB-specific) sends
the key as the `api_key` query parameter, TMDB's own convention, so errors never
echo request URLs and the key is redacted from them.

## Feature registry

`internal/features` declares what each page needs: Dashboard nothing; Library
any media manager; Explore a metadata provider and a media manager of kind
`movies`; Codecs any media manager and ffmpeg. Only **ready** instances count
(valid config), so a half-configured Radarr unlocks nothing. ffmpeg is probed
once per process (`ffmpeg -version`).

`GET /api/features` returns `{ features: [{ id, label, route, available,
missing: ["a Radarr instance", ...] }] }`. The header nav (`FeatureNav.tsx`)
shows Dashboard plus the available features, the global add/search box
(`AddSearchGate.tsx`) appears only when Library is available, and the Library,
Explore and Codecs routes are wrapped in `FeatureGate.tsx`, which renders a stub
saying what is missing, with a link to the README's Configuration section.

An administrator can switch Library, Explore or Codecs off (`PUT
/api/admin/features`; the Dashboard cannot be). A disabled feature is reported
`available: false, disabled: true, missing: ["disabled by admin"]`, and the
router refuses the API routes that feature serves (`/api/library*`,
`/api/explore*`, `/api/codec-analysis*`) with 403 for everyone, so a switched-off
Codecs page cannot be driven by hand. `/api/add`, `/api/search` and
`/api/add-meta` are shared with other features and stay. This is a convenience
for the household, not a boundary between roles.

## Config

Environment variables, read by the server only (never sent to the browser). All
are optional; a service that is not configured is reported as such instead of
breaking the app:

```
SONARR_URL, SONARR_API_KEY
RADARR_URL, RADARR_API_KEY
SABNZBD_URL, SABNZBD_API_KEY
QBITTORRENT_URL, QBITTORRENT_USERNAME, QBITTORRENT_PASSWORD
PLEX_URL, PLEX_TOKEN
JELLYFIN_URL, JELLYFIN_API_KEY
TMDB_API_KEY, TMDB_BASE_URL
```

Extra Sonarr/Radarr instances use `RADARR_<ID>_URL` and `RADARR_<ID>_API_KEY`
(same for `SONARR_`): `RADARR_4K_URL` is the instance `radarr-4k`, shown as
"Radarr 4K". Environment variables are a **read-only** source. The same instances can be
stored in the database (see "Stored configuration" below), which wins by id.

Server settings: `PORT` (3000; 3050 in the image), `WHEELHOUSE_LISTEN`
(`host:port`, overrides `PORT`), `WHEELHOUSE_MEDIA_ROOTS` (`/movies:/tv`),
`WHEELHOUSE_CONFIG_DIR` (`/config`), `WHEELHOUSE_DB` (default
`/config/wheelhouse.db`), `WHEELHOUSE_SECRET_KEY_FILE` / `WHEELHOUSE_SECRET_KEY`
(the master key; otherwise `/config/secret.key` is generated at the first start)
`WHEELHOUSE_DENIED_NETWORKS` (networks provider clients refuse to connect to;
default link-local and cloud metadata, `none` to allow all) and
`WHEELHOUSE_TRUSTED_PROXIES` (reverse proxies whose forwarded headers are
believed, together with the list stored in the database; default none). The
server keeps its state in `/config`: the database,
the key, `settings.json` and, until the first account exists, `setup-token`.

### Accounts and sessions

`server/internal/auth` owns the SQL for accounts (migration `0002_auth.sql`:
`users`, `sessions`, `audit_log`); `internal/app` turns requests into calls on it
and enforces the route levels. Phase 2 item 2 ([plan](wheelhouse-phase2-plan.md)).

- **Start.** `main` opens the database (a missing or world-writable directory, a
  database with the wrong mode, a schema newer than the binary and a key that does
  not match are each fatal with a message that says what to do), loads or
  generates the master key, and, when no account exists, makes a one-time setup
  token (logged once, written to `/config/setup-token`, mode 0600). `/healthz`
  answers without the database but reports `degraded` (503) when the store does
  not answer.
- **Setup.** With zero accounts every non-public route answers 503 `setup
  required`. `POST /api/auth/setup` creates the first administrator only while no
  account exists, only with the token (compared in constant time, guesses are
  throttled per address), inside one transaction that re-checks the count; it
  deletes the token file and does not sign anyone in.
- **Passwords.** Argon2id (`golang.org/x/crypto/argon2`; 64 MiB, 3 passes, 2
  lanes), the parameters stored in each encoded hash and raised by a rehash at the
  next login; at most 3 hashes at a time so a login flood cannot exhaust memory.
  At least 12 characters, at most 1024 bytes, not on an embedded list of common
  passwords, not the username, no composition rules. A login for a name that does
  not exist verifies against a dummy hash made at start, so the work and the
  answer do not tell whether an account exists. Passwords are hashed, never
  encrypted: **accounts do not depend on the master key** (a test forbids
  `internal/auth` from importing `internal/secrets`).
- **Sessions.** A 256-bit random token from `crypto/rand`; only its SHA-256 is
  stored. The cookie is `HttpOnly`, `SameSite=Strict`, `Path=/`, and `Secure` with
  the name `__Host-wheelhouse` when the request arrived over HTTPS (directly, or
  `X-Forwarded-Proto: https` from a peer in `WHEELHOUSE_TRUSTED_PROXIES`),
  otherwise `wheelhouse`. Only the cookie that fits the transport is read. Idle
  timeout 12 h and absolute 30 days; `last_seen` is written at most once a minute
  per session. A new session at every login and at a password change, which also
  ends the account's other sessions; an admin reset, disabling and deleting an
  account end its sessions. The role is read from the account at every request,
  so a demotion applies at once.
- **CSRF.** Every state-changing request (not GET, HEAD or OPTIONS) must be
  same-origin: a `Sec-Fetch-Site` other than `same-origin` or `none`, or an
  `Origin` that is not the request's own host, is 403, on login and setup too. On
  authenticated routes it must also send the session's token in `X-CSRF-Token`.
  The token is derived as SHA-256 of the cookie value with a label, so nothing is
  stored and a database copy does not reveal it; it is returned by login and
  `GET /api/auth/session`, never set in a script-readable cookie. A request with
  neither `Origin` nor `Sec-Fetch-Site` is a non-browser client and still needs
  the token. `GET` never changes state (a test walks the router).
- **Local-network bypass** (`internal/netaccess`, table `network_settings`,
  `GET/PUT /api/admin/network`). The guard resolves the client address once per
  request (`netaccess.Service.Resolve`: the TCP peer, or, for a trusted proxy,
  the right-most untrusted forwarded address; unknown when headers are malformed
  or disagree) and keeps it in the request context for the audit log and
  throttling. With the bypass on, a request that has no valid session and whose
  client address is inside a range is `auth.BypassIdentity`: role `user`, no
  session, `Bypass: true`, CSRF token = HMAC of the address under a per-process
  key, returned by `GET /api/auth/session` (`"bypass": true`). It is refused
  (401) on logout, password change and the session routes, 403 on admin routes;
  a real session always wins. The settings are cached in memory and replaced on
  each update, so a request never reads the database for them. Before the bypass
  identity is granted the request's `Host` must be acceptable
  (`netaccess.Service.HostAllowed`: an IP literal, `localhost`, a single-label
  name, `WHEELHOUSE_ALLOWED_HOSTS` or the stored `allowedHosts`, column
  `allowed_hosts`, migration 0006), the defence against DNS rebinding; a request
  that fails it is an ordinary anonymous 401. `PUT /api/admin/network` adds the
  request's own Host when it turns the bypass on (`addedHost` in the response).
- **Login throttling.** In memory (a failed login causes no database write, a
  restart is the only thing that forgets it, and the locks are short): the first 4
  failures for a name are free, the 5th locks it for 30 s and each further failure
  doubles that up to 15 min; 20 failures lock a client address (an IPv6 address
  counts as its /64); 10 wrong setup tokens lock an address for setup. Counters
  exist for names that do not exist, a locked login answers like a wrong password,
  and a password reset by an admin or the CLI lifts the lock. The table is bounded.
  Attempts in flight count toward the threshold: `Login` and `ChangePassword`
  reserve a slot per key before hashing (`throttle.reserve`), convert it into a
  failure or release it afterwards, re-check the lock once the hash semaphore is
  held, skip Argon2 for locked keys, and shed load when more than 32 verifications
  are queued, so a parallel burst cannot exceed the allowance.
- **Audit log.** `audit_log` is append-only (triggers refuse update and delete).
  Setup, login ok / failure / lockout, logout, password change and reset, user
  create / update / role change / delete, session revocation, library add and
  delete, and conversion start and cancel are recorded with the acting account,
  the address and a small JSON detail; never a password, token or request body. A
  failed login for an unknown name records no name.
- **Last administrator.** The last enabled administrator cannot be deleted,
  demoted or disabled, checked in code inside the transaction and by two triggers.
- **Recovery.** `wheelhouse reset-password <user>` (generated one-time password,
  `must_change_password`, sessions ended, audited as `cli`); a flagged account can
  call only the session, password and logout routes until it has changed it.

### Web sign-in (`web/src`, plan item 6)

- **One client.** Every request goes through `lib/http.ts` (`apiFetch`/`apiJson`);
  a test fails if anything else calls `fetch`. It sends the cookie
  (`credentials: "same-origin"`), adds `X-CSRF-Token` to every non-GET request and,
  on a CSRF refusal, fetches the session again and retries once. The token comes
  from `GET /api/auth/session` or the login answer and lives only in module
  memory: no `localStorage`, `sessionStorage` or script-readable cookie holds
  anything (the session cookie is `HttpOnly`).
- **Central handling.** A 401 on a normal request makes the session provider
  (`components/SessionProvider.tsx`) anonymous and clears every SWR cache; the
  route guard then goes to `/login?redirect=<where you were>` (only in-app paths
  are honoured, see `lib/redirect.ts`). A 503 `setup required` goes to `/setup`;
  a 403 `password-change-required` goes to `/change-password`. Login, setup and the
  session probe are `anonymous` requests whose 401/503 are ordinary results.
- **Session shape.** The UI reads `{"user": {id, username, role, mustChangePassword},
  "csrfToken": "...", "bypass": true|false}`. `bypass` is set by the server when
  the local-network bypass (plan item 5) let the request in without a login; a
  missing `bypass` is false, a bypass answer is never treated as admin, a missing
  `user` on a bypass answer is a `user` called "Local network", and a missing
  `csrfToken` just means no header is sent. Adapt `lib/session-context.ts`
  (`parseSession`) if the final shape differs.
- **Routes.** `/login` and `/setup` are open; everything else is behind
  `RequireAuth` (loading, error, setup, login redirect, forced password change).
  `/admin/*` is behind `RequireAdmin`: a `user` sees "Not authorised", a bypass
  visitor is sent to the login page for an administrator login. The administration
  pages (item 7) are the children of that route: Services, Features, Users,
  Sessions, Network, Media and converter, Audit log, under a sub-navigation; the
  header shows an "Admin" link to administrators only.
- **Pages.** Login (generic error for 401, a throttling message for 429), setup
  wizard step 1 (token, username, password with the 12-character hint, confirm;
  then signs in and lands on the Dashboard, where a first-run checklist banner
  (administrators only, until at least one service exists) links to the new-service
  form), account (role, change password, own sessions with revoke;
  for a bypass visitor only "signed in from the local network"), forced password
  change, and a header user menu (name, role, Account, Sign out; no Sign out for
  bypass).

### Stored configuration

`internal/instances` (`Source`) implements `core.ConfigSource` over the
database (`instances` and `feature_settings`, migration 0003) and, read-only,
the environment. **Precedence:** an instance in the database hides an
environment instance with the same id; an environment instance with no row is
shown as `source: "environment"` and cannot be edited, deleted or replaced by a
create with its id. `POST /api/admin/instances/{id}/import` copies it into the
database (settings as they are, a URL's user information dropped), after which
the environment values for that id are ignored; deleting the stored copy brings
the environment one back (`environmentAgain: true`).

Secret settings (the `secret` fields of the provider's config struct) of one
instance are a single JSON object sealed with `secrets.Cipher` in
`instances.secrets`, associated data `(instances, id, secrets)`, so a value
copied to another row does not open. Non-secret settings are JSON in `config`.
A row whose secrets cannot be opened is served without them (the instance then
reports its credentials missing) and logged once; it never fails the others.

**Runtime reload.** The `core.Runtime` asks the source for the instances on
every call and caches each client by a hash of the validated config, so a create,
edit, enable/disable or delete is seen by the next request: a changed instance
gets a new client (and so a fresh response cache), a deleted one is dropped, and
a request already using the old client finishes with it. Writes are single
transactions on the one writer connection, so two edits cannot lose each other's
secret (`TestRuntimePicksUpChangesWithoutRestart` runs them concurrently under
`-race`).

**Admin API** (all `admin`, CSRF-protected, audited with setting names, never
values):

| Route | |
|---|---|
| `GET /api/admin/providers` | the registry: id, role, display name, JSON Schema of the config form (`x-secret` marks credentials), capabilities |
| `GET /api/admin/instances` | every instance: `source`, `readOnly`, `overridesEnvironment`, `status`, redacted `error`, `lastTest`, `config` (non-secret values) and `secrets: {name: {set: bool}}` |
| `POST /api/admin/instances` | create: `providerId`, optional `id` (the provider id, or starting with `<provider>-`; lowercase letters, digits, hyphens; derived from the name when omitted), `name`, `enabled`, `config`; validated by the provider's config; 400 with `fields`, 409 on a duplicate id or an environment id |
| `PUT /api/admin/instances/{id}` | update `name`, `enabled`, `config`; a secret left out keeps its stored value, a given one replaces it |
| `DELETE /api/admin/instances/{id}` | delete a stored instance |
| `POST /api/admin/instances/{id}/import` | copy an environment instance into the database |
| `POST /api/admin/instances/test` | connection test of unsaved `{providerId, config}`, or of a saved `{id}` (with optional edited `config`, stored secrets filled in); returns `{ok, version, error, durationMs}`, redacted |
| `GET/PUT /api/admin/features` | per-feature `enabled` flags |

**Administration pages** (`web/src/pages/admin`). Service forms are generated from
the provider's JSON Schema (`configSchema`, with `x-order` for the order and `x-secret` /
`writeOnly` for credentials), so a new provider needs no UI code; a committed copy of the
registry's output (`web/src/fixtures/providers.json`) is checked against the Go registry
by `TestWebProviderSnapshotMatchesRegistry`. A secret is never read back: the list
reports `secrets: {name: {set}}`, the edit form shows "set" and a Replace action, and
an update that omits the secret keeps the stored one. The UI also uses
`DELETE /api/users/{id}/sessions/{sessionId}` (administrator: end one session of another
user; added with item 7 because the users API could only end all of them), the users,
sessions and audit routes, `GET/PUT /api/admin/network` and `GET/PUT /api/admin/converter`.
The audit log filters (action, who) apply to the entries loaded so far; the API has
no filter parameters.

**URLs are trusted administrator input.** The server requests whatever address an
admin configures (a connection test, then every poll), so a URL must be `http` or
`https` with a host and no credentials, and the core HTTP client refuses to
connect to link-local and cloud-metadata addresses (`169.254.0.0/16`,
`fe80::/10`, `fd00:ec2::254`) by default, checked on the resolved address at
connect time. Private LAN ranges and loopback are allowed: they are where the
services are. `WHEELHOUSE_DENIED_NETWORKS` replaces the list (comma-separated
CIDRs; `none` allows everything).

### Storage and secrets

The storage layer ([plan](wheelhouse-phase2-plan.md), item 1) under the accounts.
The secret column is `instances.secrets` (see "Stored configuration"); the key is
created and checked at start so that a wrong key is noticed at once.

- **Database.** SQLite through `modernc.org/sqlite` (pure Go, so the binary stays
  static and cross-compiles). One file, mode 0600, WAL, foreign keys on. A writer
  pool of one connection and a pool of `query_only` readers, so writers queue in
  Go and readers never block. Embedded, numbered, forward-only migrations are
  applied at open in a single transaction and recorded with a hash in
  `schema_migrations`; a database newer than the binary is refused. Tables: `meta`
  (0001), `users`, `sessions`, `audit_log` (0002), `instances`, `feature_settings` (0003).
- **Secrets at rest.** `secrets.Cipher` seals each value with AES-256-GCM, a
  random nonce per value and the table, row id and field as associated data, in a
  versioned envelope (format byte, key version, nonce, ciphertext and tag). The
  master key comes from a key file, the environment or a generated
  `/config/secret.key`. A check value in `meta` says which key version the
  database is under, so a wrong key fails immediately, `rekey` can re-encrypt
  every secret column in one transaction, and `forget-secrets` is the recovery
  when the key is gone for good.
- **Backups** are `VACUUM INTO` snapshots taken while the server runs, mode 0600,
  never overwriting; they contain ciphertext, **not the key**. Losing the key
  loses the stored provider credentials for good; [operations.md](operations.md)
  has the checklist.
- **What this protects.** A copied database or backup. It does not protect a copy
  of the whole `/config` volume when the key is the generated file beside the
  database; see the residual risks in the [threat model](threat-model.md).

The formats and commands are specified in [server/README.md](../server/README.md#storage-and-secrets).

Sonarr/Radarr library lists are cached for 5 seconds per instance (concurrent
requests share one upstream call; adding, deleting or rescanning clears that
instance's cache; errors are never cached).

`PLEX_TOKEN` cannot be known until the Plex server has been claimed through its
own web UI; it then lives in Plex's `Preferences.xml` (`PlexOnlineToken`
attribute). `TMDB_API_KEY` is a free key from themoviedb.org (account settings →
API), used only by Explore. `JELLYFIN_API_KEY` is created in Jellyfin under
Dashboard → API Keys.

Keep the env file out of version control and readable only by the user running
the container (mode 600).

## Deployment

One image, `tristanwl/wheelhouse:<version>`, one process (`wheelhouse`), one
writable volume (`/config`). It runs as a non-root user (1000:1000 by default,
any `--user UID:GID` works, and the user needs write access to `/config`), with a
read-only root filesystem and all capabilities dropped if you like: the only
paths written are `/config`, the media directories (Convert) and, for driver
caches, `/tmp` (`HOME=/tmp`; give it a tmpfs). It listens on `PORT` (3050) on
all interfaces unless `WHEELHOUSE_LISTEN` says otherwise and has a built-in
health check (`wheelhouse healthcheck`, no curl). The README has the Docker
Compose and Podman/systemd examples.

The image is built in three stages (Dockerfile): Node builds `web/`, Go builds
the static binary with the UI embedded and the version stamped in from the root
`package.json`, and an Alpine runtime adds ffmpeg, the VA-API drivers,
`ca-certificates` and `tzdata`. Base images are pinned by tag and digest.
The image is multi-arch (linux/amd64 and linux/arm64, one tag, issue #14). The
UI and Go stages run on the build machine (`--platform=$BUILDPLATFORM`) and the
Go stage cross-compiles the static binary with `GOOS`/`GOARCH` from buildx, so
no compiler is emulated. Only the runtime stage runs on the target
architecture, and that is just `apk add`: ffmpeg, `mesa-va-gallium`,
`ca-certificates` and `tzdata` exist on both, while the two Intel VAAPI drivers
are installed only where `apk --print-arch` is `x86_64`. The release workflow
sets up QEMU (`docker/setup-qemu-action`, with its binfmt image pinned by
digest) for that one layer, publishes both platforms under the same tag and then
fails if the pushed manifest lacks either. CI's `docker` job builds both: the
amd64 image gets the full container smoke test and acceptance suite, the arm64
image is loaded and run under QEMU (version, ffmpeg and ffprobe present, the
same container smoke test). Hardware encoding on arm64 is out of scope; the
software encoders work.

The converter stays a role inside the server, behind an interface, so it can
later move to a separate worker (see the phase 2 spec).
