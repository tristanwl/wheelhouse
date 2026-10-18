# Wheelhouse

A self-hosted dashboard and companion for the *arr media stack: status, library
management, movie discovery and codec-aware conversion. Alpha: there is **no
authentication yet**, so it is for trusted networks only.

Read [docs/architecture.md](docs/architecture.md) for how things work and
[docs/wheelhouse-spec.md](docs/wheelhouse-spec.md) for decisions, positioning
and the roadmap. [CONTRIBUTING.md](CONTRIBUTING.md) covers the human workflow.

## Commands

Node 22.12+ (24 recommended, see `.nvmrc`).

```sh
npm ci
npm run dev            # http://localhost:3000, point it at services via .env.local
npm test               # vitest; npm run test:watch while iterating
```

Before opening a PR, run what CI runs (all five must pass):

```sh
npm run typecheck && npm run lint && npm run format:check && npm test && npm run build
```

`npm run format` fixes formatting. The built app must also boot with **no**
environment variables (every page and API answers 200; only the Dashboard is
enabled): `npm run build`, then run `.next/standalone/server.js`.

`npm run test:acceptance` (after a build) runs the black-box HTTP suite in
`acceptance/` against the built server; CI runs it as its own job. It is the
definition of done for the Go port, so a new route must be added to
`acceptance/routes.ts` (see `acceptance/README.md`).

## How it is built

Next.js 15 (App Router) + React 19, SWR for polling, Tailwind. No database.

Everything that talks to another service goes through the provider layer in
`lib/providers/`:

- **Roles:** `downloadClient`, `mediaManager`, `mediaServer`, `metadata`. Each
  role has a `types.ts` (the normalized model), a reusable `contract.ts` suite
  every adapter must pass, and one file per provider.
- **A provider** is a `defineProvider({ id, role, configSchema (zod),
  secretKeys, capabilities, create })`. It is registered in
  `lib/providers/index.ts`. Config comes from a `ConfigSource`: environment
  variables today (`lib/providers/core/config-source.ts`), a database later.
- **Routes** never import a service client directly: they ask the runtime
  (`getRuntime()`) for the ready instances of a role and iterate them, so every
  route works with several instances (Radarr and Radarr 4K) and with none.
- **Features** (Dashboard, Library, Explore, Codecs) are gated by
  `lib/features/registry.ts`, which decides from the configured instances what
  the nav shows and which pages show a "not configured" stub.
- Pure helpers the browser also uses live in `lib/status.ts`,
  `lib/download-rows.ts` and `lib/format.ts` (no server-only imports there).

## Rules that matter

- **Secrets never leave the server.** Use `createHttpClient({ secrets })` for
  upstream calls and `redact()` for anything echoed. API keys, tokens and URL
  credentials must not appear in any response body, header, error message or
  log line. `tests/phase1-acceptance.test.ts` plants canary secrets and scans
  every GET route; when you add a route or provider, make sure it is covered,
  and never weaken that test.
- **Adapters normalize.** Sizes in bytes, progress 0..1, durations in seconds,
  states from the shared enums. An unreachable service throws a `ProviderError`
  (the caller reports it as down); a wrong key is a clear "API key incorrect".
  User-facing notices never name configuration keys or env vars.
- **Fixtures come from real responses**, sanitized. Each service answers
  errors differently (SABnzbd: HTTP 403 plain text; Plex: 401 with an HTML page;
  qBittorrent v4 and v5 differ on login), so don't invent shapes: run the real
  service (Docker is fine), capture, then anonymize.
- **This is a public repo.** No real hostnames, IPs, library titles, hashes,
  usernames or keys in code, fixtures, docs or tests. Use `example.com`, RFC 5737
  addresses (`192.0.2.x`) and placeholder titles ("Example Movie 1").
- **Destructive actions need an in-app confirm.** Conversion keeps a `.bak` and
  warns when a file is hardlinked (it may be seeding). Don't add a delete or
  rewrite without both.
- **Next.js 15:** dynamic route `params` are Promises (`await params`); client
  pages read them with `useParams()`.
- **Test helpers:** passing `undefined` to a parameter with a default silently
  uses the default. Use an explicit `null` for "none" in helpers.

## Adding a provider

1. Types in the role's `types.ts` if the model needs anything new (prefer
   fitting the existing one; extend the contract suite if you change it).
2. `lib/providers/<role>/<name>.ts` with `defineProvider`, and register it in
   `lib/providers/index.ts`.
3. Env mapping in `ENV_MAPPINGS` (`config-source.ts`), plus README config table
   and `.env.example`.
4. Fixtures + a fake server in `__fixtures__/<name>/`, adapter tests, and run
   the role's contract suite against it.
5. An integration test that calls the real route handlers through
   `EnvConfigSource` and the runtime with your fake configured.
6. Add it to the canary sweep and update `docs/wheelhouse-spec.md` /
   `docs/architecture.md`.

## Commits, PRs and releases

- Sign off every commit (`git commit -s`, DCO). Small, focused PRs; describe how
  you tested it and against what. Keep the formatting-only commit separate from
  behaviour changes.
- Don't add "Generated with Claude Code" (or similar) footers to commits, PR
  descriptions or issues.
- Merge with a merge commit, not a squash, so separate commits stay separate.
- Only a maintainer cuts releases. The tag (or GitHub release) is the bare
  version with no `v` prefix (`0.1.0`) and **must equal `package.json`'s
  `version`**: bump it in a PR first. `.github/workflows/release.yml` runs the
  checks, pushes `tristanwl/wheelhouse:<version>` to Docker Hub and creates or
  updates the GitHub release. Every `0.x` is a pre-release, and there is no
  `latest` tag until authentication ships.
- Claude Code keeps isolated worktrees in `.claude/`; it is ignored by git,
  tsc, ESLint and Vitest. Never commit it.
