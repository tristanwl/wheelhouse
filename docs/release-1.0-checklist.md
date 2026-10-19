# Release 1.0 checklist

What must be true before 1.0.0, and the evidence that it is. Written for the readiness audit
of 2026-10-19 on `main` at `3ae99e8` (release 0.3.0 plus the audit's own changes). Every row
names the test that enforces the criterion (a Go test by name, or an acceptance test file) and
what was done with it: **verified** (the test exists, was run and passes, and the behaviour was
also tried by hand against a locally built binary), **gap** (not met yet; says why) or **fixed in
this PR** (a test or a change was added by the audit). A criterion without a test would be a gap
by definition; the audit wrote the missing tests it found.

How the evidence was produced, all on 2026-10-19:

- `make -C server check`: gofmt, vet, `go test -race ./...` (all packages), golangci-lint,
  `govulncheck` (code affected by 0 vulnerabilities; see the note under criterion 12), `go mod tidy`, licenses, build.
- `go test -count=1 -v ./...` and every Go test named in `threat-model.md` and the plan matched
  against the output: 76 named tests, 76 passed, none skipped.
- The acceptance suite against the binary (175 tests, 16 files, all passed) and against the
  Docker image built from the repository with `acceptance/docker-server.sh` (173 passed, 2
  skipped: the two scenarios that need a fake ffmpeg on the host `PATH`), and
  `acceptance/docker-smoke.sh` (OK: any `--user`, read-only root, all capabilities dropped,
  health check, `reset-password` inside the container, SIGTERM).
- The web `npm test` (Vitest) and `npm run test:smoke` (Chromium, now including a 400 px width
  check of every page); `npm audit` for the root package and `web/` (0 vulnerabilities).
- By hand with the built binary on `127.0.0.1` and temporary config directories: the first-run
  flow, login, throttling, CSRF, role checks, the bypass with spoofed headers and a hostile
  `Host`, and the Docker Compose quick start from the README on a clean directory.
- `python3 scripts/check-docs.py` (links, file references and the environment variable table).

## Phase 2 acceptance criteria (docs/wheelhouse-phase2-plan.md)

| # | Criterion | Status | Evidence (tests) | Tried by hand | Date |
|---|---|---|---|---|---|
| 1 | A fresh container serves only setup until an admin exists; the setup token is required | verified | `acceptance/auth-setup.test.ts` ("answers health, the pages and the setup status, and nothing else", "answers 503 setup required on every other route", "refuses setup without the token, with a wrong one, and with a hostile origin", "creates the first administrator once, deletes the token"); Go `TestSetupNeedsTheTokenAndWorksOnce`, `TestSetupLosingTheRaceCreatesNothing` | Fresh binary: every API route 503, wrong token and a short password refused, token file 0600 and gone after setup | 2026-10-19 |
| 2 | Every route has a level; an unauthenticated request to a non-public route is 401 | verified | `TestEveryRouteHasAnAccessLevel`, `TestUnauthenticatedRequestsGet401OnEveryGuardedRoute`; `acceptance/auth-levels.test.ts` (a), which iterates `acceptance/routes.ts` (kept equal to the Go table by a test) | `GET /api/library`, `/api/admin/instances`, a delete: 401; `/healthz` 200 | 2026-10-19 |
| 3 | A `user` gets 403 on every admin route | verified | `TestAUserGets403OnEveryAdminRouteAndAdminsGetThrough`; `acceptance/auth-levels.test.ts` (b) | A user: 403 on `/api/admin/instances`, `/api/users`, `/api/audit` and `PUT /api/admin/network`; 200 on `/api/library` | 2026-10-19 |
| 4 | Sessions: cookie flags; logout and password change end sessions; idle and absolute timeouts | verified | `TestSessionCookieFlags`, `TestSessionsEndWhenIdleOrOld`, `TestPasswordChangeRotatesAndRevokes`; `acceptance/auth-sessions.test.ts` (flags, expiry report, new token at each login, logout, password change ends every session, `Secure` and `__Host-` only behind a trusted proxy) | `HttpOnly; SameSite=Strict; Path=/`, no `Secure` over plain HTTP or from an untrusted `X-Forwarded-Proto`; an old cookie replayed after logout: 401; a user who must change the password gets 403 everywhere else until they do | 2026-10-19 |
| 5 | CSRF: a state-changing request without the token or from a foreign origin is refused on every mutating route | verified | `TestEveryMutatingRouteRefusesMissingCSRFAndForeignOrigins`, `TestGETRoutesNeverChangeState`; `acceptance/auth-levels.test.ts` (c) | No token, foreign `Origin`, a `text/plain` body and a foreign-origin login: 403 or 400 | 2026-10-19 |
| 6 | Login throttling and lockout work and do not reveal whether a user exists | verified | `TestEveryFailedLoginLooksTheSameAndComputesAHash`, `TestAccountLockoutBacksOffAndLiftsAfterTheTime`, `TestParallelGuessesCannotPassTheAccountLock`; `acceptance/auth-throttle.test.ts`, `acceptance/auth-hardening.test.ts` | Six wrong passwords: the same body each time | 2026-10-19 |
| 7 | Provider secrets are encrypted in the database, never returned, never in logs, audit entries, errors or backups (canary sweep over the database file) | verified | `TestPlaintextNeverReachesDisk` (database, `-wal`, backup), `TestDeletedValuesAreOverwritten`, `TestKeyNeverPrints`, `TestSecretsAndKeysRedact`; provider and core canary tests; `acceptance/secrets.test.ts` and `acceptance/config.test.ts` ("keeps every secret out of responses, logs and the database files", "records every change in the audit log without a secret") | A saved key never appeared in any response; a key saved after a live `rekey` is lost, not leaked (see gap below and issue #83) | 2026-10-19 |
| 8 | The bypass never grants admin, ignores spoofed forwarded headers, respects the trusted-proxy list | verified | `TestBypassGrantsUserNeverAdmin`, `TestSpoofedForwardedHeadersAreIgnoredFromUntrustedPeers`, `TestBypassNeedsAnAcceptableHost`, `TestSpoofedHostAndOriginPairIsRefusedForTheBypass`, `server/internal/netaccess` tests; `acceptance/auth-bypass.test.ts`, `acceptance/auth-hardening.test.ts` | Range 10.0.0.0/8 and `X-Forwarded-For: 10.1.2.3`, `Forwarded`, `X-Real-IP` from the local peer: 401; range 127.0.0.0/8: users get 200, `/api/admin/*` and `/api/users` 403, a POST without the token 403, `Host: nas.example.lan` 401, `Host: nas` 200 | 2026-10-19 |
| 9 | Environment-only installs keep working (read-only seed) and can be imported | verified | `TestEnvironmentPrecedenceReadOnlyAndImport`; `acceptance/config.test.ts` ("environment instances": shows them read-only, imports them, then ignores the environment; credentials in a URL are never shown or stored); `acceptance/stack.test.ts` runs the whole suite on environment configuration | A Compose quick start with the shipped `.env.example` | 2026-10-19 |
| 10 | Converter: media roots configurable, HDR excluded by default, queue survives a restart | verified | `TestValidateRoots`, `TestSettingsPrecedencePersistenceAndRefusal`, `TestStaleRowOutsideTheRootsNeverRuns`, `TestStoredJobWithoutHDRAcknowledgementDoesNotRunHDRFile`, `TestRestartMarksInterruptedCleansUpAndResumesQueuedJobsInOrder`, `TestSwapRecordedByTheQueueSurvivesACrash`; `acceptance/converter.test.ts` (roots, "excludes HDR by default", "a restart mid-conversion", settings and jobs after a restart) | Not driven by hand: a real conversion needs real media and ffmpeg (acceptance uses a fake ffmpeg) | 2026-10-19 |
| 11 | The acceptance suite passes in authenticated mode against the binary and the image; the Phase 1 and migration criteria still pass | verified | Binary: 175 of 175. Image: 173 passed, 2 skipped (fake ffmpeg scenarios), plus `acceptance/docker-smoke.sh`. Phase 1 criteria: see the next table | | 2026-10-19 |
| 12 | No known vulnerabilities; signed images with provenance and an SBOM; `SECURITY.md` current; threat model published | **verified in the workflow, open until the first release** | `govulncheck`: code affected by 0 vulnerabilities (`GO-2026-5932`, the unmaintained `golang.org/x/crypto/openpgp` package, is in a module Wheelhouse requires but is not imported or called; only `argon2` and `blake2b` are used). `npm audit`: 0 in the root package and `web/`. `SECURITY.md` and `docs/threat-model.md` brought up to date in this PR. The release workflow (merged in #82) attaches an SPDX SBOM and BuildKit SLSA provenance and signs the provenance of the image digest with a GitHub artifact attestation (Sigstore keyless, not a cosign key pair; how to check it is in [verifying-releases.md](verifying-releases.md)), and its dry run checks that both attestations exist for each platform. **Not verifiable until a real release exists:** the first tagged release must be checked with those commands (`gh attestation verify`), and no release before it has attestations | | 2026-10-19 |

Beyond the 12: a **gap** that does not block the criteria above but should be known before
tagging: the audit-log retention (180 days, decided in the plan) is not enforced and rows are
kept (issue #84); `rekey` and `forget-secrets` do not refuse to run against a live server (issue #83: the
documentation now says to run them with the server stopped, in a throwaway container).

## Phase 1 acceptance criteria (docs/wheelhouse-spec.md)

The Phase 1 criteria were written for the Next.js app; the same behaviours are now enforced by the
Go server and the acceptance suite (`docs/wheelhouse-spec-phase2.md`, migration step 8).

| Criterion | Status | Evidence | Date |
|---|---|---|---|
| With only the old env vars set, the app behaves as before | verified | `acceptance/stack.test.ts` (instance ids `sonarr`, `radarr`, `sabnzbd`, `plex`; library, downloads, storage, explore, features), `acceptance/mutations.test.ts` | 2026-10-19 |
| `QBITTORRENT_*` shows qBittorrent downloads, including seeding | verified | `acceptance/stack.test.ts` ("normalizes rows from both download clients"); the qBittorrent adapter and contract tests | 2026-10-19 |
| `JELLYFIN_*` instead of `PLEX_*` shows a Jellyfin card | verified | `acceptance/stack.test.ts` (`/api/status`), the Jellyfin adapter tests | 2026-10-19 |
| Two Radarr instances appear in status, storage and library; Explore's "In library" badge reflects both | verified | `acceptance/stack.test.ts` (second Radarr, merged library, Explore badge), `acceptance/mutations.test.ts` (add to the second Radarr) | 2026-10-19 |
| With no Sonarr or Radarr, the app boots, the Dashboard works and the rest is hidden or stubbed | verified | `acceptance/boot.test.ts` (no environment: every page 200, only the Dashboard enabled); web `routes.test.tsx` (stubs); the Compose quick start and the first-run browser walk-through | 2026-10-19 |
| No hang longer than the timeout when a service is down | verified | `acceptance/failures-slow.test.ts`, `TestHTTPClientTimesOutInsteadOfHanging` | 2026-10-19 |
| No API key or token in any API response or log line | verified | `acceptance/secrets.test.ts`, `providers/core/canary_test.go`, each provider's `canary_test.go` | 2026-10-19 |

## Threat model: every mitigation marked as in place

Each row of [threat-model.md](threat-model.md) marked **in place** was checked against the
tests it names (all 76 named tests ran and passed on 2026-10-19; none is skipped). Where the row
named no test, the table says which test now enforces it.

| Row | Evidence | Status |
|---|---|---|
| S1, S2, S3, S4, S5, S7 | the tests named in criteria 1 to 6 and 8 | verified |
| S6 TLS verification always on | `TestHTTPClientVerifiesTLSCertificates`; **new** `TestTLSVerificationIsNeverDisabled` (no `InsecureSkipVerify` anywhere in production code) | verified, test added |
| T-1 deletes need confirmation, conversions keep a `.bak` | `TestDeleteNeedsServerSideConfirmation`, `TestConvertHardlinkNeedsConfirmation`, `acceptance/mutations.test.ts` | verified |
| T-2, T-4, T-5, T-6, T-7 | `TestAUserGets403OnEveryAdminRouteAndAdminsGetThrough`, `TestTheLastAdminSurvivesCodeAndTriggers`, `TestTamperDetectionEveryByte`, `TestRefusesNewerSchema`, `TestRefusesChangedOrDamagedHistory`, `TestCrashMidTransactionLeavesNoTrace`, `TestResolveInRoots`, `TestValidateRoots` | verified |
| T-8 ffmpeg arguments never from a request, never a shell | `TestArgsPerBackend`, `TestEveryBackendArgsHaveTheWhitelistBeforeTheInput`; **new** `TestNoShellIsEverStartedAndOnlyFfmpegCodeRunsPrograms` (no shell, no program started outside the converter and the ffmpeg probe) | verified, test added |
| T-9 hostile Host and Origin | the bypass and CSRF tests above; checked by hand | verified |
| R1, R2 audit log | `TestAuditTrailHasNoSecretsAndCannotBeEdited`, `acceptance/auth-users.test.ts` | verified |
| I1, I2, I12 no secret in responses, errors, logs | `acceptance/secrets.test.ts`, `acceptance/config.test.ts`, the canary tests | verified |
| I3, I5, I10, I11 | `TestPlaintextNeverReachesDisk`, `TestKeyNeverPrints`, `TestSecretsAndKeysRedact`, `TestRecoverHidesDetails`, `TestBackupCopiesEverythingWithMode0600`, `TestBackupNeverOverwrites`, `TestEveryFailedLoginLooksTheSameAndComputesAHash` | verified |
| I6, I8 | criteria 2, 3 and 8 | verified |
| I9 browser hardening | `TestSecurityHeaders`, `TestCSPAllowsExactlyTheImageHosts`; the browser smoke test fails on a CSP violation, and the served HTML is checked for inline script and style | verified |
| I13 stored secret not sent to a changed destination | `TestStoredSecretIsNotSentToAChangedDestination`, `acceptance/config.test.ts` | verified |
| D1 to D5, D8 to D10 | `TestHTTPClientRejectsOversizedResponses`, `TestHTTPClientStopsReadingAnEndlessStream`, `TestCheckRedirectRefusesHTTPSDowngrade`, `TestLimitListenerHoldsBackConnectionsPastTheLimitUntilOneCloses`, `TestPasswordResetLiftsTheLockout`, `TestOneInstanceFailingNeverAffectsTheOthers`, `TestConcurrentReadersAndWriters`, `TestEmptyObjectFloodIsRefusedBeforeItIsDecoded`, `TestCheckJSONLimits`, `TestHungDurationProbeTimesOut` | verified |
| D6 queue survives a restart | `TestRestartMarksInterruptedCleansUpAndResumesQueuedJobsInOrder`, `acceptance/converter.test.ts` | verified (partly in place, as the row says) |
| D7 the audit table grows | no enforcing code or test | **gap**, issue #84 (Known limitations) |
| E1, E2, E3 | `acceptance/auth-levels.test.ts`, `TestUserManagementAPI`, `TestBypassGrantsUserNeverAdmin` | verified |
| E4 SQL injection | bound parameters everywhere; **new** `TestSQLIsNeverBuiltFromStringsOutsideTheSecretColumnCode` (only `store/secrets.go` may format SQL, from the fixed `SecretColumns` list; no concatenated statements) | verified, test added |
| E5 SSRF | `TestHTTPClientRefusesMetadataAddresses`, `TestMoreMetadataAddressesAreDeniedByDefault`, `TestNAT64IsJudgedByTheEmbeddedAddressUnderACustomList`, `acceptance/config.test.ts`; by hand: an instance at `169.254.169.254` can be saved but every request is refused with `address not allowed (link-local and cloud-metadata addresses are refused)` | verified |
| E6, E7 | `TestFfmpegAndFfprobeOnlyReadLocalFilesAndNeverStdin`, `TestAccessLogQuotesControlCharactersInPath` | verified (E6 accepted risk) |
| E8 supply chain | CI `govulncheck`, `npm audit`, licence check, Dependabot, `scripts/check-pins.sh` (actions by SHA, images by digest), SBOM and signed provenance in the release workflow | verified except the first real release (criterion 12) |
| **New:** no switch turns security off | `TestThereIsNoSwitchToDisableAuthentication` (no environment variable, in code or in the README, named like a security switch) | verified, test added |

## Documentation checks (done in this PR)

- Every command in the README quick start, first run, backup, `reset-password`, `rekey` and
  `forget-secrets` sections was run as written against the built binary and against a Compose project
  made from `docker-compose.example.yml` (with the locally built image). Found and fixed: the quick
  start's `.env.example` made every service show as broken ("apiKey: is required") because it set
  URLs without keys (everything in it is now commented out); `make -C server run`,
  used by the README, CONTRIBUTING and CLAUDE.md, failed on any machine without a `/config`
  directory (it now uses `server/.devconfig`); `rekey` and `forget-secrets` were shown with
  `docker exec`, which cannot work for a server that does not start and makes the credentials
  saved afterwards unreadable when it does; the status banner and the in-app stubs still said that
  0.2.x was the current release and pointed at the README instead of the admin pages.
- The README now has one table of every environment variable the server reads (name, default,
  meaning, secret or read-only seed). `scripts/check-docs.py` fails CI when the code and that
  table differ, when a relative link or an anchor does not resolve, and when a `code span` naming a
  file in this repository does not exist. It runs in the existing `check` job.
- [troubleshooting.md](troubleshooting.md) is new.
- Historical notes about the 0.1.x Next.js app stay where they are labelled as history
  (`docs/wheelhouse-spec.md` banner, `server/README.md`, `acceptance/README.md`).

## First-run usability review (done in this PR)

Walked at 400 px wide in headless Chromium: setup wizard, dashboard, Library, Explore and Codecs
stubs, account, and every admin page. Fixed: the header (Library, Explore, Codecs, Admin and the
user) did not wrap and made every page scroll sideways when all features were on; the Users and
Audit tables leaked a screen-reader-only element outside their scroll container (the Users
page was 618 px wide); the Dashboard had no heading level 1; every page had the same
tab title; the empty-state texts told an administrator to read the README instead of
linking to Admin, Services (and a plain user to ask an administrator). The smoke test now fails
if any main or admin page needs sideways scrolling at 400 px. Form labels, error announcements
(`role="alert"`), focus handling on the setup and login forms and keyboard operation of the
switches were checked and needed no change.

## Could not verify

- A real conversion with real media, and hardware encoding of any kind (no GPU, no media here).
- Anything on arm64 (no arm64 host; the image is built for it by the release workflow only).
- Podman: the unit file and `podman run` instructions were read and compared with the Docker
  behaviour, not run (Podman is not installed on the audit machine).
- Signed images, provenance and SBOM on a real published image (criterion 12: the workflow is
  merged and its dry run checks the attestations, but only a tagged release produces them), and the
  Docker Hub `latest` tag rule (starts at 1.0.0; the release workflow has no `latest` until then).
- Behaviour behind a real reverse proxy and over HTTPS (acceptance tests simulate it with
  headers from a trusted peer).
- Browsers other than headless Chromium, and screen readers.

## Known limitations

These are deliberate or accepted for 1.0. The threat model has the reasoning; each is also a
promise not made, so please read it before exposing Wheelhouse beyond your home network.

**Accounts and login**

- **No second factor and no API tokens.** Sign-in is a password and a session cookie. There is no
  TOTP, no personal access token, no single sign-on (OIDC). Scripts must log in and carry the session
  and CSRF token. These were decided for after 1.0.
- **Lockout state is in memory.** A restart clears the counters, and an attacker who knows a
  username can keep that account locked for up to 15 minutes at a time (an administrator or
  `wheelhouse reset-password` lifts it).
- **The per-address lockout sees one address behind a shared proxy.** Twenty wrong passwords from
  one address lock that address; behind a NAT or a reverse proxy that is not in
  `WHEELHOUSE_TRUSTED_PROXIES`, that means everyone. List the proxy as trusted.
- **A stolen session cookie works** until it expires (12 hours idle, 30 days in total) or an
  administrator or the user ends it.
- **No email.** A lost password is recovered by another administrator or with
  `wheelhouse reset-password` inside the container.

**Network and trust**

- **DNS rebinding protection needs the host allow-list.** The local-network bypass is only
  granted when the request's `Host` is an IP address, `localhost`, a single-label name, or on the
  allowed host names list. A dotted name that is not listed gets a login page, by design.
- **Administrators are trusted for service URLs.** Wheelhouse refuses link-local and cloud-metadata
  addresses, but a service address on your LAN is exactly what it must connect to, so an
  administrator (or someone holding an administrator session) can use "Test connection" to probe your
  private network. Do not give the administrator role to people you would not give LAN access.
- **Plain HTTP to your services and from your browser.** Wheelhouse speaks HTTP; TLS is your reverse
  proxy's job, and traffic to services on your LAN (SABnzbd and TMDB send keys in the URL) is as safe
  as that LAN.
- **People with the local-network bypass can delete and convert media.** They never see
  configuration, users or secrets.

**Secrets and data**

- **The master key sits next to the database by default** (`/config/secret.key`), so a stolen copy
  of the whole volume reveals the stored credentials. Supply the key from a secret mount with
  `WHEELHOUSE_SECRET_KEY_FILE` to avoid that. Losing the key loses the stored credentials for good
  ([operations.md](operations.md#key-loss)); users and sessions are unaffected.
- **Environment variables are readable** by anyone who can inspect the container.
- **`rekey` and `forget-secrets` must be run with the server stopped**; nothing enforces it yet
  (issue #83).
- **The audit log is not pruned** (rows are kept; issue #84), and it is only as trustworthy as the
  database file.
- **There is no downgrade** after a schema migration, and no built-in backup schedule: take
  backups yourself, and keep a copy of the key and of `settings.json` (not in the backup).

**Media and conversion**

- **Library responses are capped at 96 MiB of JSON** (about 25,000 movies at the usual 4 KB
  each). A larger library gets an error instead of a list (issue #88). Other upstream
  responses have smaller caps.
- **Hardware encoding is untested on arm64**, and the arm64 image has no Intel VAAPI drivers;
  software x264/x265 works (issue #87). NVENC and other hardware paths are likewise only as
  tested as the amd64 VAAPI one.
- **Conversion writes 8-bit SDR** without tone mapping, so HDR and Dolby Vision files are excluded
  by default and need an explicit acknowledgement; one job runs at a time; ffmpeg parses files you
  hand it, and the container is the boundary if a file is hostile.
- **Time-of-check races on the media filesystem** are narrowed by re-checking before the swap, not
  eliminated; exploiting one needs write access to your media directories.

**Releases and operation**

- **Releases before the first one built by the current workflow have no signed provenance or
  SBOM** ([verifying-releases.md](verifying-releases.md)); the 0.x images cannot be verified.
- **One process, one node.** No rate limiting beyond login throttling and a cap of 512 open
  connections; a reverse proxy is the place for broader limits.
- **The image's `HEALTHCHECK`, memory limit and read-only root** are only as good as the options
  you pass; the example Compose file and unit file set them.
- **Interface:** English only; on a phone the Administration tabs scroll sideways without a
  visible hint (issue #86).
