# Wheelhouse Go server

The Wheelhouse server: one static Go binary (standard library only) that serves
the API and the embedded UI. It replaced the Next.js app in release 0.2.0
([docs/wheelhouse-spec-phase2.md](../docs/wheelhouse-spec-phase2.md), migration
steps 2 to 8). Comments in the code that mention `lib/...` or `app/...` refer to
that app, which is in git history (tag `0.1.1`).

What it serves:

- `GET /healthz` returns `{"status":"ok","version":"<version>"}`.
- `GET /api/features` gates Dashboard, Library, Explore and Codecs on the
  configured instances and on `ffmpeg` (probed once per process).
- `GET /api/status` returns one entry per media manager, download client and
  media server: `state` (`ok`, `degraded`, `needs-setup`, `down`), `version`,
  `detail`, the reason when down, and a server-computed `link`
  (`{protocol, port?, path}`, never host, credentials or query).
- `GET /api/downloads` returns `instances` (every client, `up` false with a
  reason when it cannot answer) and normalized `items`.
- `GET /api/explore?category=theaters|bluray|popular[&limit=1..60][&page=n]`
  returns `results` (TMDB movies with `alreadyAdded`, true when any Radarr
  instance has the movie) and `hasMore`. `GET /api/explore/{id}` returns one
  movie's detail with `genres`, `runtime`, `backdropUrl` and `trailerKey`. A
  category that is not one of the three or an `id` that is not an integer is a
  400. TMDB not configured, rejected, down or slow is a 502 with a redacted
  message; a Radarr that cannot answer just does not count as "in library".
- Any other `/api/...` path is a JSON 404.
- Everything else is the embedded UI, with a fallback to `index.html` for paths
  that are not files. It is the Vite build of `web/` when one was copied into
  `internal/ui/dist` (`make ui`, or `npm run build && npm run embed` in `web/`;
  not committed), and otherwise the committed placeholder page
  (`internal/ui/placeholder`), so a clean checkout still builds and tests.

Middleware on every response (`internal/middleware`): a request ID
(`X-Request-Id`), security headers (a CSP with no inline script or style, images only from `self`,
`data:`, `image.tmdb.org` and `artworks.thetvdb.com`,
`nosniff`, `Referrer-Policy: no-referrer`, `frame-ancestors 'none'`), an access
log that never records query strings, headers or bodies, and panic recovery that
answers a generic 500 and never logs the panic value.

## Configuration

| Variable   | Default   | Meaning                  |
| ---------- | --------- | ------------------------ |
| `PORT`     | `3000`    | TCP port (the image sets `3050`) |
| `WHEELHOUSE_LISTEN` | `0.0.0.0:$PORT` | Address to listen on, `host:port` (`:3050`, `127.0.0.1:3050`, `[::]:3050`); overrides `PORT` |
| `WHEELHOUSE_MEDIA_ROOTS` | `/movies:/tv` | Folders conversion may touch, separated like `PATH` (issue #31) |

`HOSTNAME` is **not read**. Container runtimes set it to the container id (and
to the machine's name with host networking), so using it as the bind address
broke the listener; the old Next image forced `HOSTNAME=0.0.0.0` for the same
reason. Settings are saved to `/config/settings.json`.

The services are configured with the same variables as the 0.1.x Next app (`SONARR_URL`,
`SONARR_API_KEY`, ..., `TMDB_API_KEY`; see the main README). A service URL must
not contain `user:pass@`: such an instance is an invalid config.

## Routes and access levels

Routes are registered only through `app.Router`, which makes each one state its
access level (`Public`, `Authenticated`, `Admin`; all enforced as public until
Phase 2). Add a route group as a `register...(r *Router, d deps)` function called
from `Routes`:

```go
r.HandleFunc("GET /api/status", Public, handler)
```

`router_test.go` enumerates the registered routes and fails if one has no valid
level or is not listed there; a route registered with a missing level panics at
startup. `app.Options{Source: ...}` (or `Runtime`) injects the config source, so
tests build the whole app over fake services. Provider wiring lives in
`internal/providers/all`.

Library routes (`routes_library.go`, logic in `internal/library`): the two
`DELETE /api/library/...` routes are `Admin` and require `?confirm=true`
(issue #41), otherwise 400 and nothing is sent upstream; each confirmed delete
logs an `audit` line. The UI in `web/` sends `confirm` after its own confirm dialog.

The server stops gracefully on SIGINT or SIGTERM (in-flight requests get 10 s).

## Codecs and the converter

`GET /api/codec-analysis` (library analysis across instances, with `problems`),
`GET`/`PUT /api/codec-analysis/settings` (`/config/settings.json`),
`GET /api/codec-analysis/encoders` (real throwaway encodes, cached),
`GET`/`POST /api/codec-analysis/convert`, `GET /api/codec-analysis/convert/{jobId}`
and `POST /api/codec-analysis/convert/{jobId}/cancel` are in
`internal/codecs` and `internal/converter`.

- ffmpeg and ffprobe run through `os/exec` with a context, never a shell; the
  arguments come from validated enums and an absolute, resolved path.
- A file path in a request is resolved (`..` refused, `EvalSymlinks`, regular
  file) and must be inside a media root (`converter.DefaultMediaRoots`,
  `WHEELHOUSE_MEDIA_ROOTS`), or the request is a 400. The check repeats when the
  job starts and just before the swap.
- A file with more than one hard link answers 409 `{code: "hardlinked",
  linkCount}` until the body has `acknowledgeHardlink: true`.
- The output is written to `.wheelhouse-convert-<id><ext>` next to the original,
  forced to 8-bit (`yuv420p`) with audio and subtitles copied; a non-empty result
  replaces the original, which stays as `<file>.bak`. On start-up, leftover temp
  files are removed and a `.bak` whose file is missing is renamed back.
- Tests use fake `ffmpeg`/`ffprobe` shell scripts that print `-progress` output
  and exit with real codes (success, failure, cancel, empty output); a test runs
  the real ffmpeg when the machine has one (with `libx264`) and skips otherwise.
- HDR (`VideoDynamicRange` in `mediamanager.MediaInfo`) is deliberately not used
  yet (issue #32); the queue is in memory behind `converter.Queue` (issue #33).

## Commands

`wheelhouse` runs the server. `wheelhouse healthcheck` exits 0 when the server on
this machine (at `WHEELHOUSE_LISTEN` or `PORT`) answers `/healthz`, for the
image's `HEALTHCHECK`. `wheelhouse version` (or `--version`) prints the version,
which `-ldflags "-X github.com/tristanwl/wheelhouse/server/internal/version.Version=0.2.0"`
sets (the Dockerfile reads it from the root `package.json`; a plain `go build`
says `dev`). The version is also in `/healthz` and the startup log line.

## Container image

The root `Dockerfile` builds the web UI, then this binary with the UI embedded,
then an Alpine image with ffmpeg and the VA-API drivers; see the main README for
running it and `docs/architecture.md` for the reasoning. The binary needs a
writable `/config` (settings) and writes converted files next to the originals
under the media roots; it uses `TMPDIR`/`/tmp` for nothing of its own, and the
GPU drivers cache under `$HOME` (`/tmp` in the image), so a read-only root
filesystem with a tmpfs `/tmp` works.

## Build, run, test

Needs Go 1.27+ (see `go.mod`). From `server/`:

```sh
make build        # static, CGO-free binary ./wheelhouse (-trimpath -s -w)
make run          # go run ./cmd/wheelhouse
make test         # go test -race ./...
make check        # gofmt, vet, test, golangci-lint, govulncheck, build (what CI runs)
```

Linting and vulnerability scanning use two tools installed separately:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
```

Without Make, the raw commands are:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o wheelhouse ./cmd/wheelhouse
go vet ./... && go test -race ./... && golangci-lint run && govulncheck ./...
```

## Acceptance suite

The black-box suite in [../acceptance](../acceptance/README.md) targets the
binary (and the container image):

```sh
make build
cd .. && npm ci && npm run test:acceptance     # defaults to ./server/wheelhouse
```

Its fakes and fixtures (`acceptance/fakes`) are the source of the fixture copies
in the `testdata` directories of the Go fakes; a test next to each copy fails
when they differ.

## Provider core

`internal/providers/core` (package `core`): roles, provider definitions and registry, config
validation, config sources, the runtime and the HTTP client. It is a library
for the providers and routes; the app builds its runtime over it.

```go
reg, err := core.NewRegistry(core.Definition{
    ID: "sonarr", Role: core.RoleMediaManager, DisplayName: "Sonarr",
    Config: SonarrConfig{}, // zero value of the settings struct
    Create: func(cfg any, pc core.ProviderContext) any { return newSonarr(cfg.(SonarrConfig), pc) },
})
rt := core.NewRuntime(reg, core.NewProcessEnvConfigSource())
clients, err := rt.Clients(ctx, core.RoleMediaManager) // ready instances of a role
```

A provider's settings are a struct; tags drive validation, secret masking and the
JSON Schema the admin UI renders (`Definition.Schema()`):

```go
type SonarrConfig struct {
    URL    string `json:"url" label:"URL" config:"required,url"`
    APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
}
```

Adapters make requests through `core.NewHTTPClient(core.HTTPOptions{BaseURL,
Label, Headers, Secrets})` and `Do`/`JSON`. Defaults and guarantees:

- 8 s timeout per request (body included), `Request.Timeout` overrides it.
- At most 64 MiB read from upstream (`MaxResponseBytes`, `Request.MaxBytes`); a
  larger response is a `ProviderError`, and the stream is cut off at the limit.
- Redirects are followed only within the same host name, never from https to
  http; a redirect to another host is a `ProviderError` and is not followed.
- TLS verification is always on. There is no insecure option (the 0.1.x
  TypeScript `allowInsecureTls` was deferred and never built); a self-signed certificate is
  reported as "TLS certificate could not be verified".
- Errors are `*core.ProviderError{Message, Status}`, built from the label, the
  path without its query and the numeric status. They are redacted: configured
  secrets (plain, URL-encoded, base64, JSON-escaped), `apikey=` style
  parameters and `user:pass@` URLs. Upstream bodies and reason phrases are never
  echoed or logged. The optional `Logger` gets one debug line per failed request.
- `Request.CacheTTL` caches a GET for that long, concurrent identical requests
  share one upstream call (even if the first caller cancels), errors are not
  cached, `Invalidate()` clears it (a read in flight at that moment is not
  cached). Each caller gets its own copy of the response.

`EnvConfigSource` reads the same variables as the 0.1.x Next app (`SONARR_URL`,
`SONARR_API_KEY`, ..., `TMDB_BASE_URL`, and `RADARR_4K_URL` style extras).
`Instance.Config` holds secrets and is excluded from JSON, `fmt` and `slog`.

Tests use `httptest` fake upstreams. `canary_test.go` plants secrets and sweeps
every error string, JSON description and log line the core can produce for them.

## Layout

```
cmd/wheelhouse        main: config, signals, listener, healthcheck and version commands
internal/app          routes, middleware chain, graceful Serve
internal/config       listen address (PORT, WHEELHOUSE_LISTEN)
internal/features     feature gating
internal/status       GET /api/status logic
internal/downloads    GET /api/downloads logic
internal/codecs       codec analysis and preferences (/config/settings.json)
internal/converter    ffmpeg queue, encoder detection, path containment, hardlink check
internal/library      library, storage, search, add-meta, details, seasons bodies
internal/explore      GET /api/explore and /api/explore/{id} logic
internal/providers/all  the registry of every provider
internal/middleware   request ID, security headers, access log, recovery
internal/providers/core  provider core
internal/ui           embedded UI
internal/version      the release version
```

Adding a dependency needs a reason in the PR; use the newest version
(`go list -m -u all`) and run `govulncheck ./...`.
