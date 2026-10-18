# Wheelhouse Go server

The Go server that will replace the Next.js app, built alongside it per
[docs/wheelhouse-spec-phase2.md](../docs/wheelhouse-spec-phase2.md) (migration
step 2). It is a skeleton: the standard library only, no providers yet.

What it serves today:

- `GET /healthz` returns `{"status":"ok"}`.
- `GET /api/features` returns what the Next app returns with nothing configured.
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

Most scenarios fail until the providers and routes are ported (steps 3 to 5).

## Layout

```
cmd/wheelhouse        main: config, signals, listener
internal/app          routes, middleware chain, graceful Serve
internal/config       environment settings
internal/features     feature gating (port of lib/features/registry.ts)
internal/middleware   request ID, security headers, access log, recovery
internal/ui           embedded UI
```

Adding a dependency needs a reason in the PR; use the newest version
(`go list -m -u all`) and run `govulncheck ./...`.
