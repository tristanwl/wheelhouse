# Acceptance suite

A black-box HTTP suite that defines "done" for Wheelhouse's server. It starts
the server under test as a child process, starts the fake services from the
adapter fixtures, points the server at them with the environment variables the
app reads today, and talks to it only over HTTP. It passes against the Next.js
app and is meant to be pointed, unchanged, at the Go server that replaces it
(`docs/wheelhouse-spec-phase2.md`, "Migration plan", step 1).

It is not part of `npm test`.

## Running it

Against the built Next app (this is what CI does):

```sh
npm ci
npm run build
npm run test:acceptance
```

The default command is `node .next/standalone/server.js` with `PORT` and
`HOSTNAME=127.0.0.1` set and a free port picked per server. Readiness is
`GET /api/features` answering 200.

Against another server, give it a shell command that serves Wheelhouse on
`$PORT`:

```sh
WHEELHOUSE_CMD="./server/wheelhouse" npm run test:acceptance
WHEELHOUSE_CMD="go run ./server/cmd/wheelhouse" WHEELHOUSE_CWD=. npm run test:acceptance
```

- `WHEELHOUSE_CMD`: the command (run as `sh -c "exec $WHEELHOUSE_CMD"`, so the
  PID the suite kills is the server's own). It receives `PORT`,
  `HOSTNAME=127.0.0.1` and the service variables below, plus `PATH`, `HOME`,
  `LANG`, `LC_ALL`, `TMPDIR` and `TZ` from the test process and nothing else.
- `WHEELHOUSE_CWD`: the working directory (default: the repository root).

Against the Go server's skeleton (`docs/wheelhouse-spec-phase2.md`, step 2):

```sh
make -C server build
WHEELHOUSE_CMD=./server/wheelhouse npx vitest run --config acceptance/vitest.config.mts boot \
  -t "page |reports only the Dashboard|unknown routes"
```

The skeleton has no providers, so most scenarios fail until the later steps
port them; the command above runs the ones that already apply (readiness on
`/api/features`, every page answers HTML, only the Dashboard is available,
unknown routes are 404). Any file name or `-t` pattern narrows a run the same
way; there is no other scope switch.

The suite kills only the process it spawned, on success, failure and exit.
Each test file starts its own servers, so files run in parallel; the whole run
takes about 15 seconds (one test waits out the real 8 s upstream timeout).

The fakes are the TypeScript servers in `lib/providers/**/__fixtures__`; they
replay the JSON and text fixtures next to them, which a Go port reads as they
are. Only the fakes are imported from the repository; the suite imports nothing
from `app/`, `components/` or the provider code.

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

## Files and what they guarantee

| File | Scenario | Guarantees |
|---|---|---|
| `boot.test.ts` | 1. No-env boot | With no variables set: every page answers 200 HTML; every GET route answers the status in `routes.ts` (`none`) with JSON; `/api/features` enables only the Dashboard; aggregate routes are empty; unknown routes are 404 and wrong methods 4xx. |
| `stack.test.ts` | 2. Full stack | With healthy fakes of every service (and two Radarrs): every GET route answers its `full` status; `/api/status` lists the seven instances with stable ids and none is down; `/api/downloads` normalizes rows from SABnzbd and qBittorrent (states from the shared enum, progress 0..1, sizes in bytes); `/api/library` merges all managers biggest first; details, seasons, storage, Explore (including the "in library" mark for either Radarr and `limit`), search, add-meta and the codec routes have the documented shapes. |
| `failures.test.ts` | 3. Failure modes | Wrong keys read "API key incorrect", unreachable services read as down, each in `/api/status`, `/api/downloads`, `/api/library`, `/api/storage`, search and codec analysis (as `problems`), and in the 502 of routes addressed to the broken instance; healthy instances keep answering. With everything unreachable all aggregate routes still answer 200 within seconds. |
| `failures-slow.test.ts` | 3. Hangs | Services that accept connections and never answer are down with a "timed out" reason; nothing takes longer than the 8 s upstream timeout plus 3 s; healthy instances are unaffected. |
| `secrets.test.ts` | 4. Secret sweep | Canary secrets are planted in every key, password and token, and in `user:password@` URL credentials, in five situations (rejected keys, unreachable, credentials in URLs, credentials in unreachable URLs, working keys). Every case in `routes.ts`, every page, and the error paths of the mutating routes are requested. No secret (plain, URL-encoded or base64) appears in any status line, header or body, or in the server's stdout and stderr. The scan is itself tested. |
| `mutations.test.ts` | 5. Mutations | `POST /api/add` validation and the add flow to both Radarrs and to Sonarr (checked on the fake upstream's received request, and in the library afterwards); `DELETE` of a movie and of a season (only the addressed instance is touched); settings validation; conversion validation, cancel of an unknown job, and the hard-link confirmation (409, `code: "hardlinked"`, `linkCount`, nothing queued). |
| `route-table.test.ts` | | Reference-app check only: the route list in `routes.ts` equals the route files in `app/api`. Skipped when `app/api` does not exist. |

Bodies are asserted by shape and invariants, never by exact content, where the
exact value is an accident of the run: timestamps, the order of equal items,
which encoders or ffmpeg the machine has (so `codecs` availability is only
required to match its `missing` list), ids a fake assigns. `libraryUsedBytes`
and free space are checked as non-negative numbers only.

### Not covered

- Converting a file, and saving codec settings with `PUT`: both touch the real
  filesystem (`/movies`, `/tv`, `/config`). Only their refusals are tested.
- The UI. Pages are checked for 200 HTML only; a port may serve one shell for
  all of them.
- Real Plex/Jellyfin/etc. behaviour beyond what the fixtures replay.
- Request logging format; only that the output holds no secret.

### Behaviour worth knowing when porting

These are what the Next app does today and the suite requires, unless it says
otherwise.

- Explore without TMDB is a 502 (not 404), and its message names the variable.
- `GET /api/search?kind=movies` with no `term` answers `{"results": []}` with
  200 even when nothing is configured.
- An id the manager does not have, on a details route, is whatever the upstream
  made of it: 404 or 502 are both accepted.
- Cancelling an unknown job is a 4xx (Next answers 400); only the range is required.
- `DELETE /api/library/{instanceId}/{id}` asks the manager to delete the files
  as well (`deleteFiles=true`); the confirmation lives in the UI. The suite does
  not pin that parameter.

## Adding a route

1. Add it to the server.
2. Add it to `routes.ts`, once: a `GET_ROUTES` entry (its `pattern`, with
   `{param}` placeholders) with one or more `cases` (concrete `path`, the status
   with nothing configured (`none`) and with the full stack (`full`)), or a
   `MUTATING_ROUTES` entry. The secret sweep and the boot and stack scans read
   that table, so a listed route is checked for leaks without more work.
   `route-table.test.ts` fails on the Next app until the table and `app/api` agree.
3. Assert its body in `stack.test.ts` (and its failure behaviour in
   `failures.test.ts`) by shape and invariants. For a mutating route add a
   probe to `MUTATING_PROBES` in `secrets.test.ts` and its cases in
   `mutations.test.ts`.
4. A new provider: add its fake to `fakes.ts` (`startStack`), its variables to
   the table above and its canary to the sweep.
