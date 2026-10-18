# Wheelhouse spec: stack decision and Phase 2

Status: draft for review. Migration steps 1 to 3 are done (acceptance suite,
Go skeleton, provider core in `server/internal/providers/core`), as are the
providers of step 4. Step 5 is in progress: the provider registration, the
access-level router, the feature registry, `/api/status`, `/api/downloads` and
`/api/explore` are done, as are the codec and converter routes (step 7, pulled
forward); the other routes are not built yet. It replaces the "Next.js
versus a lighter stack" open question in [wheelhouse-spec.md](wheelhouse-spec.md)
and defines the work needed to reach 1.0.0.

## Decision

Wheelhouse moves to a **Go server with an embedded single-page UI**, before
authentication, the database and the admin page are built.

- **Server:** Go, standard library `net/http` routing, `log/slog`, a pure-Go
  SQLite driver (`modernc.org/sqlite`, no CGO), `golang.org/x/crypto` for
  Argon2id. A small dependency list, reviewed on every addition.
- **UI:** the existing React pages move to a Vite build with React Router (decided); SWR is kept.
  The build output is embedded into the binary with `embed`. npm is
  therefore build-time only; no JavaScript dependency runs on the server.
- **One process, one container.** The server serves the API and the UI and runs
  `ffmpeg` as a child process for conversions. Nothing else is required to run it.

### Why

1. **Security is a first-class goal for 1.0.** Wheelhouse holds API keys for a
   user's whole media stack and can rewrite or delete media. A memory-safe
   single binary with a small dependency tree has a much smaller attack surface
   than a Node server with a large npm tree, and Next.js has needed 23 security
   advisories and a middleware-bypass class of bugs that sit directly on the
   auth path.
2. **Auth should be explicit.** Sessions, CSRF and rate limits are plain
   middleware that can be tested route by route, not framework conventions.
3. **Operational wins.** Smaller memory use, a static binary, and arm64 (#14)
   with no extra work.
4. **Do it once.** Moving after auth, SQLite and the admin page exist would
   mean porting those too.

### What it costs

- Port about 5,000 lines of server code (provider layer, routes, converter) and
  about as many lines of tests. The 51 real-service fixtures carry over unchanged.
- The zod schemas that currently describe provider config are not shared with the
  UI. The server publishes each provider's config as JSON Schema and the UI
  renders the admin forms from it.
- Contributors need Go for the server and TypeScript for the UI.

### Rejected

- **Stay on Next.js.** Largest attack surface; auth in middleware is the weakest
  fit; the UI barely uses server rendering.
- **Vite SPA plus a Node server (Hono).** A good middle road, and the fallback
  if the port stalls, but the npm tree stays on the server.
- **Rust.** Comparable safety, slower to contribute to for this kind of
  integration glue.

## Container and deployment model

One image, one process: `wheelhouse`, containing the binary, ffmpeg and the
VA-API/QSV drivers. It runs as a non-root user with a read-only root filesystem,
all capabilities dropped, and one writable volume for config and the SQLite
database. A slimmer variant without ffmpeg is possible later but is not planned.

The converter stays a role inside the server, behind an interface, so it can move
out later without touching the rest: a separate worker image that the server
talks to over an internal API (and Tdarr as an alternative backend, per the
Phase 3 sketch). That split is a possible future change, not a 1.0 goal.

## Security requirements

These are acceptance criteria for 1.0, each with a test.

- **Roles:** exactly two. An **admin** has every right: configuration (providers
  and instances, feature toggles, media roots), users and sessions. A **user**
  can browse, search, add media through Explore and manage the library
  (monitoring, quality, deletes, conversions) but cannot see or change
  configuration, users or any secret. Roles are enforced on the server for every
  route; the UI only hides what the server would refuse. The first account
  created in setup is an admin, and the last admin cannot be deleted or demoted.
- **Passwords:** Argon2id with per-user salts; a minimum length, no composition rules.
- **Sessions:** random 256-bit tokens, stored hashed server-side; cookies are
  `HttpOnly`, `Secure` (when served over HTTPS), `SameSite=Strict`; rotated on
  login; idle and absolute timeouts; revocable from the admin page.
- **CSRF:** every state-changing request requires a per-session token or a
  same-origin custom header; `GET` never changes state.
- **Login protection:** rate limiting and incremental lockout per account and
  per address.
- **Local-network bypass:** off by default. When on, it matches only the
  configured CIDR ranges against the connection address, never a forwarded
  header, unless a trusted proxy is configured explicitly.
- **Authorization by default:** every route requires authentication except an
  explicit allow-list (health, login, first-run setup). A test enumerates all
  registered routes and fails if one is missing a decision.
- **Secrets at rest:** provider API keys are encrypted in the database with a
  key supplied by the operator (file or environment), and never returned by any
  API after being saved. The existing canary test keeps running against the new server.
- **Browser hardening:** strict `Content-Security-Policy` (no inline scripts),
  `X-Content-Type-Options`, `Referrer-Policy`, `frame-ancestors 'none'`.
- **Destructive actions:** server-enforced confirm tokens, the `.bak` safety net
  and the hardlink check carry over, and every destructive call is logged with
  the acting user.
- **Supply chain:** pinned dependencies, `govulncheck` and an npm audit in CI,
  Dependabot, signed images (cosign) with build provenance and an SBOM.
- **Errors and logs:** no secrets, URLs with credentials or upstream response
  bodies in either (this includes #26).
- **Threat model:** a short `docs/threat-model.md` is written before the auth
  PR and updated with it.

## Phase 2 features

1. First-run setup: create the first admin, then add services.
2. Local users with the admin and user roles above, and an optional
   local-network bypass (which grants the user role, never admin).
3. SQLite-backed config source replacing `EnvConfigSource` (the environment
   source stays as a read-only seed and for headless installs).
4. Admin page: providers and instances, "Test connection", feature toggles,
   users and sessions.
5. Converter: configurable media roots (#31), skip HDR and Dolby Vision (#32),
   a persisted queue (#33).
6. Remaining bugs: #26 and #30.

## Migration plan

The behaviour is defined first, then the server is rebuilt against it.

1. **Acceptance suite** (done). A black-box HTTP suite that starts the fake services
   from the existing fixtures, points a Wheelhouse server at them and asserts
   every route and the secret-canary sweep. It runs against the current Next app
   and must pass there; it is the definition of done for the port.
2. **Go skeleton** (done): `server/` module, CI, lint, `govulncheck`, build and the
   embedded UI placeholder, still alongside the Next app.
3. **Provider core in Go** (done): HTTP client (timeout, redaction, cache),
   runtime, config source. Ported tests, plus new ones for oversized responses,
   cross-host redirects and a secret-canary sweep. See
   [server/README.md](../server/README.md).
4. **Download clients,** then **media servers,** then **media managers,**
   each passing the shared contract tests and the real fixtures.
5. **API routes and the feature registry,** passing the acceptance suite.
   (In progress: foundation, features, status, downloads, explore and the library
   group (library, storage, search, add, add-meta, details, seasons, deletes) done.)
6. **UI to Vite:** pages and components move to the SPA served by the Go server.
7. **Converter in Go:** ffmpeg arguments, hardware-encode detection, hardlink
   check, queue. (Done, pulled forward with step 5: `internal/codecs`,
   `internal/converter`, `app/routes_codecs.go`.)
8. **Cut over:** remove Next, update the Dockerfile and release workflow, ship as
   0.2.0.
9. **Phase 2 features,** then the security review, then 1.0.0.

Each step is its own PR, and the Next app keeps working until step 8.

## Open questions

- Key management for secrets at rest: a key file by default, with optional
  environment variable and external secret managers later.
- Whether deletes and conversions should be switchable per user later (for
  example a read-and-add role), or whether the two fixed roles are enough.
