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

```sh
npm install
cp .env.example .env.local   # point at your Sonarr/Radarr/SABnzbd/etc.
npm run dev
```

Node 20+. Before opening a PR, run the same checks CI does:

```sh
npm run typecheck && npm run lint && npm run format:check && npm test && npm run build
```

`npm run format` fixes formatting. Tests use [Vitest](https://vitest.dev/)
and live next to the code as `*.test.ts`.

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
