# Contributing to Wheelhouse

Thanks for helping out. Wheelhouse is young, so the most useful things right
now are bug reports, testing on different hardware and stacks, and small,
focused pull requests.

## Before you start

- For anything bigger than a bug fix, open an issue first so we can agree on
  the approach. The direction (providers, accounts, stored configuration, the
  converter) is in [docs/wheelhouse-spec.md](docs/wheelhouse-spec.md) and
  [docs/wheelhouse-phase2-plan.md](docs/wheelhouse-phase2-plan.md); work that fits it is
  easiest to merge. What is deliberately out of scope for 1.0 is under
  [Known limitations](docs/release-1.0-checklist.md#known-limitations).
- Security problems are not issues: see [SECURITY.md](SECURITY.md) (private
  vulnerability reporting).
- Wheelhouse is a companion for the *arr stack. New *arr-family manager
  support is in scope; replacing Sonarr/Radarr is not.

## Development setup

Wheelhouse is a Go server (`server/`, standard library plus the SQLite driver and `golang.org/x/crypto` for Argon2id) with a Vite UI
(`web/`) embedded in the binary. You need Go 1.27+ and Node 22.12+ (24
recommended, see `.nvmrc`); Docker only for the image.

```sh
make -C server run                # the API on http://localhost:3000; config in server/.devconfig
                                  # (create the administrator with the token in its log, then add services
                                  # under Admin; optionally `set -a; . ./.env; set +a` first, from .env.example)
cd web && npm ci && npm run dev   # the UI on http://localhost:5173, proxying /api
```

Before opening a PR, run what CI runs for the parts you touched (the CI jobs
are `check`, `acceptance`, `server`, `web` and `docker`):

```sh
# server (job "server"); also needs golangci-lint v2 and govulncheck
make -C server check   # gofmt, vet, test -race, lint, govulncheck, deps (go.mod tidy, licenses), build

# UI (job "web"); the smoke test needs Chromium (`npx playwright-core install chromium`)
cd web && npm ci && npm run typecheck && npm run lint && npm run format:check \
  && npm test && npm run build && npm run embed && npm run test:smoke

# acceptance suite on the real binary (job "acceptance") and repository tooling (job "check")
npm ci && make -C server build && npm run test:acceptance
npm run typecheck && npm run format:check
shellcheck acceptance/*.sh scripts/*.sh && scripts/check-pins.sh   # every action SHA-pinned, every base image digest-pinned
python3 scripts/check-docs.py     # links, file references and the environment variable table in the docs

# the image (job "docker")
docker build -t wheelhouse:dev . && acceptance/docker-smoke.sh wheelhouse:dev
WHEELHOUSE_CMD="acceptance/docker-server.sh wheelhouse:dev" npm run test:acceptance
```

`npm run format` (in the root and in `web/`) fixes formatting. Go tests live
next to the code as `*_test.go`, the UI's tests as `*.test.ts(x)` (Vitest), and
the black-box HTTP suite is in `acceptance/` ([acceptance/README.md](acceptance/README.md)):
a new API route must be added to `acceptance/routes.ts`.

The tools for `make check` are installed separately, see
[server/README.md](server/README.md). Without Make, the commands are in the
`server` job of [.github/workflows/ci.yml](.github/workflows/ci.yml).

## Environment variables for development and tests

Operators' variables are in the README's Configuration table. These are read only by tests
and tooling: `WHEELHOUSE_CMD` (the command the acceptance suite starts), `WHEELHOUSE_API` (where
`npm run dev` proxies `/api`), `WHEELHOUSE_BIN` and `WHEELHOUSE_SMOKE_REQUIRED` (the browser
smoke test), `UPDATE_PROVIDER_SNAPSHOT=1` (regenerates the provider fixture the web tests
use), `WHEELHOUSE_BIG_FIXTURE` (size of a hostile-library test) and
`WHEELHOUSE_STORE_CRASH_HELPER` / `WHEELHOUSE_STORE_CRASH_MODE` (the store's crash test).

## Pull requests

- Keep PRs small and focused; one change per PR.
- Match the surrounding code's style and comment density.
- Don't add destructive actions (deletes, file rewrites) without an in-app
  confirm step.
- Never commit API keys, tokens, `.env` or `.env.local`, and keep real
  hostnames, IPs and library titles out of code, docs, fixtures and tests (this
  repository is public).
- Pin new GitHub Actions by full commit SHA (with a `# vX.Y.Z` comment) and new
  Docker base images by digest; `scripts/check-pins.sh` fails the `check` job
  otherwise. Explain every new dependency in the PR.
- Describe what you tested, and on what stack (OS, GPU, which services).

## Sign your commits (DCO) and with a key

`main` requires signed commits (GPG, SSH or S/MIME signatures that GitHub shows
as "Verified"; `git commit -S`, or set `commit.gpgsign` to true), and every
commit needs the DCO sign-off below. A pull request with unsigned commits
cannot be merged. The settings that protect `main` and releases are listed in
[docs/maintainer-settings.md](docs/maintainer-settings.md).

### DCO sign-off

We use the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. Certify that you wrote the change or have the right to
submit it by adding a sign-off to each commit:

```sh
git commit -s -S -m "Describe the change"
```

This adds a `Signed-off-by: Your Name <you@example.com>` line.

## License

By contributing, you agree that your contributions are licensed under the
[GPL-3.0](LICENSE).
