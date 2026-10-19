# Phase 2 plan: accounts, stored configuration and the admin page

Status: draft for review. Builds on [wheelhouse-spec-phase2.md](wheelhouse-spec-phase2.md)
(stack decision, security requirements, feature list). The migration to Go is
done; this is the road from 0.2.x to 1.0.0.

## Principles

1. **Secure by default, with no way to turn security off.** There is no
   "disable auth" switch. A fresh or upgraded install serves nothing but the
   setup page until an admin exists.
2. **Nothing ships half-protected.** Config and user routes land on `main`
   early but no release is cut until authentication enforces them. 0.2.x stays
   the current release until then.
3. **Every route has a level** (`public`, `authenticated`, `admin`), already
   enforced by the router test; Phase 2 makes the level real.
4. **Small trusted dependency set.** Phase 2 adds two modules, each justified
   below, always at the newest stable version and checked by `govulncheck`.
5. **The acceptance suite stays the contract.** It gains an authenticated mode;
   no test backdoor is added to the server.

## Design decisions

### Storage
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO, static binary,
  arm64 for free), file at `/config/wheelhouse.db`, WAL mode, `foreign_keys=ON`,
  file mode 0600. Forward-only numbered migrations embedded in the binary,
  applied in a transaction at start; refuse to start on a database newer than
  the binary.
- `settings.json` (codec settings) is migrated into the database on first run and
  then left in place untouched as a backup.
- **Backups:** a `wheelhouse backup <file>` command (SQLite online backup) and
  documentation. Restores are a file copy with the server stopped.

### Secrets at rest
- Provider API keys and passwords are encrypted with AES-256-GCM (standard
  library), a fresh nonce per value and the row id as associated data.
- The master key is 32 random bytes from, in order: `WHEELHOUSE_SECRET_KEY_FILE`,
  `WHEELHOUSE_SECRET_KEY`, or a generated `/config/secret.key` (0600, created on
  first start). Be honest in the docs: with the generated file next to the
  database this protects a copied database or backup, not a full volume
  compromise. Operators who want more point the key file at a secret mount.
- After saving, a secret is never returned by any API; the UI shows "set" and a
  "replace" action. Key rotation: `wheelhouse rekey` re-encrypts every secret.

### Passwords and sessions
- **Argon2id** from `golang.org/x/crypto` (second dependency), parameters
  stored with each hash so they can be raised later; a minimum length of 12 and
  a check against a short embedded list of the most common passwords; no
  composition rules.
- **Sessions:** 256-bit random token, only its SHA-256 stored; cookie
  `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` when the request arrived
  over HTTPS (directly, or via a trusted proxy header). Name `__Host-` prefixed
  when `Secure`. Idle timeout 12 h, absolute 30 days, rotated on login and on
  password change; every user's sessions listed and revocable; a password
  change or reset revokes all other sessions.
- **CSRF:** state-changing requests need a per-session token in a header, plus
  an `Origin`/`Sec-Fetch-Site` same-origin check; `GET` never changes state.
- **Login throttling:** per account and per address, exponential delay and a
  temporary lockout, with the same response for unknown users and wrong
  passwords, and a constant-time comparison path for both.
- **Roles:** `admin` and `user` as in the Phase 2 spec. The last admin can't be
  deleted or demoted.

### First-run setup
- With zero users, every route except `/healthz`, the setup status and the setup
  submit returns 503 `setup-required`, and the UI shows the setup wizard.
- **Setup token:** on start with no users the server generates a one-time token,
  logs it once and writes it to `/config/setup-token` (0600). Creating the first
  admin requires it. This stops a stranger who reaches a freshly started
  container before you do from claiming it. The token file is deleted on
  success.
- The wizard: create the admin, then add services (or import what the
  environment already defines), then optionally set the local-network bypass.

### Configuration model
- A `DatabaseConfigSource` implements the existing `ConfigSource`. Environment
  variables stay supported as a **read-only** source shown in the admin page as
  "from environment" and never editable there.
- Precedence: a database instance with the same id as an environment one wins;
  the admin page offers "import into database" for environment instances so an
  existing installation moves to stored config in one click, after which the
  environment values are ignored for that id.
- The runtime reloads on change (instances are cached by config already), so
  edits apply without a restart. "Test connection" calls the existing
  `TestConnection` on the unsaved values and returns only the redacted result.

### Local-network bypass
- The admin account always has to exist first: nothing works until setup is
  done, and only an admin can turn the bypass on, because it is configuration.
- Once enabled (off by default), a request from inside the configured CIDR
  ranges is treated as a signed-in `user` **without a login**. Those people can
  browse, add and manage media (including deletes and conversions) as in 0.2.x.
  They cannot see or change configuration, users or secrets. Only the `admin`
  role gates configuration.
- The match uses the TCP peer address. Forwarded headers are ignored unless the
  peer is in an explicit trusted-proxy list, and only then is the left-most
  untrusted entry used.
- The bypass never grants `admin`. Opening the admin pages from the local network
  asks for the admin's login; everything else stays login-free.
- Setup itself is never covered by the bypass; it needs the setup token.
- The admin page warns when the bypass range is wider than private address space.

### Audit log
- Append-only table of security-relevant events (logins, failures, lockouts,
  user and role changes, service changes, deletes, conversions, bypass use) with
  the acting user and address, never secrets. Viewable by admins, retention
  configurable (default 180 days).

### Recovery
- No email, so no emailed resets. An admin can reset a user's password in the
  UI. If the only admin is locked out, `wheelhouse reset-password <user>` run
  inside the container (access to the volume is the proof of ownership) prints
  a one-time password.

## Route levels (target)

| Level | Routes |
|---|---|
| `public` | `/healthz`, setup status and submit (only while no users exist), login |
| `authenticated` | everything the app shows today: features, status, downloads, library, storage, search, add, add-meta, explore, codec analysis and converting, `GET` own account, logout, password change |
| `admin` | services (list, create, update, delete, test), users, sessions of others, feature toggles, settings (media roots, converter, bypass, proxies), audit log, backup/rekey info |

Library deletes and conversions stay `authenticated` (decided: users manage
media). The open question below covers a later read-and-add role.

## Work plan

Status: **items 1 and 2 are done.** Item 1: threat model, `internal/store`,
`internal/secrets`, `wheelhouse backup` and `rekey`. Item 2: the server opens the
database and key at start; users, Argon2id passwords, sessions, CSRF, login
throttling, the `Guard` that enforces the route levels, the audit log, a minimal
`POST /api/auth/setup` with the setup token, the user and audit API, `wheelhouse
reset-password` and `forget-secrets`, [docs/operations.md](operations.md), and an
authenticated mode in the acceptance suite. Items 3 to 10 are open. The web UI
cannot log in until item 6, so nothing is released before then (a browser smoke
test borrows the acceptance harness's session meanwhile).

Each item is one PR (or a short series), built by a sub-agent against this plan
and re-verified before merging, as in the migration. Items 1 to 6 land on `main`
without a release; the release after item 6 is 0.3.0.

| # | PR | Notes |
|---|---|---|
| 1 | Threat model and storage foundation (done) | `docs/threat-model.md`; SQLite package, migrations, backups, secret encryption, `rekey`; first third-party dependency, justified |
| 2 | Users, sessions and login (done) | Argon2id, session store, CSRF, throttling, `authorize` middleware enforcing levels, audit log; acceptance suite gets an authenticated mode (setup then login helper; no server backdoor) |
| 3 | First-run setup and recovery | the setup token flow, 503 until set up and `reset-password` landed with item 2; left: the setup wizard routes' polish (wizard steps, import of environment services), the upgrade path for 0.2.x installs in the UI |
| 4 | Stored configuration | `DatabaseConfigSource`, environment seed and import, instance CRUD and test-connection routes, feature toggles, runtime reload |
| 5 | Local-network bypass | CIDRs, trusted proxies, role cap, tests for header spoofing |
| 6 | Web: login, setup wizard, account, route guards | session handling, CSRF header, 401 handling, user menu |
| 7 | Web: admin pages | services (add provider from JSON Schema forms, test, import from environment), users, sessions, features, settings, audit |
| 8 | Converter settings and fixes | media roots from the database (#31), HDR and Dolby Vision excluded by default with an explicit override (#32), queue persisted in SQLite with a documented recovery policy for interrupted jobs (#33), job owner recorded |
| 9 | Bugs and polish | Explore instance picker (#30), API inconsistencies (#42), arm64 image (#14: buildx with QEMU in the release workflow, drivers per architecture) |
| 10 | Security review and release candidate | independent adversarial reviews (auth, injection, path handling, SSRF through provider URLs, header handling), fuzz tests on parsers, dependency audit, full `govulncheck`/`npm audit`, update `SECURITY.md`, final docs |

Releases: **0.3.0** after item 8 (feature complete behind auth), **1.0.0-rc.1**
after item 10, **1.0.0** after a soak period with no open security findings. The
`latest` Docker tag and the non-pre-release label start at 1.0.0.

## Acceptance criteria for 1.0.0

1. A fresh container serves only setup until an admin exists, and the setup
   token is required (test: `acceptance/auth-setup.test.ts`).
2. Every registered route has a level and an unauthenticated request to any
   non-public route gets 401, verified by an enumerating test.
3. A `user` gets 403 on every `admin` route (enumerating test).
4. Sessions: cookies carry the flags above; logout and password change
   invalidate sessions; idle and absolute timeouts work.
5. CSRF: a state-changing request without the token or from a foreign origin is
   refused on every mutating route (enumerating test).
6. Login throttling and lockout work and do not reveal whether a user exists.
7. Provider secrets are encrypted in the database, never returned by any API,
   and never appear in logs, audit entries, errors or backups' plaintext (canary
   sweep over the database file).
8. The bypass never grants admin, ignores spoofed forwarded headers, and
   respects the trusted-proxy list (tests).
9. Environment-only installs keep working (read-only seed) and can be imported.
10. Converter: media roots configurable, HDR excluded by default, queue
    survives a restart.
11. The acceptance suite passes in authenticated mode against the binary and the
    image; the Phase 1 and migration criteria still pass.
12. No known vulnerabilities (`govulncheck`, `npm audit`), signed images with
    provenance and an SBOM, `SECURITY.md` current, threat model published.

## Open questions

- **Upgrade behaviour:** decided. Existing 0.2.x installs have no users, so after
  upgrading they show setup with a token in the logs and nothing else works
  until the admin exists. There is no grace mode. The admin can then enable the
  local-network bypass so household members keep using the app without a login.
- **Second factor (TOTP) and API access:** decided to leave both until after
  1.0. Sessions and the user table are designed so TOTP and personal access
  tokens can be added without migrating existing accounts.
- **Single sign-on (OIDC)** is left for after 1.0; the user table keeps room for
  an external identity.
- **Audit retention** default of 180 days.
