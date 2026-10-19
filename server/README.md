# Wheelhouse Go server

The Wheelhouse server: one static Go binary (standard library plus one
dependency, the pure-Go SQLite driver; see [Dependencies](#dependencies)) that
serves the API and the embedded UI. It replaced the Next.js app in release 0.3.0
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
| `WHEELHOUSE_MEDIA_ROOTS` | `/movies:/tv` | Folders conversion may touch, separated like `PATH`. Read-only seed: roots saved in the database (`PUT /api/admin/converter`) win; the response says which `source` is in force |
| `WHEELHOUSE_CONFIG_DIR` | `/config` | Directory for the database, the master key, the setup token and `settings.json`; it must exist, and not be writable by others |
| `WHEELHOUSE_DB` | `<config dir>/wheelhouse.db` | SQLite file (accounts, sessions, audit log); opened at start, fatal if it cannot be |
| `WHEELHOUSE_DENIED_NETWORKS` | link-local, cloud metadata | Comma-separated CIDRs or addresses provider clients refuse to connect to (default `169.254.0.0/16,fe80::/10,fd00:ec2::254`; `none` allows all; an unparsable entry is fatal). Private ranges and loopback are allowed unless listed |
| `WHEELHOUSE_ALLOWED_HOSTS` | | Comma-separated host names (`nas.example.lan`, `*.example.lan`) whose requests may get the local-network bypass, besides IP literals, `localhost` and single-label names (DNS-rebinding defence). Added to the list stored via `PUT /api/admin/network` (`allowedHosts`); an invalid entry is fatal |
| `WHEELHOUSE_TRUSTED_PROXIES` | | Comma-separated addresses and CIDR ranges whose forwarded headers are believed: `X-Forwarded-Proto: https` makes the session cookie `Secure`, `X-Forwarded-For`/`Forwarded`/`X-Real-IP` give the client address (right-most untrusted entry). Added to the trusted proxies stored via `PUT /api/admin/network` (default none; an unparsable entry is fatal) |
| `WHEELHOUSE_SECRET_KEY_FILE` | | File with the 32-byte master key (mode 0600 or 0400); first choice |
| `WHEELHOUSE_SECRET_KEY` | | The master key, base64 of 32 bytes; second choice |

`HOSTNAME` is **not read**. Container runtimes set it to the container id (and
to the machine's name with host networking), so using it as the bind address
broke the listener; the old Next image forced `HOSTNAME=0.0.0.0` for the same
reason. Settings are saved to `settings.json` in the config directory.

The services are configured with the same variables as the 0.1.x Next app (`SONARR_URL`,
`SONARR_API_KEY`, ..., `TMDB_API_KEY`; see the main README). A service URL must
not contain `user:pass@`: such an instance is an invalid config.

## Routes and access levels

Routes are registered only through `app.Router`, which makes each one state its
access level (`Public`, `Authenticated`, `Admin`), which the `Guard` enforces from
the session (see [Accounts and sessions](#accounts-and-sessions)). Add a route group as a `register...(r *Router, d deps)` function called
from `Routes`:

```go
r.HandleFunc("GET /api/status", Public, handler)
```

`router_test.go` enumerates the registered routes and fails if one has no valid
level, is not listed there with that level, or differs from `acceptance/routes.ts`; a route registered with a missing level panics at
startup. `app.Options{Source: ...}` (or `Runtime`) injects the config source, so
tests build the whole app over fake services. Provider wiring lives in
`internal/providers/all`.

Library routes (`routes_library.go`, logic in `internal/library`): the two
`DELETE /api/library/...` routes are `Authenticated` (users manage media) and require
`?confirm=true` (issue #41), otherwise 400 and nothing is sent upstream; each
confirmed delete is an `audit_log` row with the acting user. The UI in `web/` sends `confirm` after its own confirm dialog.

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
  file) and must be inside a media root (`converter.Settings`: the database,
  else `WHEELHOUSE_MEDIA_ROOTS`, else `/movies` and `/tv`), or the request is a
  400. The roots are read at every check, so a change applies without a restart.
  The check repeats when the job starts and just before the swap. Roots saved
  through `PUT /api/admin/converter` are validated (`converter.ValidateRoots`):
  absolute, no `..`, not `/` or a system folder (`/etc`, `/proc`, `/sys`, `/dev`,
  `/root`, `/boot`, `/bin`, `/sbin`, `/usr`, `/lib*`, `/run`, `/var/lib`) or the
  config directory, must exist and be a directory (symlinks followed), at most 32,
  duplicates merged, nested or overlapping roots refused.
- A file with more than one hard link answers 409 `{code: "hardlinked",
  linkCount}` until the body has `acknowledgeHardlink: true`.
- The output is written to `.wheelhouse-convert-<id><ext>` next to the original,
  forced to 8-bit (`yuv420p`) with audio and subtitles copied; a non-empty result
  replaces the original, which stays as `<file>.bak` (or `.bak.1`, `.bak.2`, ... when
  that name is taken: a backup is never overwritten). The swap is recorded in the
  database first; on start-up a swap that was recorded but not finished is
  restored from its recorded paths, and leftover temp files are removed. A stray
  `.bak` is never renamed back. Every ffmpeg and ffprobe call reads local files
  only (`-protocol_whitelist file`), ffmpeg never reads stdin, and the duration
  probe times out after 30 s.
- Tests use fake `ffmpeg`/`ffprobe` shell scripts that print `-progress` output
  and exit with real codes (success, failure, cancel, empty output); a test runs
  the real ffmpeg when the machine has one (with `libx264`) and skips otherwise.
- HDR and Dolby Vision (issue #32): conversion writes SDR 8-bit and does not tone
  map, so such files are not candidates (`codecs.AnalyzeWith`; classified from
  Radarr/Sonarr `videoDynamicRange`/`videoDynamicRangeType` and, at request time,
  from the file with `converter.ProbeHDR`: PQ or HLG transfer, Dolby Vision side
  data; a probe that fails or is not understood is `unknown`, never SDR) unless
  `includeHdr` is set. Then `POST .../convert` answers 409 `{code: "hdr"}` until
  the body has `acknowledgeHdr: true` (409 `{code: "hdr-excluded"}` when the
  switch is off). The runner probes again before ffmpeg and refuses an
  unacknowledged or no-longer-allowed HDR job. Candidates and jobs carry `hdr`:
  `none`, `hdr10`, `hlg`, `dolby-vision` or `unknown`.
- The queue (`converter.JobQueue`) is written through to the `conversion_jobs`
  table (`converter.SQLStore`, migration 0005) and recovered at start: jobs that
  were probing or running become `failed` with `interrupted: ...` and their temp
  files are cleaned, queued jobs resume in order, finished jobs are pruned
  (`RetainFinishedFor` 30 days, `RetainFinishedMax` 200). Every job records its
  owner (user id, `bypass` or `cli`), the hard-link and HDR acknowledgements and
  its HDR class; start, cancel and finish are audited (`conversion.start`,
  `conversion.cancel`, `conversion.finish`). Nothing read back from the database
  is trusted: enums are validated and the path is resolved against the current
  roots again before a job runs.
- `GET`/`PUT /api/admin/converter` (admin): `{mediaRoots, includeHdr,
  effectiveRoots, source}` with `source` one of `database`, `environment`,
  `default`. `PUT` takes `mediaRoots` (an empty list removes the saved roots) and
  `includeHdr`, either or both; audited as `converter.update`.

## Commands

`wheelhouse backup <file>`, `wheelhouse rekey` and `wheelhouse forget-secrets --yes`
are described under [Storage and secrets](#storage-and-secrets), and
`wheelhouse reset-password <user>` under [Accounts and sessions](#accounts-and-sessions).
`wheelhouse` runs the server. `wheelhouse healthcheck` exits 0 when the server on
this machine (at `WHEELHOUSE_LISTEN` or `PORT`) answers `/healthz`, for the
image's `HEALTHCHECK`. `wheelhouse version` (or `--version`) prints the version,
which `-ldflags "-X github.com/tristanwl/wheelhouse/server/internal/version.Version=0.3.0"`
sets (the Dockerfile reads it from the root `package.json`; a plain `go build`
says `dev`). The version is also in `/healthz` and the startup log line.

## Accounts and sessions

Phase 2 item 2 ([plan](../docs/wheelhouse-phase2-plan.md), [threat
model](../docs/threat-model.md), [architecture](../docs/architecture.md#accounts-and-sessions)):
the server enforces authentication. `internal/auth` is the account service (SQL
for `users`, `sessions` and `audit_log`, Argon2id, sessions, throttling, the setup
token); `internal/app/guard.go` is the single place the route levels, the
same-origin check, the CSRF token and the must-change rule are enforced;
`routes_auth.go` and `routes_users.go` are the API.

| Method and path | Level | |
|---|---|---|
| `GET /api/auth/setup` | public | `{"setupRequired": bool}` |
| `POST /api/auth/setup` | public | `{token, username, password}`; only with no accounts, only with the setup token; 201, 403 (token), 409 (done), 400 (policy) |
| `POST /api/auth/login` | public | `{username, password}`; 200 with the cookie and `{user, csrfToken, sessionId, expiresAt, absoluteExpiresAt}`; every failure is the same 401 `invalid username or password` |
| `POST /api/auth/logout` | authenticated | ends the session, clears the cookie |
| `GET /api/auth/session` | authenticated | the user, role, CSRF token and expiry |
| `POST /api/auth/password` | authenticated | `{currentPassword, newPassword}`; rotates the session, ends the others |
| `GET /api/auth/sessions`, `DELETE /api/auth/sessions/{id}` | authenticated | own sessions (no tokens) |
| `GET /api/users`, `POST /api/users` | admin | list; create `{username, password, role, mustChangePassword?}` |
| `PATCH /api/users/{id}` | admin | `{username?, role?, disabled?}`; the last admin is refused (409) |
| `POST /api/users/{id}/password` | admin | `{password}`; ends the user's sessions, forces a change at the next login |
| `DELETE /api/users/{id}` | admin | the last admin is refused (409) |
| `GET /api/users/{id}/sessions`, `DELETE /api/users/{id}/sessions` | admin | list; end all of that user's sessions |
| `GET /api/audit?limit=&before=` | admin | newest first, paged by `nextBefore` |

Bodies are strict JSON (`application/json`, unknown fields refused, 8 KiB cap).
Every other route is `authenticated`, except saving codec settings (`admin`). A
request is checked in this order: same-origin (state changes only), set-up (503
`setup required`), session (401 `authentication required`), CSRF token (403),
password-change-required (403 `password-change-required`), role (403 `forbidden`).

The clock and the Argon2 cost are constructor options of `auth.New`
(`auth.Options{Clock, Params}`), used by the tests; they are not environment
variables, so a running server cannot be told to skip a timeout. Tests of the
routes build the app with `accounts_test.go`'s helper (a temporary database and a
signed-in administrator) and send its cookie and `X-CSRF-Token`; an app built
without an account service answers 503 on every guarded route. The Go tests that
walk the whole router are `router_test.go` and `guard_test.go`; the black-box
equivalents are `acceptance/auth-*.test.ts`.

`wheelhouse reset-password <user>` sets a generated 20-character one-time password
(printed once, to stdout), sets `must_change_password`, ends the account's
sessions, lifts a login lockout (a password changed after the failures began
clears them) and records the reset in the audit log as `cli`. There is no
`create-admin`: the first administrator comes from the setup token flow only.

## Storage and secrets

The storage layer under the accounts ([plan](../docs/wheelhouse-phase2-plan.md),
item 1). The server opens the database and the master key at start (fatal, with a
message that says what to fix, if it cannot); stored service configuration, the
first user of the secret columns, is `internal/instances` (item 4, see
[docs/architecture.md](../docs/architecture.md#stored-configuration)). What to back up, and what
losing the key means, is in [docs/operations.md](../docs/operations.md).

### `internal/store`

SQLite through `modernc.org/sqlite`. `store.Open(ctx, path)`:

- creates the file with mode 0600 (and so its `-wal` and `-shm`), and refuses a
  missing or world-writable directory, a file that is not a regular file, and a
  database that group or others can access (with the `chmod` to run);
- opens a **writer** pool of one connection (`BEGIN IMMEDIATE`, so a read-then-write
  transaction cannot fail half way) and a **reader** pool of four `query_only`
  connections. Every connection has `foreign_keys=ON`, `busy_timeout=5000`,
  `secure_delete=ON` (deleted secrets are overwritten), `trusted_schema=OFF`,
  `cell_size_check=ON` and the driver's defensive mode; the writer is WAL with
  `synchronous=FULL`;
- applies pending migrations in one transaction (below);
- returns a `*DB` with context-aware `Exec` (writer), `Query`/`QueryRow` (readers),
  `Tx` (write transaction; a panic or error rolls back; do not call `db.Exec`
  from inside it, there is one writer connection) and `ReadTx` (one snapshot),
  `GetMeta`/`SetMeta`, `Backup` and `Close`.

`store.Options{Existing: true}` opens a database that must exist without
creating or migrating it; `backup` uses it so a copy taken before an upgrade is
of exactly what is there (and a database newer than the binary can still be
backed up).

**Migrations** are `internal/store/migrations/NNNN_name.sql`, embedded, numbered
from `0001` without gaps, forward-only. The `schema_migrations` table records
each one's version, name, SHA-256 and time; at every start the history must be a
prefix of what the binary embeds with matching hashes (an applied migration that
was edited is an error). A database whose newest migration is **newer than the
binary** is refused with `ErrSchemaTooNew`: restore a backup or run the newer
version. Pending migrations run in one `BEGIN IMMEDIATE` transaction, so two
processes starting together apply them once and a failure leaves nothing behind.
Migration `0001_meta` creates the `meta` key/value table; `0002_auth` creates
`users` (case-insensitive unique names, Argon2id hash, role, disabled and
must-change flags, with triggers that keep the last enabled administrator),
`sessions` (SHA-256 of the token, cascade on user delete) and the append-only
`audit_log`; `0003_instances` creates `instances` (non-secret JSON `config` plus
the sealed `secrets` BLOB, a registered secret column) and `feature_settings`. Never edit an applied
migration: add a new one.

### `internal/secrets`

AES-256-GCM from the standard library. `Cipher.Seal(plaintext, ad)` draws a
fresh random 12-byte nonce for every value and returns an **envelope**:

```
byte 0       format  0x01 = AES-256-GCM, 12-byte nonce, 16-byte tag
bytes 1-4    key version, big endian (the first key is 1)
bytes 5-16   nonce
bytes 17-    ciphertext, then the tag
```

The GCM additional data is the five header bytes followed by the caller's `ad`,
built with `secrets.AD(table, rowID, field)` (length-prefixed parts), so a value
copied to another row or field, or with a changed header, does not open. A new
algorithm gets a new format byte and old formats stay readable.

The **master key** is 32 random bytes, from the first of:

1. `WHEELHOUSE_SECRET_KEY_FILE`: a file of exactly 32 raw bytes, mode 0600 or 0400
   (symlinks are followed, as secret mounts use them; anything group- or
   world-accessible is refused with the `chmod` to run);
2. `WHEELHOUSE_SECRET_KEY`: base64 (standard alphabet) of 32 bytes;
3. `/config/secret.key`, created with `O_EXCL` and mode 0600 at the first start of the
   server or the first use by a command that owns the key.

A source that is set but unusable is an error; it never falls through to the
next. Make a key file with `head -c 32 /dev/urandom > key && chmod 600 key`.
Be clear about what this protects: with the generated file next to the database
it protects a copied database or backup, **not** a copy of the whole `/config`
volume. Point `WHEELHOUSE_SECRET_KEY_FILE` at a secret mount for more.
`secrets.Key` and `secrets.Secret` print as `[redacted]` under every `fmt` verb,
in `slog` and in JSON.

**Key versions.** The database stores one encrypted check value
(`meta.secret_key_check`); its envelope header carries the current key version
and only the right key opens it, so a wrong or swapped key is `ErrWrongKey`
at start instead of garbage later. The columns that hold secrets are listed in
`store.SecretColumns` (none yet; a test checks each exists and is a `BLOB`, and another that no account table is ever one).

### Backup, key rotation and key loss

Both run inside the container, as the container's user, with the same
environment as the server (`docker exec` bypasses the image's entrypoint, so the
binary is named):

```sh
docker exec wheelhouse wheelhouse backup /config/wheelhouse-backup.db
podman exec wheelhouse wheelhouse rekey
```

- `wheelhouse backup <file>` writes a consistent snapshot (SQLite `VACUUM INTO`)
  while the server runs, mode 0600, built under a temporary name and linked into
  place when complete. It **refuses to overwrite**, needs the directory to exist,
  does not create or migrate the database, and secrets in the copy are encrypted.
  The master key is **not** in it, and without it the stored provider credentials
  can never be recovered: keep `secret.key` (or your key file) elsewhere, see
  [docs/operations.md](../docs/operations.md#key-loss). The command's output says so.
  Restore by stopping Wheelhouse and copying the backup over `wheelhouse.db`
  (remove a leftover `-wal`/`-shm`).
- `wheelhouse rekey` makes a new master key (version n+1) and re-encrypts every
  stored secret **in one transaction**; any failure leaves the database and the
  old key untouched. Stop the server and take a backup first (the running server
  holds the old key and cannot read the new data). For the generated
  `secret.key` it stages `secret.key.next`, commits, then renames it over the key;
  if the process dies in between, the next start finishes or discards the swap
  (the stored check value says which key is right). For a key you supply
  (`WHEELHOUSE_SECRET_KEY_FILE` or `WHEELHOUSE_SECRET_KEY`) it cannot replace your
  mount, so run `wheelhouse rekey --new-key-out /config/new.key` and install that
  file as your key (for the environment form, `base64 -w0 new.key`), then delete
  it. The key is never printed. With no secrets stored it still works and moves
  the key version on (it creates the database and generated key if they do not
  exist; this command owns them). Old backups need the old key.
- `wheelhouse forget-secrets --yes` is the recovery from a key that is lost for
  good. The server refuses to start when the key does not match the database;
  this discards every value in the secret columns (nothing can decrypt them),
  replaces the key check by one under the key configured now, and prints how many
  values went. It refuses without `--yes`, and when the configured key does open
  the database. Users, sessions and the audit log are untouched.

## Container image

The root `Dockerfile` builds the web UI, then this binary with the UI embedded,
then an Alpine image with ffmpeg and the VA-API drivers; see the main README for
running it and `docs/architecture.md` for the reasoning. The binary needs a
`/config` that its user owns and nobody else can write (database, master key,
setup token, settings; it refuses to start from a world-writable one) and writes converted files next to the originals
under the media roots; it uses `TMPDIR`/`/tmp` for nothing of its own, and the
GPU drivers cache under `$HOME` (`/tmp` in the image), so a read-only root
filesystem with a tmpfs `/tmp` works.

## Build, run, test

Needs Go 1.27+ (see `go.mod`). From `server/`:

```sh
make build        # static, CGO-free binary ./wheelhouse (-trimpath -s -w)
make run          # go run ./cmd/wheelhouse
make test         # go test -race ./...
make check        # gofmt, vet, test, golangci-lint, govulncheck, deps (tidy + licenses), build (what CI runs)
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

## Fuzz tests

Every parser or validator that handles untrusted input has a native Go fuzz target
(`func FuzzXxx(f *testing.F)` in a `fuzz*_test.go` next to the code) that asserts
invariants (what must hold for every input), not just "does not panic". The
targets and what each one guarantees:

| Where | Targets and invariants |
|---|---|
| `internal/providers/core` | `FuzzDecodeJSON` (nothing over a depth, element, string or total limit is accepted, an accepted body decodes as `encoding/json` does, errors never quote the body, memory stays within a factor of the input), `FuzzRedact` and `FuzzRedactArbitrary` (a planted secret in plain, URL, base64, JSON and HTML encodings never survives; idempotent; bounded growth), `FuzzInstanceURL` and `FuzzJoinURL` (no userinfo, query or fragment accepted; a joined path never changes host), `FuzzAddressDenied` (IPv4-mapped and NAT64 forms get the IPv4 verdict, non-IP strings are denied), `FuzzParseDeniedNetworks` |
| `internal/netaccess`, `internal/config` | `FuzzParseRange`, `FuzzUpdateLists`, `FuzzResolveChain` / `FuzzResolveRaw` / `FuzzNoTrustedProxies` (the right-most-untrusted walk against an oracle; with no trusted proxy the client is always the TCP peer; a client taken from a header appears in a header), `FuzzNormalizeHost` / `FuzzHostAllowed` / `FuzzParseHostEntry` (only IP literals, localhost, valid single labels and valid listed names get the bypass), `FuzzTrustedProxies`, `FuzzLoad` |
| `internal/auth` | `FuzzValidateUsername`, `FuzzValidatePassword` (exact oracles), `FuzzParseHash` (stored-parameter bounds), `FuzzCleanClip`, `FuzzAuthenticate` (only the real token authenticates), `FuzzLogin` (one generic failure, no throttle dodging by spelling) |
| `internal/app` | `FuzzHTTPSurface` (the whole router with arbitrary requests as nobody, a user and an administrator: no panic, no 5xx from request shaping, no planted secret in any response, non-public routes refuse the unauthenticated, users never reach admin routes, CSRF and cross-site refusals, all derived from the router table), `FuzzHostileUpstream` (every route against one hostile upstream answer), `FuzzSameOrigin`, `FuzzSessionCookie`, `FuzzCSRFCompare`, `FuzzClientAddress` |
| `internal/converter` | `FuzzResolveInRoots` (attacks a symlink farm: the result is always a regular file inside a root, or `ErrOutsideRoots`), `FuzzValidateRoots`, `FuzzEnvRoots`, `FuzzArgs` (every ffmpeg argument is from the fixed vocabulary except the two paths, each exactly one element), `FuzzBackupNames`, `FuzzProgressWriter`, `FuzzClassifyProbe` |
| `internal/providers/all` | `FuzzUpstreamResponses` (every adapter call with one answer of the conversation replaced: `ProviderError`s only, no secrets, normalized values) |
| `internal/secrets`, `internal/store`, `internal/codecs`, `internal/instances`, `internal/ui`, `internal/providers/downloadclient` | the envelope decoder (`FuzzOpen`, `FuzzSealOpenTamper`, `FuzzAD`, `FuzzKeyFile`), migration names and the SQLite connection string (`FuzzLoadMigrations`, `FuzzDSN`), HDR, tier and codec classification and `settings.json`, the stored-secret destination rule (`FuzzStoredSecretDestination`), the UI file handler (`FuzzStaticHandler`), `FuzzNumber` |

**Seed corpora run as ordinary tests.** Each target has a corpus in
`testdata/fuzz/<FuzzName>/` made from real inputs (the fixtures of the fake
services, the hostile cases of the existing tests, a request for every route in
the router table), so `go test ./...` (and the CI `server` job) replays all of
them on every push: a regression is caught without any fuzzing time.
`internal/fuzzseed` writes the files; each package has a `TestWriteFuzzSeeds`
that regenerates its corpus and is skipped unless asked:

```sh
WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/netaccess
```

**Run a target** (any package, any length; it needs no network):

```sh
go test ./internal/netaccess -run '^$' -fuzz '^FuzzHostAllowed$' -fuzztime 2m
```

`go test` prints the executions and, on a failure, writes the minimized input to
`testdata/fuzz/<FuzzName>/<hash>` and the command that replays it. Fix the code,
keep that file as the regression entry (rename it `bug_<what>` and mention it in
the "Fuzzing findings" table of [../docs/threat-model.md](../docs/threat-model.md)),
and commit both. Without `-fuzz`, `go test` runs the corpus only.

**Scheduled runs.** [`.github/workflows/fuzz.yml`](../.github/workflows/fuzz.yml)
runs every target for about two minutes, weekly and on demand
(`gh workflow run Fuzz -f fuzztime=10m`), one matrix job per target, with
`contents: read` only; a failing job uploads the crashing inputs as an artifact
(`crashers-<target>`). It never runs on pull requests. A Go test
(`internal/fuzzseed`) fails when a target is missing from the workflow's matrix
or has no seed corpus.

**Add a target.** Write `FuzzXxx` next to the code, drive the real function with
the fewest parameters that reach its branches (a byte that picks a limit or a
variant beats a long string), and state the invariants in the doc comment and
in the body: compare with an oracle written independently of the code under
test where you can. Add its seeds to the package's `TestWriteFuzzSeeds` and run
it; add a line to the matrix in `fuzz.yml`; run the target for a few minutes
before opening the PR. A target that needs a database, a fake service or a
temporary tree builds it once outside `f.Fuzz` (`f.TempDir()`, the fakes'
`Start(f, ...)`), and must give every input a state no earlier input changed
(`FuzzHTTPSurface` rebuilds its app after any request that changed something;
the upstream targets start fresh Radarr and Sonarr fakes per input).

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
- At most 8 MiB read from upstream by default; an endpoint sets its own cap with
  `Request.MaxBytes` (`core.LargeListBytes`, 96 MiB, for library and torrent
  lists, `core.SmallResponseBytes`, 2 MiB, for status calls). `MaxResponseBytes`
  set on the client is a ceiling no call may raise. A larger response is a
  `ProviderError`, and the stream is cut off at the limit.
- The decoded shape is bounded too: decode with `core.DecodeJSON` (or
  `HTTPClient.JSON`), never `json.Unmarshal` on an upstream body. It refuses more
  than 200,000 elements in an array or object (`Request.Limits` lowers or raises
  it per call), 12 million values, nesting over 32 and strings over 1 MiB, and
  lets 2 to 4 large decodes run at once process-wide. At start the server sets
  the Go soft memory limit to 80% of the container's cgroup memory limit unless
  `GOMEMLIMIT` is set.
- Instance URLs must not carry a query string or fragment, and a stored secret is
  filled in for a changed URL only when the secret is re-entered.
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
cmd/wheelhouse        main: config, signals, listener, healthcheck, version, backup and rekey commands
internal/app          routes, middleware chain, graceful Serve
internal/config       listen address (PORT, WHEELHOUSE_LISTEN), database path (WHEELHOUSE_DB)
internal/store        SQLite database: open, migrations, backup, secret-column rekey
internal/secrets      AES-256-GCM envelopes, master key loading, redacting key and secret types
internal/features     feature gating
internal/status       GET /api/status logic
internal/downloads    GET /api/downloads logic
internal/codecs       codec analysis and preferences (/config/settings.json)
internal/converter    ffmpeg queue (SQLite-backed), settings, encoder detection, path containment, HDR probe, hardlink check
internal/library      library, storage, search, add-meta, details, seasons bodies
internal/explore      GET /api/explore and /api/explore/{id} logic
internal/providers/all  the registry of every provider
internal/middleware   request ID, security headers, access log, recovery
internal/providers/core  provider core
internal/ui           embedded UI
internal/version      the release version
```

## Dependencies

The server is the standard library plus **one** module, `modernc.org/sqlite`,
and the modules it needs (`go.mod` lists them; all are BSD-3-Clause or MIT, so
GPL-3.0 compatible). Why it is there rather than the alternatives:

- **Not cgo SQLite** (`mattn/go-sqlite3`): it needs a C toolchain, breaks the
  static `CGO_ENABLED=0` binary and the cross-compiled multi-arch image, and
  makes arm64 builds slow and fragile.
- **Not a hand-rolled store** (JSON files, a bolt-style key/value file): accounts,
  sessions, audit queries, constraints, transactions, crash safety and an online
  backup are exactly what SQLite already does well and is exhaustively tested
  for; rewriting that in a security-sensitive product is the riskier choice.

`modernc.org/libc` must stay at the version `modernc.org/sqlite`'s own `go.mod`
pins (its maintainers' rule), so it is not bumped on its own. A policy for
every addition: a written reason in the PR, the newest stable version (check
`go list -m -u all`; skip anything published in the last two weeks), a clean
`govulncheck ./...`, and a license that works with GPL-3.0 (CI runs
`go-licenses check`; locally `make -C server deps`).
