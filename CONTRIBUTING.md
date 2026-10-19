# Contributing to Wheelhouse

Thanks for helping out. Wheelhouse is early, so the most useful things right
now are bug reports, testing on different hardware and stacks, and small,
focused pull requests.

## Before you start

- For anything bigger than a bug fix, open an issue first so we can agree on
  the approach. The planned direction (swappable providers, auth, setup
  wizard) is in [docs/wheelhouse-spec.md](docs/wheelhouse-spec.md); work that
  fits it is easiest to merge.
- Wheelhouse is a companion for the *arr stack. New *arr-family manager
  support is in scope; replacing Sonarr/Radarr is not.

## Development setup

Wheelhouse is a Go server (`server/`, standard library plus the SQLite driver and `golang.org/x/crypto` for Argon2id) with a Vite UI
(`web/`) embedded in the binary. You need Go 1.27+ and Node 22.12+ (24
recommended, see `.nvmrc`); Docker only for the image.

```sh
set -a; . ./.env; set +a          # your services, from .env.example
make -C server run                # the API on http://localhost:3000
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

# the image (job "docker")
docker build -t wheelhouse:dev . && acceptance/docker-smoke.sh wheelhouse:dev
WHEELHOUSE_CMD="acceptance/docker-server.sh wheelhouse:dev" npm run test:acceptance
```

`npm run format` (in the root and in `web/`) fixes formatting. Go tests live
next to the code as `*_test.go`, the UI's tests as `*.test.ts(x)` (Vitest), and
the black-box HTTP suite is in `acceptance/` ([acceptance/README.md](acceptance/README.md)):
a new API route must be added to `acceptance/routes.ts`.

### Fuzz tests

Parsers and validators that handle untrusted input have Go fuzz targets
(`func FuzzXxx(f *testing.F)`) with committed seed corpora under
`testdata/fuzz/`; `go test` replays the seeds, so they cost nothing extra in a
PR. To explore beyond the seeds, run one target for a while:

```sh
cd server && go test ./internal/netaccess -run '^$' -fuzz '^FuzzHostAllowed$' -fuzztime 2m
```

A failure writes the minimized input to `testdata/fuzz/<FuzzName>/`: fix the
bug, commit that file as its regression test (one commit per bug) and add a row
to the "Fuzzing findings" table in [docs/threat-model.md](docs/threat-model.md).
If you add or change a parser, validator or decoder for untrusted input, add a
target that states what must hold for every input (not just "does not panic"),
seed it from real inputs, and add it to the matrix of
[.github/workflows/fuzz.yml](.github/workflows/fuzz.yml) (weekly, two minutes
per target; a Go test checks the list). The details and the list of targets are in
[server/README.md](server/README.md#fuzz-tests).

The tools for `make check` are installed separately, see
[server/README.md](server/README.md). Without Make, the commands are in the
`server` job of [.github/workflows/ci.yml](.github/workflows/ci.yml).

## Pull requests

- Keep PRs small and focused; one change per PR.
- Match the surrounding code's style and comment density.
- Don't add destructive actions (deletes, file rewrites) without an in-app
  confirm step.
- Never commit API keys, tokens, `.env` or `.env.local`.
- Describe what you tested, and on what stack (OS, GPU, which services).

## Sign your commits (DCO)

We use the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. Certify that you wrote the change or have the right to
submit it by adding a sign-off to each commit:

```sh
git commit -s -m "Describe the change"
```

This adds a `Signed-off-by: Your Name <you@example.com>` line.

## License

By contributing, you agree that your contributions are licensed under the
[GPL-3.0](LICENSE).
