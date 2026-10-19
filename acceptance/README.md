# Acceptance suite

A black-box HTTP suite that defines "done" for Wheelhouse's server. It starts
the server under test as a child process, starts the fake services from the
adapter fixtures, points the server at them with the environment variables the
app reads, and talks to it only over HTTP. It was written against the Next.js
app of release 0.1.x (history; that app is gone) and passes, unchanged, against the Go server that replaced
it (`docs/wheelhouse-spec-phase2.md`, "Migration plan"). It also runs against
the container image, which is what CI's `docker` job does.

It lives in the repository root's tooling package (`package.json`): `npm ci`
there installs Vitest. It is not part of the Go tests.

## Running it

Against the binary (what CI's `acceptance` job does):

```sh
npm ci
(cd web && npm ci && npm run build && npm run embed)   # optional: embed the real UI
make -C server build
npm run test:acceptance
```

The default command is `./server/wheelhouse`, started with `PORT`,
`WHEELHOUSE_LISTEN=127.0.0.1:<port>`, a container-style `HOSTNAME` (which the
server must ignore), the service variables below, and its own empty config
directory (`WHEELHOUSE_CONFIG_DIR` and `WHEELHOUSE_DB`, under the temp
directory), on a free port picked per server. Readiness is the server's own
`listening addr=127.0.0.1:<port>` log line (it is printed after a successful
bind, and after the setup token is written) **and** `GET /healthz` answering
200: a free port is only free for a moment, and another worker's server on that
port would answer `/healthz` too. A server that fails to bind is started again on
a new port. A command used with `WHEELHOUSE_CMD` must therefore log that line
(the server and the image do, to stderr or stdout).

### Authenticated mode

The server enforces authentication and has no switch to turn it off, so the
harness (`harness.ts`) does what a person does, through the public API only: it
reads the one-time setup token from `<config dir>/setup-token`, calls
`POST /api/auth/setup`, logs in with `POST /api/auth/login`, and keeps the cookie
and CSRF token. `server.request` sends them with every request (the scenarios,
the secret sweep and the mutating routes all use it); `server.anon` sends
nothing; `server.newClient()` and `client.login()` make more sessions;
`server.cli([...])` runs `wheelhouse <args>` against the same configuration.
`startServer({ auth: "none" })` leaves the server fresh (setup required) and
`{ auth: "existing", configDir }` signs in to an existing database (a restart).
There is no test backdoor in the server; timeouts that need a clock (idle 12 h,
absolute 30 d) are tested in the Go packages with an injected clock, and the
suite checks the expiry the server reports.

Against another server, give it a shell command that serves Wheelhouse on
`$PORT`:

```sh
WHEELHOUSE_CMD="go run ./server/cmd/wheelhouse" npm run test:acceptance
WHEELHOUSE_CMD="./other-server" WHEELHOUSE_CWD=/some/dir npm run test:acceptance
```

- `WHEELHOUSE_CMD`: the command (run as `sh -c "exec $WHEELHOUSE_CMD"`, so the
  PID the suite kills is the server's own). It receives `PORT`,
  `WHEELHOUSE_LISTEN`, `HOSTNAME` and the service variables below, plus `PATH`,
  `HOME`, `LANG`, `LC_ALL`, `TMPDIR` and `TZ` from the test process and nothing else.
- `WHEELHOUSE_CWD`: the working directory (default: the repository root).

Any file name or `-t` pattern narrows a run (`npx vitest run --config
acceptance/vitest.config.mts boot`); there is no other scope switch. The suite
kills only the process it spawned, on success, failure and exit. Each test file
starts its own servers, so files run in parallel; the whole run takes about 10
seconds (one test waits out the real 8 s upstream timeout).

### Against a container image

The suite starts a server per scenario with its own environment, so it needs a
command it can start and stop, not a URL. `docker-server.sh` is that command for
an image:

```sh
docker build -t wheelhouse:dev .
WHEELHOUSE_CMD="acceptance/docker-server.sh wheelhouse:dev" npm run test:acceptance
```

It runs the image with host networking (the fakes listen on `127.0.0.1` of the
host), your uid/gid, `--read-only --cap-drop ALL --security-opt
no-new-privileges`, a fresh writable `/config`, and the temp directory mounted at
the same path (the hard-link scenario creates its scratch files there). The
suite stops it with SIGTERM, which stops the container; a SIGKILL would leave it
running (`docker ps -aq --filter name=wheelhouse-acceptance | xargs -r docker rm -f`).

`docker-smoke.sh IMAGE` is the complementary check of how the image is *run*:
no environment, an arbitrary uid/gid, read-only root, no capabilities,
`--tmpfs /tmp`; it asserts the version is stamped, the health check turns
healthy, every page answers 200, only the Dashboard is enabled, settings are
written to `/config` with the right owner and SIGTERM exits 0.

The fakes are the TypeScript servers in `acceptance/fakes/`; they replay the
JSON and text fixtures next to them. The Go tests keep byte-identical copies of
the fixtures in their `testdata` directories and fail if a copy drifts, so
`acceptance/fakes` is the single source (CLAUDE.md, "Fixtures come from real
responses"). The suite imports nothing from `server/` or `web/`.

## What the server under test must read

| Variable | Used for |
|---|---|
| `SONARR_URL`, `SONARR_API_KEY` | Sonarr, instance id `sonarr` |
| `RADARR_URL`, `RADARR_API_KEY` | Radarr, instance id `radarr` |
| `RADARR_4K_URL`, `RADARR_4K_API_KEY` | a second Radarr, id `radarr-4k`, name "Radarr 4K" |
| `SABNZBD_URL`, `SABNZBD_API_KEY` | SABnzbd, id `sabnzbd` |
| `QBITTORRENT_URL`, `QBITTORRENT_USERNAME`, `QBITTORRENT_PASSWORD` | qBittorrent, id `qbittorrent` |
| `PLEX_URL`, `PLEX_TOKEN` | Plex, id `plex` |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY` | Jellyfin, id `jellyfin` |
| `TMDB_API_KEY`, `TMDB_BASE_URL` | TMDB; `TMDB_BASE_URL` is the API root including `/3` (default `https://api.themoviedb.org/3`) |
| `WHEELHOUSE_MEDIA_ROOTS` | the folders conversion may touch (the hard-link scenario points it at a scratch directory) |
| `WHEELHOUSE_CONFIG_DIR`, `WHEELHOUSE_DB` | the config directory (database, master key, setup token) and database file; one empty directory per server |
| `WHEELHOUSE_ALLOWED_HOSTS` | not set by the harness: the bypass scenarios connect by IP (always allowed); `auth-hardening.test.ts` checks the Host rules with raw requests and the stored `allowedHosts` list |
| `WHEELHOUSE_TRUSTED_PROXIES` | set by the cookie scenarios and `auth-bypass.test.ts` (the harness connects from `127.0.0.1`, which is also how the bypass scenarios make a request "come from" a range: they store `127.0.0.1/32`, or a range that excludes it, with `PUT /api/admin/network`) |

## Files and what they guarantee

| File | Scenario | Guarantees |
|---|---|---|
| `boot.test.ts` | 1. No-env boot | With no variables set: every page answers 200 HTML; every GET route answers the status in `routes.ts` (`none`) with JSON; `/api/features` enables only the Dashboard; aggregate routes are empty; unknown routes are 404 and wrong methods 4xx. |
| `stack.test.ts` | 2. Full stack | With healthy fakes of every service (and two Radarrs): every GET route answers its `full` status; `/api/status` lists the seven instances with stable ids and none is down; `/api/downloads` normalizes rows from SABnzbd and qBittorrent (states from the shared enum, progress 0..1, sizes in bytes); `/api/library` merges all managers biggest first; details, seasons, storage, Explore (including the "in library" mark for either Radarr and `limit`), search, add-meta and the codec routes have the documented shapes. |
| `failures.test.ts` | 3. Failure modes | Wrong keys read "API key incorrect", unreachable services read as down, each in `/api/status`, `/api/downloads`, `/api/library`, `/api/storage`, search and codec analysis (as `problems`), and in the 502 of routes addressed to the broken instance; healthy instances keep answering. With everything unreachable all aggregate routes still answer 200 within seconds. |
| `failures-slow.test.ts` | 3. Hangs | Services that accept connections and never answer are down with a "timed out" reason; nothing takes longer than the 8 s upstream timeout plus 3 s; healthy instances are unaffected. |
| `secrets.test.ts` | 4. Secret sweep | Canary secrets are planted in every key, password and token, and in `user:password@` URL credentials, in five situations (rejected keys, unreachable, credentials in URLs, credentials in unreachable URLs, working keys). Every case in `routes.ts`, every page, and the error paths of the mutating routes are requested. No secret (plain, URL-encoded or base64) appears in any status line, header or body, or in the server's stdout and stderr. The same sweep plants the setup token, the administrator's password, a wrong password, the session cookie and the CSRF token: none may appear in those responses, in the log (except the one setup-token line printed at start) or in the raw database, its `-wal` and `-shm` files, which must hold only the Argon2id hash and the SHA-256 of the token. The scan is itself tested. |
| `auth-setup.test.ts` | 6. First run | A fresh server serves the pages, `/healthz` and the setup status and answers 503 `setup required` everywhere else; the token file is mode 0600 and the token is logged once; setup refuses a missing or wrong token, a weak password and a foreign origin without spending the token; it works once, deletes the token file and then returns 409; a restart keeps accounts and sessions and makes no token; startup failures are fatal with a message (database mode, bad key file, bad proxy list, missing directory); a lost master key stops the server and `forget-secrets --yes` recovers it with the accounts intact. |
| `auth-levels.test.ts` | 7. Levels | For every route in `ROUTE_LEVELS` (kept equal to the Go router test's table): an unauthenticated request to a non-public route is 401 JSON, never a redirect; a `user` gets 403 on every admin route and is let through the others; every mutating route refuses a missing, wrong or other session's CSRF token, a foreign or `null` Origin and cross-site fetch metadata, and login and setup refuse a foreign origin; GET routes never change state. |
| `auth-sessions.test.ts` | 8. Sessions | Cookie flags (`HttpOnly`, `SameSite=Strict`, `Path=/`, no `Secure` over plain HTTP), `__Host-` and `Secure` only behind a trusted proxy that says HTTPS (and the header ignored from anyone else); 256-bit token never in a body; reported idle and absolute expiry; a new session at every login and no fixation; own session list and revoke; logout; a password change ends every session and rotates. |
| `auth-throttle.test.ts` | 9. Throttling | Unknown user, wrong password and locked login answer byte-identically; both cost a password hash; an account locks after 5 failures even for the right password and a made-up name behaves the same; an address locks after many; audit rows exist and hold no password or unknown name. |
| `auth-users.test.ts` | 10. Accounts | Create, rename, re-role, disable, delete, case-insensitive uniqueness, strict validation and size limits; the last administrator cannot be deleted, demoted or disabled; a user cannot escalate or read accounts and the audit log; an administrator lists and ends another user's sessions; an admin reset and `wheelhouse reset-password` force a new password (everything else is 403 until then) and lift a lockout; the audit log has the events, the acting user and no secret, newest first, paged. |
| `config.test.ts` | 11. Stored configuration | The admin API with no environment: provider registry and schemas; instance create, edit (an omitted secret keeps the stored one), enable, delete, with the app (`/api/status`, `/api/features`) following without a restart; secrets are never returned (only `set: true`); connection tests on unsaved and saved values (good key, wrong key, unreachable, link-local refused); environment instances are read-only, imported, then overridden by the database; feature toggles; audit entries without secrets; canary sweep over responses, logs, the database and its WAL. |
| `converter.test.ts` | Converter settings and queue | `GET/PUT /api/admin/converter` (shape, validation, precedence, no restart needed, audit), settings and a finished job surviving a restart, HDR exclusion and acknowledgement with a fake ffprobe, and a restart mid-conversion with a fake ffmpeg on `PATH` (interrupted job, cleanup, queued jobs resume in order); the fake-tool scenarios are skipped against the image. |
| `mutations.test.ts` | 5. Mutations | `POST /api/add` validation and the add flow to both Radarrs and to Sonarr (checked on the fake upstream's received request, and in the library afterwards); `DELETE` of a movie and of a season (only the addressed instance is touched); settings validation; conversion validation, cancel of an unknown job, and the hard-link confirmation (409, `code: "hardlinked"`, `linkCount`, nothing queued). |

Bodies are asserted by shape and invariants, never by exact content, where the
exact value is an accident of the run: timestamps, the order of equal items,
which encoders or ffmpeg the machine has (so `codecs` availability is only
required to match its `missing` list), ids a fake assigns. `libraryUsedBytes`
and free space are checked as non-negative numbers only.

### Not covered

- Converting a file, and saving codec settings with `PUT`: both touch the real
  filesystem (`/movies`, `/tv`, `/config`). Only their refusals are tested here
  (the Go tests convert with fake and real ffmpeg, and `docker-smoke.sh` saves
  settings in a container).
- The UI. Pages are checked for 200 HTML only; a port may serve one shell for
  all of them.
- Real Plex/Jellyfin/etc. behaviour beyond what the fixtures replay.
- Request logging format; only that the output holds no secret.

### Behaviour worth knowing

These are behaviours the suite requires. They come from the Next app of 0.1.x,
which the Go server reproduces, unless it says otherwise.

- Explore without TMDB is a 502 (not 404), and its message names the variable.
- `GET /api/search?kind=movies` with no `term` answers `{"results": []}` with
  200 even when nothing is configured.
- An id the manager does not have, on a details route, is whatever the upstream
  made of it: 404 or 502 are both accepted.
- Cancelling an unknown job is a 4xx; only the range is required.
- `DELETE /api/library/{instanceId}/{id}` asks the manager to delete the files
  as well (`deleteFiles=true`). The suite sends `?confirm=true` on every
  library delete (the server requires it, issue #41).

## Adding a route

1. Add it to the server, with its access level.
2. Add it to `ROUTE_LEVELS` in `routes.ts` with that level (a Go test fails until
   the two tables agree, and `auth-levels.test.ts` then checks 401 / 403 / CSRF /
   origin for it), and to the table below, once: a `GET_ROUTES` entry (its `pattern`, with
   `{param}` placeholders) with one or more `cases` (concrete `path`, the status
   with nothing configured (`none`) and with the full stack (`full`)), or a
   `MUTATING_ROUTES` entry. The secret sweep and the boot and stack scans read
   that table, so a listed route is checked for leaks without more work. (The Go
   router test, `server/internal/app/router_test.go`, separately lists every
   route with its access level.)
3. Assert its body in `stack.test.ts` (and its failure behaviour in
   `failures.test.ts`) by shape and invariants. For a mutating route add a
   probe to `MUTATING_PROBES` in `secrets.test.ts` and its cases in
   `mutations.test.ts`.
4. A new provider: add its fake to `acceptance/fakes/` (`index.ts`, `startStack`), its variables to
   the table above and its canary to the sweep.
