# Wheelhouse Go server

The Go server that will replace the Next.js app, built alongside it per
[docs/wheelhouse-spec-phase2.md](../docs/wheelhouse-spec-phase2.md) (migration
steps 2 to 5). The standard library only; the provider core and all providers
are in place (see below) and the API routes are being ported.

What it serves today:

- `GET /healthz` returns `{"status":"ok"}`.
- `GET /api/features` gates Dashboard, Library, Explore and Codecs on the
  configured instances and on `ffmpeg` (probed once per process).
- `GET /api/status` returns one entry per media manager, download client and
  media server: `state` (`ok`, `degraded`, `needs-setup`, `down`), `version`,
  `detail`, the reason when down, and a server-computed `link`
  (`{protocol, port?, path}`, never host, credentials or query).
- `GET /api/downloads` returns `instances` (every client, `up` false with a
  reason when it cannot answer) and normalized `items`.
- Any other `/api/...` path is a JSON 404.
- Everything else is the embedded UI: a placeholder page (`internal/ui/dist`),
  with a fallback to `index.html` for paths that are not files. Replaced by the
  Vite build in a later step.

Middleware on every response (`internal/middleware`): a request ID
(`X-Request-Id`), security headers (a CSP with no inline script or style,
`nosniff`, `Referrer-Policy: no-referrer`, `frame-ancestors 'none'`), an access
log that never records query strings, headers or bodies, and panic recovery that
answers a generic 500 and never logs the panic value.

## Configuration

| Variable   | Default   | Meaning                  |
| ---------- | --------- | ------------------------ |
| `PORT`     | `3000`    | TCP port                 |
| `HOSTNAME` | `0.0.0.0` | Address to listen on     |

The services are configured with the same variables as the Next app (`SONARR_URL`,
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

The server stops gracefully on SIGINT or SIGTERM (in-flight requests get 10 s).

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

The black-box suite in [../acceptance](../acceptance/README.md) can target the
binary:

```sh
make build
cd .. && WHEELHOUSE_CMD=./server/wheelhouse npx vitest run \
  --config acceptance/vitest.config.mts boot -t "page |reports only the Dashboard|unknown routes"
```

Most scenarios fail until the providers and routes are ported (steps 4 and 5).

## Provider core

`internal/providers/core` (package `core`) is the Go port of
`lib/providers/core`: roles, provider definitions and registry, config
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
- TLS verification is always on. There is no insecure option (the TypeScript
  `allowInsecureTls` was deferred and never built); a self-signed certificate is
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

`EnvConfigSource` reads the same variables as the Next app (`SONARR_URL`,
`SONARR_API_KEY`, ..., `TMDB_BASE_URL`, and `RADARR_4K_URL` style extras).
`Instance.Config` holds secrets and is excluded from JSON, `fmt` and `slog`.

Tests use `httptest` fake upstreams. `canary_test.go` plants secrets and sweeps
every error string, JSON description and log line the core can produce for them.

## Layout

```
cmd/wheelhouse        main: config, signals, listener
internal/app          routes, middleware chain, graceful Serve
internal/config       environment settings
internal/features     feature gating (port of lib/features/registry.ts and server.ts)
internal/status       GET /api/status logic (port of lib/status.ts and its route)
internal/downloads    GET /api/downloads logic
internal/providers/all  the registry of every provider
internal/middleware   request ID, security headers, access log, recovery
internal/providers/core  provider core (port of lib/providers/core)
internal/ui           embedded UI
```

Adding a dependency needs a reason in the PR; use the newest version
(`go list -m -u all`) and run `govulncheck ./...`.
