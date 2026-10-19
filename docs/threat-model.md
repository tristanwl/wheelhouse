# Wheelhouse threat model

Status: written for Phase 2 item 1 ([plan](wheelhouse-phase2-plan.md)); updated by
every Phase 2 PR that changes a trust boundary or a mitigation. The security
requirements this serves are in [wheelhouse-spec-phase2.md](wheelhouse-spec-phase2.md);
the reporting policy is in [SECURITY.md](../SECURITY.md).

**Where things stand today.** Releases up to 0.2.x had **no authentication**: every
route was public, so anyone who could reach the port could read the dashboard, delete library
items (with `?confirm=true`) and queue conversions. Since 0.3.0 the server enforces
authentication: accounts, sessions, login, CSRF, throttling, an audit log and the route levels
are in place and tested, and the web UI has its login, setup wizard, account page, admin pages
and route guards. [release-1.0-checklist.md](release-1.0-checklist.md) re-checks every row
marked "in place" below against the test that enforces it (with the date), and lists the known
limitations. A row marked "item N" without "in place" is not in place; a version is only as
protected as its release notes say.

## What Wheelhouse is, for this purpose

One Go process in one container. It holds credentials for a person's whole media
stack (Sonarr, Radarr, SABnzbd, qBittorrent, Plex, Jellyfin, TMDB), can delete
library entries through those services, and can rewrite media files on disk with
ffmpeg. It serves a single-page UI and a JSON API, makes outgoing HTTP calls to
the configured services, and (from Phase 2) keeps a SQLite database and a master
key under `/config`.

## Assets

| Asset | Why it matters | Where it lives |
|---|---|---|
| **Provider secrets** (API keys, tokens, qBittorrent username and password, credentials in service URLs) | They are the keys to services that hold downloads, accounts and watch history; a leaked Plex token or *arr key gives full control of that service | Encrypted in the database (item 4), master key in a file or the environment; environment variables stay a read-only source |
| **Master key** | Decrypts every stored secret | `WHEELHOUSE_SECRET_KEY_FILE`, `WHEELHOUSE_SECRET_KEY`, or `/config/secret.key` (item 1, in place) |
| **Library files** | Wheelhouse can delete entries (and, through *arr, files) and rewrite files in place; loss or corruption is irreversible without the `.bak` | Media directories mounted into the container |
| **User credentials** | Argon2id password hashes unlock the admin role | Database (item 2, in place) |
| **Sessions** | A session token is the user's identity for up to 30 days | Only the SHA-256 of the token in the database; the cookie in the browser (item 2, in place) |
| **Configuration and the audit log** | Integrity of what the admin configured and of the record of who did what | Database (audit log: item 2 and configuration: item 4, both in place) |
| **Availability** | The household uses it; a crash loop or a stuck conversion is a loss, but a lesser one | The process |
| **The host** | A compromised container is a foothold on a machine that also runs the rest of the media stack | Container runtime |

## Actors

| # | Actor | Capability assumed |
|---|---|---|
| A1 | Anonymous network attacker | Can send arbitrary HTTP to the port if it is exposed (directly, port-forwarded, or through a proxy); no credentials |
| A2 | Malicious or compromised configured upstream service | Answers Wheelhouse's outgoing requests with arbitrary bytes: huge bodies, slow streams, redirects, HTML where JSON is expected, hostile JSON, text that looks like an injection |
| A3 | Local-network user | On the LAN, maybe a guest or a compromised IoT device; can reach the port and can spoof forwarded headers; with the Phase 2 bypass they are treated as a signed-in `user` by design |
| A4 | Lower-privileged signed-in user (`user` role) | Authenticated, wants to read configuration or secrets, create users, or escalate to `admin` |
| A5 | Someone holding a copy of the database or a backup | Read access to `wheelhouse.db` or a `wheelhouse backup` file, but not to the master key |
| A6 | Someone holding the whole `/config` volume | Database, `secret.key` and `settings.json` together (a stolen disk, a leaked volume snapshot, a careless `tar` of the config directory) |
| A7 | Malicious media file name or metadata | A file in the library whose name, embedded tags or container structure are crafted (it can arrive through a download client); reaches ffprobe, ffmpeg and the UI |
| A8 | Supply chain | A malicious or compromised dependency, base image, GitHub Action or release artifact |
| A9 | Someone with shell access on the host or the container | Out of scope as an attacker (they can read process memory and the key); relevant only to what the design should not make worse |

## Trust boundaries

```
 browser ──(1)──► reverse proxy (TLS) ──(2)──► wheelhouse process ──(3)──► configured services
                                                  │   │   └──(4)──► ffmpeg / ffprobe (child processes)
                                                  │   └──(5)──► /config  (settings.json, wheelhouse.db, secret.key)
                                                  └──(6)──► media directories
```

1. **Network to proxy.** Untrusted. TLS ends here (assumption T1).
2. **Proxy to Wheelhouse.** Wheelhouse does not know whether a proxy exists. It
   trusts forwarded headers only from an explicit trusted-proxy list
   (`WHEELHOUSE_TRUSTED_PROXIES` plus the list an admin stores; see "What the
   local-network bypass changes" below).
3. **Wheelhouse to services.** The services are configured by the admin and
   *trusted to be what they are*, but their **responses are untrusted input**
   (A2).
4. **Wheelhouse to ffmpeg/ffprobe.** Child processes parse untrusted media; they
   get validated arguments, never a shell.
5. **Wheelhouse to `/config`.** Trusted storage, but its contents are protected
   against copying (encryption of secrets, file modes), not against a reader of
   the whole volume (A6).
6. **Wheelhouse to the media directories.** Writes only inside the configured
   roots after resolving symlinks.

## Entry points

| Entry point | Untrusted input | Notes |
|---|---|---|
| HTTP API (`/api/...`, `/healthz`) | method, path, query, headers, JSON bodies | Every route is registered with a level; `router_test.go` enumerates them |
| UI (the embedded SPA) | everything a service or a file name can put on screen | Rendered by React (escaped); strict CSP, no inline script |
| Environment variables | service URLs and keys, listen address, media roots, key material | Set by the operator; read once at start |
| Files under `/config` and the media roots | `settings.json`, the database, the key file, media files | Parsed defensively; wrong modes refused for the database and key |
| Upstream responses | bodies, status codes, redirects, timing | Size, time and redirect limits in `providers/core` |
| ffmpeg / ffprobe | media file contents and metadata | Run as the container user with a context and validated arguments |
| CLI inside the container (`backup`, `rekey`, later `reset-password`) | arguments, the same environment | Reachable only by someone who can already exec in the container (A9) |

## Assumptions

- **T1. TLS is terminated by a reverse proxy** the operator runs (Caddy, nginx,
  Traefik). Wheelhouse serves plain HTTP. It sets `Secure` on cookies when the
  request arrived over HTTPS (item 2); behind a proxy that means a trusted-proxy
  entry (item 5). Exposing the plain port to the internet is unsupported.
- **T2. The container runtime isolates the process.** What the image itself
  guarantees is small: it runs as a non-root user (`USER 1000:1000`, any numeric
  `--user` works) and it works with a read-only root filesystem, no capabilities
  and `no-new-privileges` (it writes only `/config`, the media mounts and `/tmp`;
  `acceptance/docker-smoke.sh` runs exactly that). The image does not *enforce*
  any of the rest: `read_only`, `cap_drop: [ALL]`, `no-new-privileges` and a
  tmpfs on `/tmp` are the operator's settings, and `docker-compose.example.yml`
  and `deploy/wheelhouse.service.example` now enable them by default. A container
  escape is out of scope.
- **T3. The host and the `/config` mount are not shared** with untrusted local
  users. The files are created `0600` and the database directory must not be
  world-writable, but a user who is root on the host reads everything.
- **T4. The services Wheelhouse is configured against are the ones the admin
  intended.** An attacker who can change the configuration is already an admin.
- **T5. Clocks are roughly right** (session timeouts, audit timestamps).
- **T6. The operator keeps the master key somewhere other than next to their
  backups** if they want backups to be useless on their own.

## Threats (STRIDE)

Status key: **in place** (merged, with the named test), **item N** (a Phase 2
work-plan item, not merged yet), **accepted** (a known limit, see Residual
risks), **out of scope**.

### Spoofing

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| S1 | A1 uses the app with no identity at all | Authentication on every non-public route; the `Guard` (`internal/app/guard.go`); an enumerating test fails if a route has no decision, and the acceptance suite sends an unauthenticated request to every non-public route and expects 401 | **In place** (item 2; `TestEveryRouteHasAnAccessLevel`, `TestUnauthenticatedRequestsGet401OnEveryGuardedRoute`, `acceptance/auth-levels.test.ts`). Released 0.2.x: none |
| S2 | A1 claims a fresh install before the admin does | Only the setup routes answer until an admin exists (503 `setup required` elsewhere); a one-time setup token (logged once, `/config/setup-token`, mode 0600, constant-time compare, throttled, removed on success, new at every start) is required; setup works only while no account exists, re-checked inside its transaction | **In place** on the server (item 2; `TestSetupNeedsTheTokenAndWorksOnce`, `TestSetupLosingTheRaceCreatesNothing`, `acceptance/auth-setup.test.ts`); the web setup page and wizard are in place (items 3 and 6) |
| S3 | A1 guesses or brute-forces a password | Argon2id (64 MiB, 3 passes) with at most 3 hashes at a time, minimum length 12, common-password list, per-account (5 failures, 30 s doubling to 15 min) and per-address (20) lockout kept in memory, identical responses for unknown users, wrong passwords, disabled accounts and locked logins | **In place** (item 2; `TestEveryFailedLoginLooksTheSameAndComputesAHash`, `TestAccountLockoutBacksOffAndLiftsAfterTheTime`, `acceptance/auth-throttle.test.ts`). Item 10 closed a race: attempts in flight now count toward the threshold (a slot is reserved per account and address key before hashing), the lock is re-checked after the hash semaphore, locked keys skip Argon2 entirely and the verification queue is bounded (`TestParallelGuessesCannotPassTheAccountLock` and siblings). The counters are not persisted; a restart clears them (accepted: only the operator restarts the server) |
| S4 | A3 spoofs `X-Forwarded-For` to look like a local address and get the bypass | Bypass matches the TCP peer address; forwarded headers ignored unless the peer is in the trusted-proxy list, then only the right-most untrusted entry counts and malformed or contradictory values give "unknown" (no bypass); tests for spoofing, chains, IPv6, IPv4-mapped, zone ids and malformed values | **In place** (item 5; `server/internal/netaccess`, `TestSpoofedForwardedHeadersAreIgnoredFromUntrustedPeers`, `acceptance/auth-bypass.test.ts`) |
| S5 | Session theft or fixation | 256-bit random tokens, only the SHA-256 stored, a new session at login and password change (a client-supplied token is never adopted), `HttpOnly`, `SameSite=Strict`, `Secure` and the `__Host-` name over HTTPS, idle 12 h and absolute 30 d limits, revocation (own and, for admins, anyone's); a stolen cookie works until it expires or is revoked | **In place** (item 2; `TestSessionCookieFlags`, `TestSessionsEndWhenIdleOrOld`, `TestPasswordChangeRotatesAndRevokes`, `acceptance/auth-sessions.test.ts`) |
| S6 | A2 impersonates a configured service | TLS verification is always on, no insecure option (`TestHTTPClientVerifiesTLSCertificates`, and `TestTLSVerificationIsNeverDisabled` fails the build if any production code sets `InsecureSkipVerify`); plain-HTTP services on a LAN are the operator's choice and are not authenticated beyond their API key | In place; plain HTTP **accepted** |
| S7 | A CSRF request makes a signed-in browser act | `SameSite=Strict` cookie; per-session CSRF token in `X-CSRF-Token` (derived from the cookie, not stored, not in a script-readable cookie) plus an `Origin`/`Sec-Fetch-Site` same-origin check on every state-changing request, login and setup included; `GET` never changes state; the JSON routes need `Content-Type: application/json`, which a cross-site form cannot send | **In place** (item 2; `TestEveryMutatingRouteRefusesMissingCSRFAndForeignOrigins`, `TestGETRoutesNeverChangeState`, `acceptance/auth-levels.test.ts`) |

### Tampering

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| T-1 | A1 deletes library items or queues conversions | Auth (S1); deletes need `?confirm=true`, enforced server side (acceptance suite, `routes_library.go`); conversion keeps a `.bak` and warns about hard links; both are audited with the acting user | **In place** (item 2) |
| T-2 | A user edits configuration, users or roles | Role check on every admin route, enumerated by a test; the last enabled admin cannot be deleted, demoted or disabled (code check in the transaction and two triggers, each tested alone) | **In place** for accounts and codec settings (item 2; `TestTheLastAdminSurvivesCodeAndTriggers`, `TestAUserGets403OnEveryAdminRouteAndAdminsGetThrough`); service configuration: **in place** (item 4; the `/api/admin/*` routes are in the enumerating tests) |
| T-3 | A5 or A6 edits the database offline (adds an admin, changes a service URL) | Not prevented: whoever can write the database can do anything the server can. File mode `0600` and the directory check reduce accidental exposure | **Accepted** (A6 is out of the guarantee) |
| T-4 | A tampered or swapped ciphertext is accepted | AES-256-GCM authenticates every byte; the row id and field are bound as associated data so a value moved to another row does not open (`TestTamperDetectionEveryByte`, `TestWrongAssociatedData`, `TestSecretIsBoundToItsRowAndField`) | **In place** (item 1) |
| T-5 | A database from a newer Wheelhouse is opened by an older binary and corrupted | Migrations record a hash per applied migration; the server refuses a newer schema (`TestRefusesNewerSchema`, `TestRefusesChangedOrDamagedHistory`) | **In place** (item 1) |
| T-6 | A crash corrupts the database or leaves half a migration | WAL, `synchronous=FULL`, every migration batch in one transaction (`TestCrashMidTransactionLeavesNoTrace`, `TestMigrationsApplyInOrderAndAllOrNothing`) | **In place** (item 1) |
| T-7 | Path traversal or a symlink escape makes conversion touch a file outside the media roots | The path is resolved (`..` refused, `EvalSymlinks`, regular file), must be inside a root, and the check repeats at job start and before the swap (`TestResolveInRoots`). The roots are admin-only configuration validated on save (absolute, no `..`, not `/` or a system folder or the config directory, existing directories, no nesting: `TestValidateRoots`), and a job read back from the database is resolved against the roots in force when it runs, never trusted (`TestStaleRowOutsideTheRootsNeverRuns`) | **In place** (item 8) |
| T-8 | A request body or header forged to reach ffmpeg arguments | Arguments come from validated enums and an absolute resolved path; `os/exec`, never a shell (`TestArgsPerBackend`; `TestNoShellIsEverStartedAndOnlyFfmpegCodeRunsPrograms` fails if any code starts a shell or a program outside the converter) | **In place** (`internal/converter`) |
| T-9 | A hostile `Host`/`Origin` or header trick | Only the TCP peer is used for the bypass (S4); the CSRF origin check (S7); `X-Forwarded-Proto` is believed only from a peer in `WHEELHOUSE_TRUSTED_PROXIES` and only decides the cookie's `Secure` flag; the recorded address is the client address resolved by the same rules (the TCP peer unless a trusted proxy forwarded it) | **In place** (items 2 and 5; `TestSessionCookieFlags`, `netaccess` tests). **DNS rebinding is mitigated** (item 10): the bypass identity is granted only when the request's `Host` is an IP literal, `localhost`, a single-label name, or on the allow-list (`WHEELHOUSE_ALLOWED_HOSTS` plus the stored list), so a rebound attacker domain gets the same 401 as any anonymous client (`TestBypassNeedsAnAcceptableHost`, `TestSpoofedHostAndOriginPairIsRefusedForTheBypass`, `acceptance/auth-hardening.test.ts`). Session cookies are host-bound and need no such check |

### Repudiation

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| R1 | A destructive action cannot be traced to anyone | Append-only audit table: setup, logins, failures, lockouts, logouts, password changes and resets (also by the CLI), user and role changes, session revocations, library adds and deletes, conversions; acting user and address, never secrets; admins read it through `GET /api/audit`; the 180-day retention is not enforced yet (rows are kept) | **In place** (item 2; `TestAuditTrailHasNoSecretsAndCannotBeEdited`, `acceptance/auth-users.test.ts`); bypass settings changes and, for the bypass principal, `via: bypass` plus the peer address on adds, deletes and conversions (item 5); service changes arrive with item 4; pruning with the settings that configure it |
| R2 | An admin edits or deletes audit rows | Nothing in the app writes anything but inserts, and triggers refuse `UPDATE` and `DELETE` on the table; **not** protected against someone with the database file (T-3) | **In place** (item 2); limit **accepted** |

### Information disclosure

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| I1 | Provider secrets appear in a response, header, error, log line or UI | Routes make upstream calls through `core.NewHTTPClient` and echo only `core.Redact`ed text (plain, URL-encoded, base64, JSON-escaped, `apikey=` parameters, `user:pass@`); canary sweeps over every error string, route and log (`providers/core/canary_test.go`, each provider's `canary_test.go`, `acceptance/secrets.test.ts`) | **In place** |
| I2 | A saved secret is read back through the admin API | After saving, a secret is never returned: the instance list says `secrets: {apiKey: {set: true}}`, an environment instance's URL is shown without user information, and a connection test returns only a redacted result (`acceptance/config.test.ts` plants canaries and sweeps responses, audit entries, logs and the database files) | **In place** (item 4) |
| I3 | A5 reads secrets from the database or a backup | Secrets are AES-256-GCM encrypted; the key is not in the database or in a `wheelhouse backup`. A canary test scans the raw database, its `-wal` and a backup (`TestPlaintextNeverReachesDisk`); deleted values are overwritten (`secure_delete`, `TestDeletedValuesAreOverwritten`) | **In place** (item 1); the column `instances.secrets` (one sealed JSON object per instance, associated data `(instances, id, secrets)`) is registered in `store.SecretColumns`, so `rekey`, `forget-secrets` and the canary sweeps cover it (item 4) |
| I4 | A6 reads secrets from the whole volume | Not prevented when the key is the generated `/config/secret.key` beside the database. Operators who care point `WHEELHOUSE_SECRET_KEY_FILE` at a secret mount or tmpfs | **Accepted**, documented honestly |
| I5 | The key or a plaintext leaks into a log, panic message, `%v` print or JSON | `secrets.Key` and `secrets.Secret` redact under every fmt verb, `slog` and JSON (`TestKeyNeverPrints`, `TestSecretsAndKeysRedact`); errors are static text; the access log omits queries, headers and bodies; panics answer a generic 500 (`TestRecoverHidesDetails`) | **In place** |
| I6 | Anyone on the network reads library and status data | Same as S1 | **In place** (item 2) |
| I7 | Secrets in the process environment are readable (`docker inspect`, `/proc/<pid>/environ`) | The environment source is the 0.2.x interface and stays supported; the admin page shows environment values as "from environment" and offers import into the database; operators can use a key file for the master key | **Accepted** for the environment path |
| I8 | A user session reveals admin data | Role-gated routes (T-2); the local-network bypass never grants `admin` | **In place** (items 2 and 5) |
| I9 | Browser-side leakage (referrers, framing, MIME sniffing, third-party images) | `Referrer-Policy: no-referrer`, `frame-ancestors 'none'`, `nosniff`, CSP with images only from `self`, `data:` and the two metadata hosts (`TestSecurityHeaders`, `TestCSPAllowsExactlyTheImageHosts`) | **In place** |
| I10 | A backup is left world-readable | Backups are created `0600` and never overwrite (`TestBackupCopiesEverythingWithMode0600`, `TestBackupNeverOverwrites`); the database and key file are refused when group or world accessible | **In place** (item 1) |
| I11 | Timing reveals whether a username exists | A password hash is computed for every login, against a dummy hash made at start when the name is unknown; the error text and status are identical; the audit log does not record unknown names | **In place** (item 2; `TestEveryFailedLoginLooksTheSameAndComputesAHash` counts hashes, the acceptance suite compares timings with a generous bound) |
| I13 | An admin (or a stolen admin session) reads back a stored secret: edit an instance's URL to a server they control, omit the key, press Test, and read the key from that server's request log | A secret left out of a connection test or an update is filled from the stored one only while the destination is unchanged (scheme, host, port and path prefix, compared normalised: lower case, default port, trailing slash); otherwise the request is refused with "must be entered again when the URL changes" and nothing is sent (`TestStoredSecretIsNotSentToAChangedDestination`, `acceptance/config.test.ts`) | **In place** (item 10) |
| I12 | The setup token or a session is leaked through the log | The token is logged exactly once at start (that is how the operator finds it) and is useless after setup; session tokens, CSRF tokens and passwords are never logged or stored, and the access log has no headers, queries or bodies. The acceptance sweep plants all of them and scans responses, the log and the raw database and WAL | **In place** (item 2; `acceptance/secrets.test.ts`) |

### Denial of service

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| D1 | A2 sends an endless or huge body | 8 MiB default response cap with explicit per-endpoint caps (library lists and the torrent list 96 MiB, status, health and configuration calls 2 MiB), 8 s timeout covering the body, the stream is cut at the limit (`TestHTTPClientRejectsOversizedResponses`, `TestHTTPClientStopsReadingAnEndlessStream`, `TestHTTPClientTimesOutInsteadOfHanging`, `TestPerCallMaxBytesMayRaiseTheDefaultButNotAnExplicitCeiling`) | **In place** (cap lowered in item 10) |
| D2 | A2 redirects Wheelhouse elsewhere or in a loop | Redirects only within the same host, never https to http, loop limit (`TestHTTPClientRefusesRedirectsToAnotherHost`, `TestCheckRedirectRefusesHTTPSDowngrade`, `TestHTTPClientStopsRedirectLoops`) | **In place** |
| D3 | A1 holds connections open | Server read-header (10 s), read (30 s) and idle (120 s) timeouts, 64 KiB of headers, and at most 512 open connections (`app.MaxConnections`: a connection past it waits in the kernel backlog until one closes; `TestLimitListenerHoldsBackConnectionsPastTheLimitUntilOneCloses`). There is deliberately **no `WriteTimeout`**: library, codec and conversion-candidate requests can legitimately run longer than any fixed bound (they are bounded by the 8 s upstream timeout per call, not by one total), and nothing streams or long-polls, so the read side and the connection cap carry the protection | **In place** (item 10 added the connection cap) |
| D4 | A1 floods login | Per-account and per-address throttling and lockout, a cap of 3 concurrent password hashes, a bounded in-memory table, and audit rows written once per lock rather than per attempt (a locked key spends no hash and writes no row; a burst of parallel guesses cannot exceed the allowance). A lock never blocks the local CLI recovery path: `reset-password` (and an admin's reset) lifts it | **In place** (item 2; `TestPasswordResetLiftsTheLockout`, `TestThrottleTableIsBoundedAndKeepsLockouts`). An attacker can still lock out a *known* account name for up to 15 minutes at a time (accepted, as everywhere; existing sessions keep working and an administrator can reset) |
| D5 | One failing service takes the dashboard down | Each instance fails independently (`TestOneInstanceFailingNeverAffectsTheOthers`) | **In place** |
| D6 | A7 crashes ffmpeg or makes a conversion run forever | Child process under a context, one job at a time, cancel endpoint, the original kept as `.bak`; ffmpeg itself parsing hostile files is **accepted** (T2) | Partly in place (item 8: the queue survives a restart; a job running at the time is marked `interrupted`, never resumed or re-run from a stale row) |
| D7 | The disk fills (a huge conversion, an unbounded audit log) | Audit rows are small and written once per lock rather than per failed attempt; retention pruning (default 180 days) arrives with its settings; conversion needs space for a second copy and fails cleanly | Partly in place; pruning pending; disk exhaustion by conversion **accepted** |
| D8 | Concurrent CLI and server contention on the database | One writer connection, busy timeout, WAL so readers never block (`TestTwoHandlesShareTheFile`, `TestConcurrentReadersAndWriters`) | **In place** (item 1) |

| D9 | A2 turns a small body into a huge allocation (`[{},{},...]`: 8 MB became about 1.5 GiB, 250 times, and about 16 GB at the old 64 MiB cap) | Every adapter decodes through `core.DecodeJSON`: a scan of the body first refuses more than 200,000 elements per array or object (per call limits are lower for small endpoints), more than 12 million values, nesting deeper than 32 and strings over 1 MiB, with a message that never quotes the body; large decodes (256 KiB and up) take one of at most 2 to 4 process-wide slots, so many requests cannot multiply the memory; the soft memory limit (`GOMEMLIMIT`) is set to 80% of the container's cgroup limit when the operator has not set it (`TestEmptyObjectFloodIsRefusedBeforeItIsDecoded`: 24 MiB allocated instead of 1538 MiB for the same 8 MB body, `TestCheckJSONLimits`, `TestDecodeGateBoundsConcurrentLargeDecodes`, `TestMemoryLimitFollowsTheContainerLimit`, a flood test in each adapter) | **In place** (item 10). A genuine library that exceeds 96 MiB of JSON (roughly 25,000 movies at the usual 4 KB each) is refused with a clear error; that is the price of a bounded process |
| D10 | A7 or a slow disk makes `ffprobe` hang the queue | The duration probe has a 30 s timeout (as `ProbeHDR` always had) | **In place** (item 10; `TestHungDurationProbeTimesOut`) |

### Elevation of privilege

| ID | Threat | Mitigation | Status |
|---|---|---|---|
| E1 | A4 reaches an `admin` route | Role check by the `Guard`, read from the account on every request (a demotion is immediate); an enumerating test sends a `user` request to every admin route and expects 403 | **In place** (item 2; `acceptance/auth-levels.test.ts`) |
| E2 | A4 or A3 sets their own role or creates an admin | User management is `admin` only; a role is never taken from a request field of the acting user; strict JSON refuses unknown fields | **In place** (item 2; `TestUserManagementAPI`) |
| E3 | The bypass becomes admin | Capped at `user` in the code that builds the identity; the admin pages ask for the admin's login even from the local network; a test asserts it | **In place** (item 5; `TestBypassGrantsUserNeverAdmin`) |
| E4 | SQL injection | Statements use bound parameters; the only dynamic SQL is table and column names from a fixed list validated against `^[a-z][a-z0-9_]*$`; the SQLite connection runs in defensive mode with `trusted_schema=OFF`; `TestSQLIsNeverBuiltFromStringsOutsideTheSecretColumnCode` fails if any other file formats or concatenates SQL | **In place** (item 1); each later query is reviewed in item 10 |
| E5 | SSRF through provider URLs: an admin (or, before item 4, the operator) points a service at an internal address and uses Test connection as a scanner | Provider URLs must be `http`/`https` with a host and without credentials; responses are never echoed (only a redacted status). Blocking private ranges is **not** possible, because the services *are* on private addresses. Instance URLs are trusted administrator input. What is refused, at connect time and after DNS (so a rebinding name and a redirect are covered too), is link-local space and the cloud metadata addresses (`169.254.0.0/16`, `fe80::/10`, `fd00:ec2::254`, and since item 10 the NAT64 prefix `64:ff9b::/96`, Alibaba's `100.100.100.200` and Oracle's `192.0.0.192`), configurable with `WHEELHOUSE_DENIED_NETWORKS` (`none` to allow all; a custom list replaces the defaults, but a NAT64 address is always judged by the IPv4 address inside it: `TestMoreMetadataAddressesAreDeniedByDefault`, `TestNAT64IsJudgedByTheEmbeddedAddressUnderACustomList`, `TestHTTPClientRefusesMetadataAddresses`, `acceptance/config.test.ts`). Instance URLs with a fragment (`#...`, which swallows the API path) or a query string are refused (`TestDecodeRequiredEmptyNilAndWrongShapes`). Loopback and private ranges stay allowed | **Mitigated** (item 4), reviewed in item 10 |
| E6 | Code execution through a hostile media file (A7) in ffmpeg or ffprobe | Fixed argument shapes, no shell, no user-supplied filter strings; the image runs non-root and works read-only with capabilities dropped (the operator has to set those, T2). Every ffmpeg and ffprobe call that reads a media file gets `-protocol_whitelist file` before the input (so a playlist or container inside the file cannot make ffmpeg open `http:`, `tcp:`, `concat:` or other protocols) and ffmpeg gets `-nostdin`; the duration probe has the same 30 s timeout as the HDR probe (`TestFfmpegAndFfprobeOnlyReadLocalFilesAndNeverStdin`, `TestHungDurationProbeTimesOut`). Memory-safety bugs in ffmpeg are not Wheelhouse's to fix but are the main reason T2 matters; keep the image's ffmpeg current | **Accepted** (hardened in item 10) |
| E7 | A hostile file name (A7) as XSS or a path trick in the UI or a log | The UI is React with a CSP that forbids inline script; the access log quotes control characters in paths (`TestAccessLogQuotesControlCharactersInPath`); conversion refuses anything outside the roots | **In place** |
| E8 | A compromised dependency or image (A8) runs code in the process | See "Supply chain" below | Partly in place |
| E9 | Anyone can run the CLI | `backup`, `rekey`, `reset-password` and `forget-secrets` need `exec` in the container: whoever has it already has the process and the key (A9) | **Out of scope** |

## Supply chain (A8)

- **One third-party Go module family.** The server was standard library only.
  Phase 2 adds `modernc.org/sqlite` (with its pinned pure-Go dependencies, listed
  with licenses in the item 1 PR) and, in item 2, `golang.org/x/crypto` (BSD-3-Clause;
  only its `argon2` package and the `blake2b` package that needs end up in the binary). Every
  addition needs a written justification, the newest stable version, a clean
  `govulncheck`, and a license compatible with GPL-3.0.
- **CI:** `govulncheck` (pinned) and `npm audit` on every PR; a license check on
  the Go module graph; Dependabot weekly for Go, npm, Actions and base images,
  with a 7-day cooldown on version updates (security updates are not delayed).
- **Pinning:** GitHub Actions by commit SHA, base images and the QEMU binfmt
  image by tag and digest, `go.sum` and the npm lock files committed.
  `scripts/check-pins.sh` runs in the `check` job and fails a pull request that
  adds an unpinned action or `FROM` (exceptions need a reason in
  `scripts/check-pins.allow`). Not pinned by version: the Alpine packages (ffmpeg
  and drivers) apk installs at build time, which the SBOM records, and the
  `go run ...@version` tools, which `go.sum` and the module proxy checksum
  database verify.
- **Reproducibility:** the Go binary is built with `-trimpath` and a fixed
  toolchain; building it twice from different directories gives identical bytes
  (checked for linux/amd64 and linux/arm64). The image as a whole is not
  bit-for-bit reproducible because of the apk layer.
- **Release artifacts:** the release workflow publishes an SPDX SBOM and SLSA
  provenance (BuildKit, `mode=max`) with the image and a signed (Sigstore,
  keyless) GitHub build-provenance attestation of the image digest; users verify
  them with [docs/verifying-releases.md](verifying-releases.md). The Docker Hub
  token is meant to live in a `release` environment that only tag runs can use,
  and tags are meant to be immutable, which are repository settings the owner
  applies ([maintainer-settings.md](maintainer-settings.md)). The attestations
  exist from the first release built by this workflow; they prove where an image
  was built, not that the source is free of defects, and do not protect against a
  compromised maintainer account or release environment.
- **Residual:** `modernc.org/sqlite` is SQLite transpiled to Go. Its quality is
  that of its maintainers' tooling; a bug there is not caught by Go's memory
  safety in the same way as hand-written Go. The alternative (cgo SQLite) is not
  safer and costs the static binary and easy arm64.

## What the local-network bypass changes (item 5)

Off by default. An administrator turns it on with a list of CIDR ranges
(`PUT /api/admin/network`); the settings live in the database (table
`network_settings`), so a restored backup restores them. What changes, exactly:

- **Who is treated as signed in.** A request with no valid session whose client
  address is inside a range becomes a synthetic `user` named `local-network`
  (marked `Identity.Bypass`, no account, no session, no cookie, no throttling). It
  can use every `authenticated` route, including library deletes and conversions
  (item 5 of the residual risks). It can never reach `admin` routes (403), and the
  routes that act on an account (logout, password change, own sessions) answer
  401 so the UI asks for a real login. Login itself is unchanged: logging in from
  inside a range yields a real session, and **a real session always wins** (an
  administrator on the LAN is an administrator; a dead cookie falls back to the
  bypass). Setup is never covered: 503 "setup required" applies to everyone.
- **Which address counts.** The TCP peer. `X-Forwarded-For`, `Forwarded` and
  `X-Real-IP` are read only when the peer is a trusted proxy (union of
  `WHEELHOUSE_TRUSTED_PROXIES` and the stored list). Each chain is walked right
  to left and the first entry that is not itself a trusted proxy is the client
  (everything left of it is client-supplied). Several headers must agree; a
  malformed value (bad address, zone id, `unknown`, an empty element, an
  over-long chain) or a disagreement makes the client unknown and unknown matches
  no range. IPv4-mapped IPv6 addresses are normalised to IPv4 before matching,
  for the peer, the headers and the stored ranges.
- **CSRF.** The bypass has no cookie, so the ambient authority is the browser's
  network position: a hostile web page could make a LAN browser send state
  changes. Defences: the same-origin check applies unchanged (a foreign `Origin`
  or cross-site `Sec-Fetch-Site` is 403), and every state change must also carry
  `X-CSRF-Token`, a stateless token (HMAC of the client address under a key made
  at process start) that `GET /api/auth/session` returns to the bypass client. A
  cross-origin page cannot read that response, so it cannot learn the token. It
  changes when the server restarts or the client's address does; the UI refetches
  it. A script that worked without credentials in 0.2.x must fetch the session
  first and send the token. DNS rebinding would make an attacker's page
  same-origin and let it read the token, which is why the Host check below
  applies first (see T-9).
- **Which Host counts.** The bypass is granted only when the request's `Host`
  (port stripped, lower-cased, IPv6 literals unbracketed, trailing dot removed) is
  an IP literal, `localhost`, a name without a dot (cannot be registered in
  public DNS) or an entry of the allow-list: `WHEELHOUSE_ALLOWED_HOSTS` (read-only)
  plus the list stored with the network settings (at most 64 names; `*.example.lan`
  matches names below it; `*`, bare TLDs, IP addresses and malformed names are
  refused). Turning the bypass on from the UI adds the Host of that request. A request
  that fails the check gets the ordinary anonymous 401, including the CSRF token
  endpoint. The proxy must pass `Host` through unchanged. Residual: a host on
  the list is trusted to resolve only to the operator's address; a name whose DNS
  an attacker controls must not be listed.
- **Wide ranges.** Ranges that are not entirely inside private space (RFC 1918,
  100.64/10, loopback, 169.254/16, fc00::/7, fe80::/10) are accepted with a
  `warnings` entry that the admin page must show; `0.0.0.0/0` and `::/0` (and the
  IPv4-mapped equivalent) are refused, as are more than 64 entries per list and
  enabling with no ranges. Trusted-proxy ranges get the same warning.
- **Audit.** Changes to the settings (`network.update`, by the admin) and every
  add, delete and conversion by the bypass principal carry `via: bypass`, the
  resolved client address, and the TCP `peer` when a proxy forwarded the request.

## Security review findings (item 10, set 1: auth, host allow-list, web)

Three independent reviewers attacked a running build. Confirmed findings and their
status (each fix has a regression test that fails on the old code):

| Finding | Severity | Status |
|---|---|---|
| Login throttle race: N parallel guesses all passed the lock check before any failed, so 100 parallel logins let the right password through past the 5-failure lock; `ChangePassword` likewise | Medium | **Fixed.** In-flight reservations per key, lock re-checked after the semaphore, locked keys skip Argon2, bounded queue, one lockout row per lock |
| DNS rebinding reaches the local-network bypass (Host and Origin both attacker-controlled) | Medium | **Fixed** by the Host allow-list (above); migration 0006 |
| `safeRedirect` could return `//evil.example/x` (from `/.//evil.example/x`) | Low | **Fixed**: the normalised result is checked; backslashes and control characters rejected |
| `/.gitkeep` and `/.gitignore` served from the embedded UI | Low | **Fixed**: dot files are never served |
| No HSTS | Info | **Added**, only on HTTPS requests (direct TLS or a trusted proxy's `X-Forwarded-Proto: https`) |
| An account could be named like an audit actor (`local-network`, `cli`, `system`, `anonymous`) | Low | **Fixed** at create and rename (case-insensitive); an existing account with such a name keeps working |
| The setup token is logged to stderr | Info | **Accepted by design** (it is the delivery mechanism). The log line and docs say that anyone who can read the logs before setup completes can claim the instance; the Compose example publishes `127.0.0.1:3050` and the Podman unit explains `WHEELHOUSE_LISTEN` |
| `/healthz` discloses the version | Info | **Accepted**: needed by health checks and the update check; the version is also in the image tag |
| Trusted-proxy documentation did not show an appending nginx configuration | Docs | **Fixed** ([operations.md](operations.md#behind-a-reverse-proxy)) |
| Release notes and SECURITY.md claimed "no authentication" for every pre-release | Docs | **Fixed**: the note depends on the version |

**Per-address lockout behind one address.** Failures count per client address as
well as per account: 20 failures lock the address (an IPv6 /64) for 30 seconds,
doubling to 15 minutes. Behind a reverse proxy that is not in the trusted list,
or a NAT shared by many people, everyone shares one address, so one person's
guessing locks the others out of login. Administrators in that position should
list the proxy under trusted proxies so the real client address is used. This is
availability, not confidentiality (accepted, as for locking a known account name).

## Security review of a running build (Phase 2 item 10, set 2)

An independent reviewer attacked a running build with a hostile upstream (large,
deeply nested, slow, redirecting and echoing responses) and fake `ffmpeg` and
`ffprobe` tools. These are the findings in the converter, upstream handling and
SSRF policy, with their fixes. (Authentication and web findings are tracked in
the other set.)

| # | Severity | Finding | Fix and regression test | Status |
|---|---|---|---|---|
| 1 | Medium | Converting a file twice silently destroyed the original: the swap renamed over `<file>.bak`, replacing the first run's backup with the already converted file | The backup is created without ever replacing a file: a hard link (atomic, fails if the name exists, and it leaves the original in place until the converted file is renamed over it, so there is no moment without a file), or on filesystems without hard links (some NFS and SMB mounts) an `Lstat` check followed by a rename, which has a tiny race window between the check and the rename. A taken name moves to `.bak.1`, `.bak.2` and so on (up to 99; then the job fails and says so), and the job's `backupPath` names the file used. A failed swap rolls back without clobbering anything. `TestConvertingTwiceKeepsEveryOriginal`, `TestExistingBackupDirectoryIsSkipped`, `TestAllBackupNamesTakenRefusesTheJob`, `TestFailureMidSwapLeavesTheOriginalIntact`, `TestRenameFallbackNeverClobbers` | Fixed |
| 2 | Medium | Memory amplification from a hostile upstream (about 250 times the body size) | See D9 and D1 | Fixed |
| 3 | Low | An admin could read a stored secret back by editing the URL and pressing Test | See I13 | Fixed |
| 4 | Low | At every start any `X.bak` whose `X` was missing was renamed back to `X`, resurrecting stray or planted files | The swap is recorded in `conversion_jobs` (migration 0007: `swap_path`, `swap_backup`) before the first step and cleared when it finishes or is rolled back. At start only a job that still has a record is restored, from the recorded paths, which must be a backup name of the file, inside the media roots in force, and a regular file; a hard-link backup next to an untouched original is just removed; rows from before the migration have no record and restore nothing. `CleanupOrphans` now removes only `.wheelhouse-convert-*` temp files. If the record cannot be saved the swap does not start. `TestRecordedInterruptedSwapIsRestored`, `TestStrayBackupWithoutARecordIsNotRestored`, `TestRecordedSwapWithBadPathsIsIgnored`, `TestSwapRecordedByTheQueueSurvivesACrash`, `TestInterruptedHardLinkSwapDropsTheDuplicateBackup`, `TestStartupCleanupRestoresAndRemoves` | Fixed. With the in-memory queue (no database) nothing is recorded and nothing is restored; the server always has a database |
| 5 | Low | `probeDuration` had no timeout; ffmpeg and ffprobe could open non-file protocols named inside a media file | See E6 | Fixed |
| 6 | Low/Info | NAT64, Alibaba and Oracle metadata addresses were not denied; an instance URL with `#...` silently dropped the API path | See E5 | Fixed |
| 7 | Info | This document said the container runs read-only with capabilities dropped, but the Dockerfile only sets `USER` | T2 now separates what the image guarantees from what the operator configures; the compose example and the systemd unit enable the options by default; timeouts and the connection cap: see D3 | Fixed (docs and examples) |

## Fuzzing findings (road to 1.0.0)

Every parser or validator that handles untrusted input has a native Go fuzz
target (`func FuzzXxx`) with a seed corpus under `testdata/fuzz/` made from the
real fixtures and the hostile cases of the existing tests; `go test` replays the
seeds on every build, and the scheduled `fuzz.yml` workflow runs each target for
a couple of minutes a week (see [CONTRIBUTING.md](../CONTRIBUTING.md#fuzz-tests)).
Each target asserts invariants, not just "does not panic". What the fuzzing found,
with the regression entry that fails on the old code:

| # | Severity | Finding | Fix and regression test | Status |
|---|---|---|---|---|
| F1 | Low | A secret echoed percent-encoded in a mix of conventions was not redacted: a space as `%20` next to a mark (`! * ' ( )`) and a reserved character (`$ & + = : @`) matched none of the three encoders the redactor knew (`encodeURIComponent`, Go's query and path escapes). The same went for JSON written with `\uXXXX` escapes (Python, Java, .NET), PHP's `\/`, and HTML entities in an error page | `redact.go` now generates every combination of the three choices (and both hex cases), the ASCII-escaped and slash-escaped JSON forms, and both HTML entity styles. `FuzzRedact` seeds `bug_redact_*` | Fixed |
| F2 | Low | `MaxValues` in the JSON scan counted commas, so the first element of every container was never counted: `[[1],[1],[1]]` has two commas and six values. Bodies made of many one-element containers got up to a nesting depth's multiple of the intended total (bounded in practice by the byte cap) | The scan also counts a container's first element when the container closes non-empty, and the comparison is now exactly "more than `MaxValues`". `FuzzDecodeJSON` seeds `bug_values_*` | Fixed |
| F3 | Low | The Host allow-list matched a wildcard entry by suffix only: a Host header of `evil.example.com/.nas.example.lan`, `a..nas.example.lan`, `x y.nas.example.lan` or `.nas.example.lan` was granted the local-network bypass by `*.nas.example.lan`. A browser cannot send such a Host (it needs a name the attacker controls below the administrator's domain), so DNS rebinding was not reopened, but the allow-list is the control that stops it | A name from the lists must now be a valid domain (letters, digits and hyphens in dot-separated labels) before it is matched. `FuzzHostAllowed` seeds `bug_wildcard_labels` | Fixed |
| F4 | Low | `WHEELHOUSE_TRUSTED_PROXIES` kept an IPv4-mapped IPv6 range (`::ffff:10.0.0.0/104`) as written, but every peer address is compared unmapped, so such an entry trusted nobody: the proxy's forwarded headers were ignored without a word (it failed closed, so clients behind it all looked like the proxy, shared one login lockout and the cookie was never marked Secure). The stored list already converted these | The environment list converts a mapped range to IPv4 like the stored one and refuses one wider than /96. `FuzzTrustedProxies` seeds `bug_mapped` | Fixed |
| F5 | Low | qBittorrent's version is a plain-text answer, and whatever the address answered to `/api/v2/app/version` was shown as the version of the "successful" connection test and in `/api/status`: a reverse proxy's login page, an error page, up to 2 MiB of text. (Found against a hostile answer; it also meant a URL pointing at the wrong service tested as connected.) | The answer must look like a version (`5.0.1`, `4.6.7`, `5.0.0beta1`: dotted numbers, 32 bytes at most), else the call is a provider error that says the answer was unexpected. `FuzzUpstreamResponses` seeds `bug_qbit_version` | Fixed |
| F6 | Info | An instance URL with a space in it (a pasted trailing one: `http://192.0.2.10:7878/ `) passed validation, because `net/url` lets a space into the path, and every request then went to `/%20/api/...` with a confusing failure | A setting of the URL kind containing any white space is refused ("must be an http or https URL"). `FuzzInstanceURL` seed `bug_whitespace` | Fixed |

## Phase 2 items and the threats they close

| Item | Closes |
|---|---|
| 1 Threat model and storage foundation (this document) | T-4, T-5, T-6, I3, I5, I10, D8, E4 groundwork |
| 2 Users, sessions, login, CSRF, `authorize`, audit (merged) | S1, S3, S5, S7, T-2, T-9 (origin), E1, E2, I6, I11, I12, R1, R2, D4, and the server side of S2 |
| 3 First-run setup and recovery | The web side of S2 (setup page), the upgrade path for 0.2.x installs |
| 4 Stored configuration | I2, I3 end to end, I7 import path |
| 5 Local-network bypass (merged) | S4, E3, I8 |
| 6 and 7 Web | Route guards and admin pages: the UI half of the above; no new boundary. Done: the admin pages never receive a stored secret, secrets are not kept in browser storage or logged, one-time passwords live only in component state, and every destructive action asks first |
| 8 Converter settings (merged) | T-7 for configurable roots, HDR handling (no silent loss of HDR: excluded by default, acknowledged when allowed), D6 persisted queue |
| 9 Bugs and polish | arm64 image: no change to the model |
| 10 Security review | E5, D3, fuzzing of parsers (ffprobe output, upstream JSON: done, see "Fuzzing findings"), final dependency audit. Set 1 (auth, host allow-list, web): see "Security review findings" above |

## Residual risks (what stays true after Phase 2)

1. **A stolen `/config` volume is a full compromise** when the master key is the
   generated `secret.key`: the key sits beside the database. Only a key supplied
   from outside the volume changes that.
2. **Environment variables are readable** by anyone who can inspect the container
   or its process.
3. **Plain HTTP between Wheelhouse and its services** is accepted; a LAN
   attacker who can sniff or alter that traffic sees API keys sent in headers or
   query strings by the services' own protocols (SABnzbd, TMDB).
4. **ffmpeg and ffprobe parse hostile files.** Wheelhouse limits what they are
   asked to do, not what a bug in them can do. The container is the boundary.
5. **A user with the local-network bypass can delete and convert.** That is the
   design (they manage media); it is a convenience trade-off the admin makes
   explicitly, and it is off by default.
6. **The audit log is only as trustworthy as the database file.**
7. **Single process, single node.** No rate limiting beyond login throttling, no
   distributed lockout; a reverse proxy is the place for broader request limits.
8. **Backups restored to a different key** cannot be read; losing the key loses
   the stored provider credentials for good, by design (the server refuses to start
   until `wheelhouse forget-secrets --yes` discards them; accounts and sessions are
   unaffected because they are hashed, not encrypted). Keep the key apart from the
   backup; [operations.md](operations.md#key-loss) says how.
9. **Time-of-check races on the media filesystem** (a symlink swapped between
   the path check and the swap) are narrowed by re-checking before the swap, not
   eliminated; they need local write access to the media directories.
10. **Login state is in memory.** Lockouts and their back-off are forgotten at a
    restart, and an attacker who can lock a known account name can do so repeatedly.
    A session cookie stolen from a browser works until it expires (12 h idle, 30 d
    absolute) or is revoked; there is no second factor before 1.0.

## Keeping this current

`TestThereIsNoSwitchToDisableAuthentication` fails if an environment variable named like a
switch for authentication, CSRF or TLS verification appears (principle 1 of the plan).

Each PR that adds a route, a dependency, a file the process writes, or an
outgoing request path updates the relevant row, in the same PR. Item 10 repeats
the exercise from scratch with an independent reviewer and records the outcome
here.
