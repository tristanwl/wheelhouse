# Wheelhouse architecture

Wheelhouse is a self-hosted dashboard and companion for the *arr media stack
(Sonarr, Radarr, SABnzbd, Plex or Jellyfin). It shows live service status and storage
usage per media volume, lets you browse and delete library items — including
deleting individual TV seasons rather than whole series — directly through
Sonarr/Radarr's own APIs, and includes a codec analysis view that recommends
which files are actually worth converting (based on your own preferred codec
per resolution tier) and can run the conversion itself via ffmpeg, using
hardware encoding when available.

There is no authentication yet, so run it only on a trusted network. Auth,
a setup wizard, and swappable providers (Jellyfin, qBittorrent, ...) are
planned — see [wheelhouse-spec.md](./wheelhouse-spec.md).

## Stack

Next.js 15 (App Router, React 19, TypeScript), Tailwind CSS, SWR for client-side
polling. Single container, no database — it's a thin read/aggregate/proxy
layer over Sonarr, Radarr, and SABnzbd's existing APIs. Routes live in
`app/`, shared UI in `components/`, and the per-service adapters in
`lib/providers/` (one directory per role: media manager, download client,
media server).

A Go server that will replace the Next app is being built alongside it in
`server/`, following [wheelhouse-spec-phase2.md](./wheelhouse-spec-phase2.md)
(migration steps 2 and 3: skeleton, middleware, embedded UI placeholder, and
the provider core, then the providers and, from step 5, the API routes). So far
it serves `/healthz`, `/api/features`, `/api/status`, `/api/downloads`, `/api/explore` and a
placeholder page; nothing here describes it until the cut-over, and the Next app
is unchanged. See [server/README.md](../server/README.md).

### Go routes and wiring (`server/internal/app`, `providers/all`)

- **One registry.** `providers/all` builds the registry from every provider's
  `Definition()` and a runtime over a `ConfigSource`; `app.Options{Source}` (or a
  ready `Runtime`) injects it, `main` passes the process environment, tests pass
  a fake one.
- **Access levels.** Every route is registered through `app.Router.Handle(pattern,
  level, handler)` with `Public`, `Authenticated` or `Admin` (all public until
  Phase 2). The router's `authorize` is the single place a session check will go.
  `router_test.go` lists every route with its level, and fails for a route that
  is missing or not listed, or that builds its own mux.
- **Features.** `features.Of(ctx, runtime, env)` is `getFeatures()`: the
  instances' roles, kinds and readiness against `Definitions`, plus the
  once-per-process `ffmpeg -version` probe.
- **Status.** `status.Compute` fans out to every enabled media manager,
  download client and media server in parallel (per-instance isolation: an
  error or panic is a `down` entry with a redacted reason). The state is
  `ok|degraded|needs-setup|down` (manager health warnings or errors, SABnzbd-style
  errors younger than 24 h, an unclaimed Plex or unconfigured Jellyfin). The
  `link` is computed on the server from the configured URL: scheme, port (none
  for a scheme's default) and path prefix, plus `/web` for Plex; never host,
  credentials or query.
- **Downloads.** `downloads.Compute` returns `instances` (one per enabled client,
  `up` false with the reason when it cannot answer) and normalized `items`, both
  in configured order.
- **Explore.** `explore.Browse` and `explore.Movie` use the first ready TMDB
  client (`lib/tmdb-client.ts` semantics: paging in units of `limit`, TMDB pages
  capped at 500) and read the movie library of every Radarr instance in parallel
  for `alreadyAdded`; an instance that fails is skipped. TMDB failures are a 502
  with `core.ErrorText`. `limit`/`page`/`id` parse like JavaScript `Number()`.
- A configured URL with `user:pass@` is an invalid config (the Next app never
  worked with these either: `fetch` refuses them), so the instance shows as down
  instead of sending Basic auth upstream.

### Go provider core (`server/internal/providers/core`)

The Go port of `lib/providers/core/`, with the same behaviour and ported tests,
(migration step 3). The Go server builds its runtime over it; the Next app's
provider layer above is still the one that runs.

- **Definitions and registry.** A `Definition` has an id, a `Role`, a display
  name, a `Config` struct (zero value) and a `Create` factory. `NewRegistry`
  rejects duplicate ids and malformed definitions; lookup by id and by role.
- **Config without zod.** A provider's settings are a struct with `json`,
  `label` and `config:"required,secret,url"` tags. The core validates raw
  settings against it (unknown keys ignored, empty strings rejected, optional
  `Validate()` hook) and builds a JSON Schema description of it for the admin UI
  (`Definition.Schema()`, with `writeOnly` and `x-secret` on credentials).
  Validation errors name the field and the rule, never the value.
- **Config sources.** `ConfigSource.Instances(ctx)`; `EnvConfigSource` has the
  same variables and rules as the TypeScript `ENV_MAPPINGS`, including extra
  Sonarr and Radarr instances (`RADARR_4K_URL` gives `radarr-4k`).
- **Runtime.** `ListInstances`, `Clients(role)` and `Client(id)` with the same
  four statuses (ready, disabled, unknown-provider, invalid-config). Clients are
  cached per instance and rebuilt when the validated config, provider or name
  changes. Safe for concurrent use.
- **HTTP client.** `NewHTTPClient` gives the same guarantees as
  `createHttpClient`: 8 s context timeout (overridable per request), path-prefix
  safe `JoinURL`, `Wheelhouse/<version>` user agent, redaction of every form of
  a configured secret plus `apikey=`-style parameters and `user:pass@` URLs,
  `ProviderError` with a status, GET cache with TTL, in-flight de-duplication
  and `Invalidate()`. It adds what the TypeScript version lacks: a cap on bytes
  read from upstream, no redirects to another host (and none from https to
  http), and TLS verification that cannot be switched off. Upstream bodies and
  reason phrases never reach an error or a log line.

## Conventions

Every modal (`ConfirmModal`, `SeriesModal`, `DetailModal`, `TmdbDetailModal`)
uses the shared `components/ModalCloseButton.tsx` — an × in the corner, not
just "click the backdrop to dismiss". On mobile a modal often fills the
whole viewport, leaving no backdrop to tap, so a dedicated close target
matters. Add it to any new modal rather than relying on backdrop-click alone.
The Wheelhouse logo in the header links to `/` (Dashboard), same as
every other site's home link.

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
  card wraps (`break-words`, `min-w-0`) so a long path can't overflow it. The
  pure logic (visibility, link building, card lines) is in `lib/status.ts`.
  Every card links out to that service's own web UI. The server sends only
  the configured port and path (`link`); the browser adds
  `window.location.hostname`, not a hardcoded hostname, so the links work the
  same way you reached Wheelhouse (by IP, mDNS name, Tailscale, whatever),
  rather than assuming mDNS resolves from every client (and the configured
  host may be a container-internal name). Set client-side in a `useEffect`
  (falls back to `localhost` for the initial server-rendered pass, to avoid a
  hydration mismatch, then swaps in the real host once mounted — the swap is
  effectively instant). Below the cards is a storage bar per root folder (TV,
  Movies); clicking a storage bar instead navigates to
  `/library?path=<rootFolderPath>`, pre-filtered to that volume. A Downloads
  section below Storage shows the download clients' queues live (protocol
  badge, name, state, progress bar, speed, ETA; ratio and upload speed for
  seeding torrents) — polled every 5s, faster than the 15s status
  poll, since this is the one thing on the dashboard actually worth watching
  in near-real-time.
- `/library` — a searchable, sortable table of every series (sonarr) + movie
  (radarr): title, size on disk, age (time since added), monitored state.
  Sorts by size on disk descending by default; click any of the Title/Size/Age
  headers to sort by that column instead (click again to flip direction). The
  table wrapper is `overflow-x-auto` with a `min-w-[640px]` table inside, not
  `overflow-hidden` — on mobile the six columns don't fit the viewport, and
  `overflow-hidden` clips content instead of making it scrollable, which
  also had a habit of pushing the whole page's horizontal extent out and
  fighting with normal vertical touch-scrolling. The scroll now stays
  contained to the table itself. A
  volume dropdown filters to a single root folder (All volumes / TV / Movies)
  — the same one the dashboard's storage-bar links populate via
  `?path=<rootFolderPath>`. Movies get a simple delete-with-confirm. Series
  get a "Manage" button opening a modal listing every season (episode count,
  size, monitored state) with per-season delete, plus a "Delete entire
  series" option in the same modal. Clicking a title (instead of the
  Manage/Delete button) opens a detail card — poster, year, runtime,
  certification, genres, ratings, overview — pulled live from
  Sonarr/Radarr's own metadata.
- `/explore` — movies-only browse-and-add: three horizontally-scrolling
  poster rows (In Theaters, Recent Blu-ray/Digital Releases, Popular) sourced
  from TMDB directly, since Sonarr/Radarr have no browse/trending API of
  their own (see "Explore" below). Shares the same quality-profile/monitored/
  search-now controls pattern as the Add search panel, and the same
  `POST /api/add` endpoint to add.
- `/codecs` — video codec breakdown of the library, with a Movies/TV toggle,
  a settings panel for your preferred codec per resolution tier (HD/UHD),
  and a "Convert" button per candidate that re-encodes the file in place via
  ffmpeg — to whichever codec its tier prefers, using hardware encoding when
  actually available (see "Codec Analysis" below). This is a real
  destructive action — it rewrites a media file on disk — gated by an
  in-app confirm step per the destructive-action rule in the repo's
  `CLAUDE.md`, same pattern as Library's delete confirm.

### Add to library

`components/AddSearch.tsx`, rendered in the root layout so it's on every
page, not just `/library`. TV Show/Movie toggle, a search box hitting
Sonarr's/Radarr's own lookup (TheTVDB/TMDB — same catalog their own "Add New"
screen searches), and inline controls for monitored, search-now, and
(movies only) minimum availability. Each result shows poster/title/year/
overview and either an "Add" button or an "In library" badge. With more than
one instance of a kind (say Radarr and Radarr 4K), the search covers all of
them and each hit is one instance's answer, labeled with the instance name;
Add posts to that instance, and its quality profiles and root folder load when
you click it.

Quality profile is **not** one of the always-visible controls — Explore's
Add button and `TmdbDetailModal`'s "Add to library" button work the same
way. Clicking Add doesn't create the item immediately; it opens
`components/AddQualityModal.tsx`, a small confirm dialog (poster, title, a
"Quality" `<select>`, Cancel/Add) and the actual `POST /api/add` only fires
once that's confirmed. This replaced an earlier design with a persistent
quality-profile dropdown in the top control bar that applied to whatever
you clicked next — confusing since it wasn't obvious the dropdown scoped to
a future click rather than the current search. The dropdown defaults to
"HD" via `pickDefaultQualityProfileId()` in `lib/quality.ts`: profile names
are arbitrary per-install strings (not a fixed enum), so it name-matches
"HD - 720p/1080p" exactly first, falls back to any name containing "HD" but
not "Ultra", then falls back to the first profile in the list. Still fully
user-selectable in the modal before confirming.

Sonarr/Radarr's lookup response is the full resource shape a not-yet-added
item *would* have, plus an `id` field — but that field is **omitted
entirely** (not `id: null`) when the item isn't in the library yet, so
`r.id !== null` silently mis-detects everything as already-added (`undefined
!== null` is `true`). Check `typeof r.id === "number"` instead — this bit
Wheelhouse once already (`app/api/search/route.ts`), worth remembering if
touching lookup-response handling elsewhere.

Adding works by re-fetching the exact item by external id right before
create — `GET /api/v3/series/lookup?term=tvdb:<id>` (sonarr, returns an
array) or `GET /api/v3/movie/lookup/tmdb?tmdbId=<id>` (radarr, returns the
object directly) — then overlaying the user's choices (`qualityProfileId`,
`rootFolderPath`, `monitored`, `addOptions`) onto that object and `POST`ing
the whole thing to `/api/v3/series` or `/api/v3/movie`. This mirrors what
each app's own web UI does; trying to construct the create payload from
scratch instead of re-fetching tends to miss required fields. Verified
end-to-end against live Radarr (add → confirmed correct quality
profile/root folder/monitored state/path on the server → deleted).

### Explore

`app/explore/page.tsx` + `components/ExploreSection.tsx`. Neither Sonarr nor
Radarr has a native browse/trending endpoint — that's what tools like
Overseerr provide on top of them, not something built into the `*arr` apps
themselves. Radarr *does* have Theaters/Popular/Top/Upcoming categories, but
only via its Import List mechanism (a persistent auto-add config, not a
browse-and-preview API), and there's no "Blu-ray" category at all. So Explore
goes straight to TMDB instead (`lib/tmdb-client.ts`, needs `TMDB_API_KEY` —
see Config below):

- **In Theaters** — `/movie/now_playing?region=US`.
- **Recent Blu-ray/Digital Releases** — TMDB has no "Blu-ray" concept
  specifically; the closest real equivalent is `with_release_type=5`
  (physical release) on `/discover/movie`, sorted
  `primary_release_date.desc` and capped at today's date so
  announced-but-unreleased physical dates don't show up as "recent".
- **Popular** — `/movie/popular`.

"Already in library" detection cross-references results against Radarr's own
`tmdbId` field (already present on every movie resource) rather than calling
Sonarr/Radarr's lookup per result — one `getMovies()` call up front, then a
Set membership check. Adding reuses `POST /api/add` directly with the TMDB
id, since Radarr's `/api/v3/movie/lookup/tmdb?tmdbId=` accepts any valid TMDB
movie id regardless of which TMDB list it came from.

Each row is a horizontally-scrolling strip with fixed left/right arrow
buttons (`scrollBy` on a ref, not native scrollbar/wheel-only — that was the
original design and felt undiscoverable) and the native scrollbar hidden via
a `.scrollbar-hide` utility in `globals.css`. Clicking a poster/title (not
the Add button — that's a separate click target so adding doesn't require
opening the modal first) opens `components/TmdbDetailModal.tsx`: backdrop
banner, poster, runtime/rating/genres, overview, an Add button, and — the
reason this needs its own modal instead of reusing `DetailModal.tsx` — a
"Watch Trailer" button that swaps the backdrop for an embedded YouTube
player (`GET /api/explore/[id]`, TMDB's `/movie/{id}?append_to_response=
videos`). TMDB movies often carry dozens of promo videos (regional teasers,
TV spots, featurettes); `pickTrailerKey()` in `lib/tmdb-client.ts` prefers an
official video literally named "Official Trailer", falling back through
official Trailer → any Trailer → Teaser → none.

Each row's "All" link goes to `app/explore/[category]/page.tsx`, a poster
grid with a "Load more" button that appends 40 at a time (two TMDB pages)
via `useSWRInfinite`, for as long as TMDB has pages (it caps at page 500).
Popular re-ranks between requests, so later batches can repeat a movie from
an earlier one — the page de-dupes by tmdbId.

### Codec Analysis

`app/codecs/page.tsx` + `lib/codec.ts` + `GET /api/codec-analysis?kind=`.
Grew out of a one-off analysis originally done by hand over SSH against
Radarr's API; useful enough to become a real feature. Deliberately **not**
a "convert everything to one fixed codec" tool — the recommendation logic
in `lib/codec.ts`'s `analyzeCodecs()` is driven by **user-configurable
preferences** (`lib/settings.ts`, `GET`/`PUT /api/codec-analysis/settings`,
persisted to `/config/settings.json`), not a hardcoded target:

- Every file is classified into a resolution **tier** — `hd` or `uhd`
  (`classifyTier()`, same `FOUR_K_THRESHOLD_PX = 3000` long-edge-in-px
  threshold as before, e.g. `"3840x2076"` → 3840 → uhd) — and each tier has
  a preferred codec family, `CodecPreferences { hd, uhd }`. Defaults:
  **HD → H.264** (broadest playback compatibility), **UHD → HEVC** (its
  compression advantage over H.264 is largest at 4K, so converting a 4K
  file *to* H.264 would meaningfully inflate it — same reasoning the old
  hardcoded "leave 4K HEVC alone" rule used, just now a default instead of
  a rule). Editable from a small settings panel at the top of `/codecs`.
- **Legacy** (vc1, xvid, mpeg2, mpeg4, anything not H.264/HEVC) — always a
  conversion candidate, converting to whatever its tier prefers. Essentially
  no hardware decode support anywhere regardless of tier.
- **H.264/HEVC** — a candidate only if it doesn't already match its tier's
  preferred family. Under the *defaults* this reproduces the old fixed
  behavior exactly (HD H.264 never flagged, HD HEVC always flagged, UHD
  HEVC never flagged) — but a UHD file already in H.264 is now *also* a
  candidate (convert up to HEVC for the space savings), which the old
  hardcoded "H.264 is never a candidate" rule couldn't express. Files that
  already match their tier's preference land in a `matching` list instead
  (was called `leaveAsIs` back when it only ever meant "4K HEVC" — renamed
  once it started meaning "whatever you asked for").

This was generalized specifically to make the tool portable for
open-sourcing — someone else's box may have different (or no) hardware
encode capability, and their sensible default might not be "prefer H.264
everywhere." See "Hardware encoding" below for how encoder *availability*
is detected the same way, rather than assumed.

The gotcha that shaped this: Radarr/Sonarr's `mediaInfo.videoCodec` reports
the *encoder* name, not a clean codec family — `x264`, `h264`, and `avc` all
mean plain H.264; `x265`, `h265`, and `hevc` all mean HEVC (case varies too,
e.g. Radarr returns `"HEVC"`, confirmed live). `classifyCodec()` normalizes
and groups by a fixed name set per family rather than trusting the raw
string, or every summary/recommendation downstream would double-count the
same codec under different names.

Movies: one `GET /api/v3/movie` call already returns `movieFile.mediaInfo`
inline per movie when a file exists — no extra per-item calls needed
(`RadarrMovie.movieFile` in `lib/providers/media-manager/types.ts`). TV has no equivalent bulk
endpoint — Sonarr's episode-file media info is only available per series
(`GET /api/v3/episodefile?seriesId=`), so the route fans out one call per
series via `Promise.all` and flattens the results.

TV candidates are labeled by actual episode, e.g. `Band of Brothers —
S01E01 "Currahee"`, not just a season — this needed a second per-series
call. Sonarr's `episodefile` resource only carries a season number;
episode number and title live on the separate `episode` resource
(`GET /api/v3/episode?seriesId=`, the `Episode` type in
`lib/providers/media-manager/types.ts`),
linked back to a file via `episodeFileId`. `episodeLabel()` in
`app/api/codec-analysis/route.ts` matches episodes to files by that id —
an array match rather than one-to-one, since an aired double-episode can
share a single file (`S03E07-E08`) — and falls back to the old
season-only label only if a file has no matching episode record at all
(hasn't come up in practice against the live library, but a file imported
before Sonarr's episode list catches up is a real possible cause). Doubling
the per-series calls (episodefile + episode, still both parallelized via
`Promise.all`) stayed fast: confirmed ~2.4s end-to-end against the live
library's full 1613-file TV catalog, still well within "clean and quick."

Movies/files with no `movieFile` yet, or a file whose `mediaInfo` hasn't
been scanned yet, are excluded from the analysis (not guessed at as any
particular codec) and surfaced as a `skippedCount` instead.

The "Copy list" button on the candidates table is a client-side
`navigator.clipboard.writeText()` of a tab-separated candidate dump — no
server-side persistence of a conversion list.

#### Convert

Each candidate row has a "Convert" button opening `components/ConvertModal.tsx`
— a confirm step (title, codec, size, explains the `.bak` safety net and that
conversions are serialized) before anything runs, consistent with every
other destructive action in this app. Confirming starts a real ffmpeg
encode against the live NFS-mounted file and polls for progress.

**Wheelhouse's container must mount the media at the same paths the *arr
apps use** — `/movies` and `/tv` in the example
[compose file](../docker-compose.example.yml) and
[systemd unit](../deploy/wheelhouse.service.example). This is needed only for
the Convert feature: everything else talks to Sonarr/Radarr purely through
their APIs. `CodecFileInfo.filePath` (from `movieFile.path` /
`episodeFile.path`) resolves to a real file inside Wheelhouse's container
without any path translation, as long as the mounts match. (Configurable
path mapping is planned.)

**Why ffmpeg instead of literal HandBrakeCLI**: HandBrakeCLI doesn't install
cleanly on Alpine (Wheelhouse's base image); ffmpeg does via
`apk add` and covers every codec/backend combination this needs (libx264/
libx265 for software, VAAPI, NVENC — see below), so the base image didn't
need to change.

`lib/hw-encode.ts`'s `ffmpegArgsFor(backend, family, filePath, tempPath)`
builds the actual command, one of three shapes depending on `backend`
(`"software" | "vaapi" | "nvenc"`) and target `family` (`"h264" | "hevc"`):
software is `-map 0 -c copy -c:v libx264|libx265 -preset medium -crf 20
-pix_fmt yuv420p` — copies every stream (audio/subs/attachments) untouched
via `-map 0 -c copy`, then overrides just the video codec; `-pix_fmt
yuv420p` forces 8-bit output even from a 10-bit source, since the entire
point of converting is maximizing playback compatibility. VAAPI and NVENC
can't use that same blanket `-c copy` alongside their own filter/encode
setup, so their arg shapes differ (see "Hardware encoding" below). All
three keep the same container format as the source (temp/output file
reuses the original's extension) to avoid subtitle/audio-codec-vs-container
mismatches that a fixed output format (e.g. always `.mp4`) would risk.

**Job model**: `lib/convert-queue.ts` is an in-memory, module-level job
queue (guarded via a `globalThis` singleton against Next.js dev-mode module
reloads) — there's no database in this app, and jobs only need to survive
within one Node process's lifetime. **Job history is lost on container
restart/redeploy** — acceptable for a live-progress feature, but worth
knowing if a conversion appears to "disappear" after a deploy. Only one job
runs at a time (`processQueue()`'s serial loop) — deliberate, since
transcoding is CPU-heavy and the host usually runs Sonarr/Radarr/a media server live;
starting several conversions just queues the rest rather than running them
concurrently.

Each candidate row in `app/codecs/page.tsx` polls the job list
(`GET /api/codec-analysis/convert`, every 2s) and replaces its own "Convert"
button with a live progress bar (or "Queued…") whenever a job exists for
that row's exact `filePath` — clicking it reopens `ConvertModal` reattached
to that job (`existingJobId` prop) rather than losing the live view, which
was the actual bug this replaced: closing the modal used to mean the only
way back in was clicking "Convert" again, which started a **second**
job against the same file. `enqueueConversion()` now guards against that
server-side too (`findActiveJobForPath()`) — a file with an active job just
hands back that job instead of starting a duplicate, since a second job
would rename whatever's *currently* at that path (possibly job 1's
already-converted output) to `.bak`, clobbering job 1's backup and losing
the true original. `ConvertModal` also shows a scrolling, auto-scrolling
`<pre>` of the job's raw ffmpeg output (`job.log`, capped to the last 8000
chars) — both the `-progress` stream and ffmpeg's own stderr banner/stream
mapping, for anyone who wants to see what's actually happening rather than
just a percentage.

**Hardware encoding**: `lib/hw-encode.ts` detects, per (backend, codec
family) pair, whether this box can actually hardware-encode that codec —
`detectEncoders()` probes VAAPI-H.264, VAAPI-HEVC, NVENC-H.264, and
NVENC-HEVC independently (plus software, always available for both
families) and returns all six as `EncoderOption[]` via
`GET /api/codec-analysis/encoders`. None of these are inferred from device
presence or `ffmpeg -encoders` output — both can be true while the actual
path is broken (missing driver package, host permissions not passed
through, GPU generation too old, etc.) — each is confirmed with a real
1-second throwaway encode, cached for the process's lifetime (capability
doesn't change at runtime). `ConvertModal` filters this list to whichever
family the candidate actually needs (from its tier's preference — the
family itself isn't freely choosable, only *how* to encode to it) and
defaults to the best available hardware option, falling back to software.

This was deliberately generalized across two backends and both codec
families rather than hardcoding "H.264 via VAAPI", so it works on whatever
hardware an install has. The development machine is a good example of why
detection beats assumption: it has two GPUs, a 4th-gen (Haswell) Intel iGPU
(`i915` driver, `/dev/dri/renderD128`) and an NVIDIA GT 730M (Kepler) running
the open-source `nouveau` driver with no working NVENC. Live probe results
match the physical reality exactly: VAAPI-H.264 → available, **VAAPI-HEVC →
not available** (Haswell's Quick Sync only gained hardware HEVC encode
starting at Skylake — confirmed by a real failed test encode, not assumed
from the CPU generation), and both NVENC options → not available (no
CUDA/nvenc support in this Alpine ffmpeg build, and no working driver on
this GPU regardless). A newer Intel iGPU or a properly-driven NVIDIA card
lights up more options automatically, with zero code changes. NVENC is
untested on real hardware. `-qp 20` (VAAPI) / `-cq 20 -preset p5` (NVENC)
aren't precise equivalents of the software path's `-crf 20` — each is the
standard practical default for a comparable-quality encode on that backend.

Hardware encode needs a few things outside the code:
1. `--device /dev/dri` (or `devices: [/dev/dri]` in compose) — passes the
   render node into the container.
2. `RUN apk add ffmpeg libva-intel-driver mesa-va-gallium` (`Dockerfile`) —
   ffmpeg's Alpine package alone doesn't include a working VAAPI driver;
   `libva-intel-driver` (the classic i965 driver) supports Haswell — Intel's
   newer `intel-media-driver` (iHD) requires Broadwell or later.
   `mesa-va-gallium` is installed alongside as a fallback. Other GPUs may
   need a different driver package in a custom image.
3. **Rootless Podman only: `--group-add keep-groups`** (the image runs as a
   non-root user, so the example unit also uses `--userns=keep-id
   --user=%U:%G` to map it to your host user). Rootless Podman does
   not propagate the invoking host user's supplementary groups into the
   container by default. Even if that user is in the host's `render`/`video`
   groups, the device node shows up *inside* the container owned
   `nobody:nobody` and VAAPI fails to open it (`ffmpeg`: "No VA display
   found for device"). `--group-add keep-groups` carries the host group
   membership through. Docker users instead use `group_add` with the
   render group's numeric GID (`getent group render`).
4. A persistent `/config` volume for `lib/settings.ts`'s codec preferences.

If hardware encode doesn't light up, the Codecs page shows each encoder's
availability along with the reason from the real test encode.

**File safety** (`runJob()` in `lib/convert-queue.ts`): ffmpeg writes to a
dotfile temp path in the same directory (`.wheelhouse-convert-<jobId>.ext`)
— hidden from Radarr/Sonarr's own library scanners, which ignore dotfiles,
so a mid-conversion scan can't pick up a half-written file as a second copy.
Only after ffmpeg exits 0 *and* the output file has nonzero size does
anything touch the original: it's renamed to `<original>.bak` (a
non-video extension so Radarr/Sonarr's scanners won't treat it as an
alternate file version), then the temp file is renamed into the original's
place. **The `.bak` file is never auto-deleted** — it's the safety net,
and cleanup is manual. On any failure or cancellation, only the temp file
is removed; the original is never touched. After a successful swap, Radarr's
`RescanMovie` / Sonarr's `RescanSeries` command (`/api/v3/command`) is
triggered so its own DB (size, mediaInfo, codec) picks up the new file —
otherwise the next codec-analysis run would still show the old codec.

**Restart recovery**: job state is in-memory only (see "Job model" above),
so a container restart mid-conversion loses all track of what was running
— confirmed the hard way mid-development, when a redeploy killed a live
conversion twice. There's no attempt to resume an interrupted encode
("ditch and start over" — per-request); instead, `cleanupOrphanedArtifacts()`
runs once per process startup (lazily, on first use of the convert routes,
guarded via `globalThis` same as the job queue) and walks `/movies` and
`/tv` for two leftover states: any `.wheelhouse-convert-*` temp file gets
deleted outright (safe — nothing else references it); a `<file>.bak` whose
corresponding `<file>` is *missing* means the process was killed in the
narrow window between the two swap-renames in `runJob()` (original → .bak,
then temp → original), so the real content is stranded at the backup path
— self-healed by renaming it straight back. A `.bak` *with* a live `<file>`
next to it is a normal, intentional post-success backup and is left alone.

Progress is parsed from ffmpeg's `-progress pipe:1` stdout stream
(`out_time_ms` against a duration probed up front via `ffprobe`), polled by
the modal every 2s via SWR until the job reaches a terminal state.

## API routes (all server-side proxies — API keys never reach the browser)

- `GET /api/features` — the feature registry's answer (see "Feature
  registry"): which pages are available and what the others still need. No
  hosts, keys or instance names.
- `GET /api/status` — `{ instances: [...] }`, one entry per enabled
  instance with `id`, `providerId`, `role`, `name`, `state` (`ok`,
  `degraded`, `needs-setup`, `down`), `version`, `error`, `link` and the
  role's normalized `detail` (see the spec's "`/api/status` shape" for the
  state rules). Instances are queried in parallel and each fails
  independently (`state: "down"` + a redacted `error`, no `detail`) rather
  than taking the route down; a misconfigured instance is `down` too. Plex
  degrades in stages: unclaimed → `needs-setup`; no or rejected `PLEX_TOKEN`
  → `ok` with basic version info from the unauthenticated `/identity`
  endpoint and a `notice`; claimed + tokened → also server name and active
  session count via `/` and `/status/sessions`. Jellyfin follows the same
  stages with its own signals: setup wizard not completed → `needs-setup`; no
  or rejected `JELLYFIN_API_KEY` → `ok` with the unauthenticated
  `/System/Info/Public` data and a `notice`; with a working key, the name and
  the number of sessions that are playing something (not merely connected
  clients) via `/System/Info` and `/Sessions`. With nothing configured the
  answer is `{ instances: [] }`.
  Sonarr/Radarr's indexer summary (`{ total, enabled, issues }`) comes from
  `/api/v3/indexer` cross-referenced with `/api/v3/health` entries whose
  `source` starts with `Indexer` — there's no per-indexer live backoff/health
  endpoint in this API version (`/api/v3/indexerstatus` 404s on both
  Sonarr 4.0.15 and Radarr 5.26.2 despite being documented elsewhere), so
  this is the closest available proxy. SABnzbd's `warnings` excludes
  "Refused connection..." entries — those are the web UI's host whitelist
  rejecting client access, unrelated to server/download health, and would
  read as a false alarm on the card.
- `GET /api/downloads` — every configured download client's items in the
  normalized `DownloadItem` shape (bytes, `progress` 0..1, `state`,
  `protocol`), each tagged with its `instanceId`:
  `{ instances: [{ instanceId, name, protocol, up, paused?,
  speedBytesPerSec?, error? }], items }`. A client that fails (wrong login,
  down, half-configured) is reported in `instances` with its redacted error
  and doesn't affect the others. Deliberately separate from `/api/status` so
  it can be polled faster (5s vs 15s) without dragging the rest of the
  dashboard along.

  The dashboard sorts the rows by state (failed, post-processing or
  checking, downloading, stalled, queued, paused, seeding), draws at most 50
  and adds a "+N more" line, and puts a usenet/torrent badge on each. The pure
  helpers are in `lib/download-rows.ts`.

  qBittorrent (`lib/providers/download-client/qbittorrent.ts`) logs in with
  a cookie session (`POST /api/v2/auth/login`), keeps whatever cookie that
  sets, and signs in again once if a call comes back 403. Its torrent states
  map to the normalized ones as the spec's table says (v4's `paused*` and v5's
  `stopped*` names both work). Every torrent is listed except finished,
  stopped ones, so seeding torrents appear with their ratio and upload speed.
  `paused` is true only when every unfinished torrent is stopped (qBittorrent
  has no queue-wide pause); `queueCount` counts torrents below 100 %. The spec
  documents the details and how v4 and v5 differ.

  A download doesn't disappear from here once the network transfer finishes
  — it keeps showing (amber, pulsing bar) through SABnzbd's post-processing
  phases (Extracting/Verifying/Repairing/Moving/Running) until it's fully
  done. This needed a real fix, not just reading the existing queue: SABnzbd's
  own `mode=queue` API **collapses every one of those phases down to
  "Downloading"** in the `status` field — confirmed by reading `build_queue()`
  in SABnzbd's `api.py` (not a guess), it's intentional API-compat behavior,
  not a bug. The accurate per-item status only appears in `mode=history`,
  which SABnzbd's own code comments describe as combining "the jobs still in
  post-processing and the database history" — the SABnzbd adapter
  (`lib/providers/download-client/sabnzbd.ts`) pulls the still-active
  (non-Completed/Failed) entries from there and overlays their real status
  onto the matching queue slot by `nzo_id`, surfacing it as
  `state: "postprocessing"` with the phase in `stateDetail`. Since the underlying nzo is the same
  object in both APIs, this never double-counts — it's an enrichment of the
  queue list, not a merge of two separate lists.
- `GET /api/storage` — `{ entries, instances }`. For every media-manager
  instance, its `/api/v3/rootfolder` entries (`freeSpace`) plus the
  `sizeOnDisk` of the library items inside that root folder (matched by path
  prefix on whole path segments, deepest root wins), so several root folders
  or instances don't each get the whole library's size. Note: the
  Sonarr/Radarr API doesn't expose a `totalSpace` field, so this reports free
  space + library-used space rather than a true used/total percentage. An
  instance that can't be read is listed in `instances` with `up: false` and
  its reason, and the others are still returned.
- `GET /api/library` — every instance's items merged: `{ items, instances }`
  where an item is `{ instanceId, instanceName, kind, id, title, sizeOnDisk,
  monitored, rootFolderPath, added }` (`added` is Sonarr/Radarr's own `added`
  field, an ISO timestamp of when the item was added — used for the
  Age column/sort). `instances` lists every configured media manager with
  `up`/`error`, so the page can say which one is down or that none exist.
  Ids are only unique within an instance: `instanceId` + `id` is the key.
- `DELETE /api/library/[instanceId]/[id]` — whole-item delete
  (`deleteFiles=true`) via Sonarr/Radarr's own delete endpoint. The default
  instances are `sonarr` and `radarr`; extras are `radarr-4k` and so on.
- `GET /api/library/[instanceId]/[id]/details` — powers the detail card:
  `{ title, overview, year, genres, runtime, certification, status, network,
  studio, posterUrl, fanartUrl, ratings }`. `posterUrl`/`fanartUrl` are the
  `remoteUrl` from each item's `images[]` (thetvdb.com / image.tmdb.org CDN
  URLs) rather than Sonarr/Radarr's own `/MediaCover/...` path, so the
  browser loads them directly with no proxying or API key needed. Radarr
  exposes multiple rating sources (imdb/tmdb/metacritic/trakt) as an object
  keyed by source; Sonarr exposes one (TVDB) — both get normalized to the
  same `{ source, value, votes }[]` shape.
- `GET /api/library/[instanceId]/[id]/seasons` — series instances only. Returns
  `series.seasons[]` reshaped to `{ seasonNumber, monitored,
  episodeFileCount, episodeCount, totalEpisodeCount, sizeOnDisk }`. This data
  comes straight from the series resource — no extra episode-level calls
  needed.
- `DELETE /api/library/[instanceId]/[id]/seasons/[season]` — series instances only. Looks
  up that series' episode files via `/api/v3/episodefile?seriesId=`, deletes
  each file individually (`DELETE /api/v3/episodefile/{id}`), then `PUT`s the
  series back with that season's `monitored` flag set to `false` — so Sonarr
  doesn't immediately re-grab what you just cleared.
- `GET /api/search?kind=movies|series&term=` — proxies the lookup endpoint of
  every instance of that kind, normalized to `{ results, problems? }` with
  `results` = `{ instanceId, instanceName, id, alreadyAdded, title, year,
  overview, posterUrl, subtitle }[]` (`id` is the external tvdbId/tmdbId —
  what you POST to `/api/add`; `subtitle` is network for series, studio for
  movies). Capped at 8 results per instance, so a movie in two Radarrs shows
  up once per instance, each with its own `alreadyAdded`. An instance that
  fails goes to `problems`; if all fail the route answers 502.
- `GET /api/add-meta?instanceId=` (or `?kind=movies|series` for the first
  ready instance of that kind, which is what Explore uses) —
  `{ instanceId, instanceName, qualityProfiles: [{id, name}], rootFolderPath }`
  for populating the add form. The first root folder is used directly rather
  than exposed as a choice.
- `POST /api/add` — body `{ instanceId, id, qualityProfileId, rootFolderPath,
  monitored, searchNow, minimumAvailability? }`. Re-fetches the canonical
  item by external id (see "Add to library" above) and creates it.
  `qualityProfileId` comes from `AddQualityModal`'s per-add confirmation
  step, not a page-level control (see "Add to library" above).
- `GET /api/explore?category=theaters|bluray|popular[&limit=N][&page=P]` —
  see "Explore" above. `limit` defaults to 20 (max 60); `page` is 1-based in
  units of `limit`. Returns `{ results: [{ id, alreadyAdded, title, year,
  overview, posterUrl, voteAverage }], hasMore }` (`id` is the tmdbId, same as `/api/search`'s radarr
  results — both feed the same `/api/add`). `alreadyAdded` is true when the
  movie is in any Radarr instance; an instance that can't be read is simply
  left out of that check.
- `GET /api/explore/[id]` — one movie's full detail + trailer for
  `TmdbDetailModal`: `{ id, alreadyAdded, title, overview, year, genres,
  runtime, voteAverage, posterUrl, backdropUrl, trailerKey }`. `trailerKey`
  is a YouTube video id or `null`; the frontend embeds it directly
  (`youtube.com/embed/<key>`) rather than linking out.
- `GET /api/codec-analysis?kind=movies|series` — see "Codec Analysis"
  above, across every instance of that kind. Returns `{ totalCount, totalSize, preferences: { hd, uhd },
  buckets: { h264, hevc, legacy }, candidates: [{ title, codec, resolution,
  bitDepth, size, instanceId, itemId, filePath, tier, targetFamily, reason }],
  matching: [...], skippedCount, problems? }`. Read-only — doesn't touch media files.
  `preferences` reflects whatever's currently persisted, so the UI doesn't
  need a second round-trip to know what drove the candidate list.
- `GET`/`PUT /api/codec-analysis/settings` — `{ hd: "h264"|"hevc", uhd:
  "h264"|"hevc" }`. `PUT` validates both fields and persists to
  `/config/settings.json` (`lib/settings.ts`); `GET` returns the defaults
  (`hd: "h264"`, `uhd: "hevc"`) if the file doesn't exist yet.
- `GET /api/codec-analysis/encoders` — `{ encoders: [{ backend: "software"
  |"vaapi"|"nvenc", family: "h264"|"hevc", label, available, reason? }] }`
  — six entries (software × 2 families always available, VAAPI × 2 and
  NVENC × 2 probed live). See "Hardware encoding" above — `available`
  reflects a real test encode, not just a capability guess.
- `POST /api/codec-analysis/convert` — body `{ instanceId, itemId, filePath,
  title, originalSize, backend: "software"|"vaapi"|"nvenc", targetFamily:
  "h264"|"hevc" }` (straight from a candidate's `instanceId`/`itemId`/
  `filePath`/`title`/`size`/`targetFamily` plus the modal's encoder-backend
  choice). Enqueues a conversion job and returns it immediately (`status:
  "queued"`); doesn't block on the encode. If a job is already
  queued/running for that exact `filePath`, returns *that* job instead of
  starting a duplicate — both a safety guard (a second job would clobber
  the first's `.bak`) and how the UI reattaches after closing the modal.
  `GET` lists all jobs (queued/running/recently finished) from the
  in-memory queue.

  Before queuing, the route `stat`s the file (`lib/hardlink.ts`). If it has
  more than one hard link it may also be the copy a torrent client is seeding,
  so the route answers `409 { error, code: "hardlinked", linkCount }` and
  starts nothing, unless the body carries `acknowledgeHardlink: true`.
  `ConvertModal` shows a plain-language warning on that 409 (the conversion
  replaces the library file with a new one, so the seeding copy stays and disk
  use roughly doubles) and retries with the flag when the user chooses
  "Convert anyway". A path that already has an active job skips the check.
- `GET /api/codec-analysis/convert/[jobId]` — one job's live status:
  `{ id, instanceId, itemId, filePath, title, originalSize, backend,
  targetFamily, status, progressPercent, error?, newSize?, backupPath?,
  log, createdAt, startedAt?, finishedAt? }`. Polled by `ConvertModal`
  every 2s until `status` is `success`/`failed`/`canceled`.
- `POST /api/codec-analysis/convert/[jobId]/cancel` — cancels a queued job
  outright, or kills a running ffmpeg process for a running one. 400 if the
  job doesn't exist or already finished.

`lib/providers/media-manager/` holds the Sonarr and Radarr adapters
(`sonarr.ts`, `radarr.ts`) on top of `arr-http.ts`, which adds `X-Api-Key`
and the `/api/v3` prefix and turns an HTTP 401 into an "API key incorrect"
error; `instances.ts` lists the configured instances of a kind for the routes.
`lib/providers/download-client/sabnzbd.ts` is the
SABnzbd adapter (query-param API style; a wrong key is an HTTP 403 with a
plain-text body); `lib/providers/download-client/qbittorrent.ts` is the
qBittorrent adapter (cookie session; a wrong login is a 401 on v5 and a 200
"Fails." on v4); `lib/providers/media-server/plex.ts` is the Plex adapter
(`Accept: application/json`, `X-Plex-Token` header for anything beyond
`/identity`); `lib/providers/media-server/jellyfin.ts` is the Jellyfin adapter
(`Authorization: MediaBrowser Token="<key>"` for anything beyond
`/System/Info/Public`; Jellyfin 12 rejects `X-Emby-Token`);
`lib/providers/metadata/tmdb.ts` is the TMDB provider (role `metadata`, no
shared interface: Explore's categories are TMDB-specific). It sends the key as
the `api_key` query parameter, which is TMDB's own convention, so errors never
echo request URLs and the key is redacted from them. `lib/tmdb-client.ts` holds
Explore's list and detail calls on top of that client, which it gets from the
runtime (`getTmdbClient()`); there is no `lib/env.ts` any more.

## Feature registry

`lib/features/registry.ts` is a pure module (no server-only imports, no zod)
that declares what each page needs: Dashboard nothing; Library any media
manager; Explore a metadata provider and a media manager of kind `movies`;
Codecs any media manager and ffmpeg. `getAvailableFeatures(instances, env)`
returns each feature with `available` and the unmet requirements. Only
**ready** instances count (valid config), so a half-configured Radarr doesn't
unlock anything. ffmpeg is probed once (`lib/features/ffmpeg.ts`) and cached.

`GET /api/features` turns that into `{ features: [{ id, label, route,
available, missing: ["a Radarr instance", ...] }] }`. The header nav
(`components/FeatureNav.tsx`) shows Dashboard plus the available features, the
global add/search box (`components/AddSearchGate.tsx`) appears only when the
library feature is available, and `app/{library,explore,codecs}/layout.tsx`
wrap their pages in `components/FeatureGate.tsx`, which renders a stub saying
what is missing, with a link to the README's Configuration section, for a
direct visit to an unavailable page.

## Config

Server-side-only env vars (never sent to the client). All are optional; a
service that isn't configured is reported as such instead of breaking the app:

```
SONARR_URL, SONARR_API_KEY
RADARR_URL, RADARR_API_KEY
SABNZBD_URL, SABNZBD_API_KEY
QBITTORRENT_URL, QBITTORRENT_USERNAME, QBITTORRENT_PASSWORD
PLEX_URL, PLEX_TOKEN
JELLYFIN_URL, JELLYFIN_API_KEY
TMDB_API_KEY
```

Extra Sonarr/Radarr instances use `RADARR_<ID>_URL` and
`RADARR_<ID>_API_KEY` (same for `SONARR_`), e.g. `RADARR_4K_URL` is the
instance `radarr-4k`, shown as "Radarr 4K". This is the interim way to
configure several instances until configuration moves to a database.

Sonarr/Radarr responses for the library lists are cached for 5 seconds per
instance (concurrent requests share one upstream call; adding, deleting or
rescanning clears that instance's cache; errors are never cached).

`PLEX_URL`/`PLEX_TOKEN`, `JELLYFIN_URL`/`JELLYFIN_API_KEY` and `TMDB_API_KEY` degrade gracefully without them.
`PLEX_TOKEN` can't be known until the Plex server has been claimed via its own web UI — it lives in Plex's own
`Preferences.xml` (`PlexOnlineToken` attribute) once that's done, under the
server's `Plex Media Server` application-support directory. `TMDB_API_KEY`
is a free API key from themoviedb.org (account settings → API) — only used
by `/explore`, which stays hidden without it; the rest of the app works fine.

`JELLYFIN_API_KEY` is created in Jellyfin under Dashboard → API Keys.

Keep the env file out of version control and readable only by the user
running the container (mode 600). The Dockerfile sets `HOSTNAME=0.0.0.0` so
the Next.js standalone server binds all interfaces; without it, it can end
up bound to a single interface (we saw it scoped to a Tailscale interface
under `--network=host`).

## Deployment

See the README for Docker Compose and Podman/systemd examples. The container
listens on port 3050.
