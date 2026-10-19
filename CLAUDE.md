# Wheelhouse

A self-hosted dashboard and companion for the *arr media stack: status, library
management, movie discovery and codec-aware conversion. Alpha: there is **no
authentication yet**, so it is for trusted networks only.

Read [docs/architecture.md](docs/architecture.md) for how things work and
[docs/wheelhouse-spec.md](docs/wheelhouse-spec.md) for decisions, positioning
and the roadmap. [CONTRIBUTING.md](CONTRIBUTING.md) covers the human workflow.

## Layout and commands

A Go server (`server/`, standard library plus one justified dependency, the SQLite driver, Go 1.27+) with a Vite + React UI
(`web/`, own `package.json`) embedded in the binary, shipped as one Docker image
with ffmpeg. The root `package.json` is only tooling for the black-box
acceptance suite (`acceptance/`) and the **single source of the release
version**. Node 22.12+ (24 recommended, see `.nvmrc`).

```sh
make -C server run                         # API on :3000; configure with the usual env vars
cd web && npm ci && npm run dev            # UI on :5173, proxies /api to :3000
```

Before opening a PR, run what CI runs for what you touched (jobs `check`,
`acceptance`, `server`, `web`, `docker`):

```sh
make -C server check                       # gofmt, vet, go test -race, golangci-lint, govulncheck, deps (tidy, licenses), build
cd web && npm ci && npm run typecheck && npm run lint && npm run format:check \
  && npm test && npm audit && npm run build && npm run embed    # then: npm run test:smoke (Chromium)
npm ci && npm run typecheck && npm run format:check && npm audit  # root tooling
make -C server build && npm run test:acceptance                   # needs the UI embedded for a real run
docker build -t wheelhouse:dev . && acceptance/docker-smoke.sh wheelhouse:dev
```

`npm run embed` (in `web/`) copies the UI build into `server/internal/ui/dist`
(never committed) for `go build`; without it the binary serves a placeholder
page, so `go build` and `go test` work from a clean checkout. See
`server/README.md`, `web/README.md` and `acceptance/README.md`.

The built server must also boot with **no** environment variables (every page and
API answers 200; only the Dashboard is enabled); the acceptance suite's boot
scenario and `acceptance/docker-smoke.sh` check it. The acceptance suite is the
definition of "the API works": a new route must be added to `acceptance/routes.ts`
(and to the Go router test, which lists every route with its access level).

A UI change must keep the CSP (no inline script/style) and send `?confirm=true`
on library deletes only after its confirm dialog.

## How it is built

Today the server itself keeps no database (the config is the environment plus
`/config/settings.json`); `internal/store` and `internal/secrets` are the tested
foundation for Phase 2 and nothing in the server process opens them yet (only
the `backup` and `rekey` commands and tests do). Everything that talks to
another service goes through the provider layer in `server/internal/providers/`:

- **Roles:** `downloadClient`, `mediaManager`, `mediaServer`, `metadata`. Each
  role has a package with the normalized model and a reusable `contract.go`
  suite every adapter must pass, and one package per provider.
- **A provider** is a `core.Definition` (id, role, a `Config` struct whose tags
  say required/secret/url, a `Create` factory) returned by its `Definition()` and
  listed in `providers/all`. Config comes from a `core.ConfigSource`: environment
  variables today (`core/envsource.go`), a database later.
- **Routes** (`internal/app`) never build a service client: they ask the runtime
  for the ready instances of a role and iterate them, so every route works with
  several instances (Radarr and Radarr 4K) and with none. Every route is
  registered with an access level (`Public`, `Authenticated`, `Admin`) through
  `app.Router`; `router_test.go` lists them all.
- **Features** (Dashboard, Library, Explore, Codecs) are gated by
  `internal/features`, which decides from the configured instances what the nav
  shows and which pages show a "not configured" stub.
- The UI's pure helpers live in `web/src/lib` (`status.ts`, `download-rows.ts`,
  `format.ts`).

## Rules that matter

- **Secrets never leave the server.** Make upstream calls with
  `core.NewHTTPClient` (give it the `Secrets`) and echo only `core.Redact`ed text.
  API keys, tokens and URL credentials must not appear in any response body,
  header, error message or log line. `providers/core/canary_test.go`, each
  provider's `canary_test.go` and `acceptance/secrets.test.ts` plant canary
  secrets and sweep every error string, route and log; when you add a route or
  provider, make sure it is covered, and never weaken them.
- **Adapters normalize.** Sizes in bytes, progress 0..1, durations in seconds,
  states from the shared enums. An unreachable service returns a `ProviderError`
  (the caller reports it as down); a wrong key is a clear "API key incorrect".
  User-facing notices never name configuration keys or env vars.
- **Fixtures come from real responses**, sanitized. Each service answers
  errors differently (SABnzbd: HTTP 403 plain text; Plex: 401 with an HTML page;
  qBittorrent v4 and v5 differ on login), so don't invent shapes: run the real
  service (Docker is fine), capture, then anonymize. The fixtures and the fake
  servers that replay them are in `acceptance/fakes/`, the single source: the Go
  fakes keep byte-identical copies in their `testdata/`, and a test in each fails
  when they drift.
- **This is a public repo.** No real hostnames, IPs, library titles, hashes,
  usernames or keys in code, fixtures, docs or tests. Use `example.com`, RFC 5737
  addresses (`192.0.2.x`) and placeholder titles ("Example Movie 1").
- **Destructive actions need an in-app confirm.** Library deletes need
  `?confirm=true` on the server and the UI's confirm dialog. Conversion keeps a
  `.bak` and warns when a file is hardlinked (it may be seeding). Don't add a
  delete or rewrite without both.
- **Environment variables are an interface.** Operators' env files and unit files
  are not versioned with the image: keep every existing name and meaning. The
  listen address comes from `PORT` and `WHEELHOUSE_LISTEN`, never from `HOSTNAME`
  (container runtimes set it).
- **The image must keep working as any `--user`, read-only, with all capabilities
  dropped.** Write only under `/config` and the media directories; anything else
  goes under `/tmp`. `acceptance/docker-smoke.sh` checks this.
- **Storage and secrets** (`server/internal/store`, `server/internal/secrets`).
  Open the database only through `store.Open` (it enforces mode 0600, WAL, foreign
  keys and the migration checks); schema changes are a new numbered file in
  `store/migrations/`, never an edit of an applied one. A secret column stores
  only `secrets.Cipher` envelopes sealed with `secrets.AD(table, id, field)` and is
  listed in `store.SecretColumns`; never log, print or return a `secrets.Key` or
  plaintext (use `secrets.Secret`), and keep the plaintext-on-disk canary test
  (`TestPlaintextNeverReachesDisk`) covering any new secret column. Update
  `docs/threat-model.md` when a PR changes a boundary or a mitigation.
- **Dependencies:** the Go server is the standard library plus `modernc.org/sqlite`
  and its pure-Go dependencies. Every added module needs a written reason in the
  PR (why not the standard library, why this one), the newest stable version
  (`go list -m -u all`; nothing published in the last two weeks), a clean
  `govulncheck`, a license that works with GPL-3.0 (`make -C server deps` checks
  an allow-list) and a tidy `go.mod`/`go.sum`. `modernc.org/libc` stays at the
  version the SQLite driver pins. Pin GitHub Actions by commit SHA and the Docker
  base images by digest.

## Adding a provider

1. Types in the role's package if the model needs anything new (prefer fitting
   the existing one; extend the role's `contract.go` suite if you change it).
2. `server/internal/providers/<role>/<name>/` with a `Definition()`, and add it to
   `providers/all/all.go`.
3. The env mapping in `core/envsource.go` (`envMappings`), plus the README config
   table, `.env.example` and the table in `acceptance/README.md`.
4. Fixtures and a fake server in `acceptance/fakes/<name>/`, a Go fake with
   `testdata/` copies and a drift test, adapter tests, and the role's contract
   suite run against it.
5. An integration test that calls the real routes through the env config source
   and the runtime with your fake configured, and the fake in
   `acceptance/fakes/index.ts` (`startStack`).
6. Add it to the canary sweeps and update `docs/architecture.md`.

## Commits, PRs and releases

- Sign off every commit (`git commit -s`, DCO). Small, focused PRs; describe how
  you tested it and against what. Keep the formatting-only commit separate from
  behaviour changes.
- Don't add "Generated with Claude Code" (or similar) footers to commits, PR
  descriptions or issues.
- Merge with a merge commit, not a squash, so separate commits stay separate.
- Only a maintainer cuts releases. The tag (or GitHub release) is the bare
  version with no `v` prefix (`0.2.0`) and **must equal the root `package.json`'s
  `version`**: bump it in a PR first. `.github/workflows/release.yml` runs the
  CI checks, builds the Dockerfile (the binary reports that version), pushes
  `tristanwl/wheelhouse:<version>` to Docker Hub and creates or updates the
  GitHub release. Every `0.x` is a pre-release, and there is no `latest` tag
  until authentication ships. Branch protection requires the CI job named
  `check`, so don't rename the jobs.
- Claude Code keeps isolated worktrees in `.claude/`; it is ignored by git. Never
  commit it.
