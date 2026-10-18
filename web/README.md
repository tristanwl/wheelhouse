# Wheelhouse web UI

The single-page UI served by the Go server in [../server](../server/README.md):
Vite, React 19, React Router (data router, library mode), TypeScript, Tailwind CSS 4,
SWR, Vitest + Testing Library, ESLint (flat config) and Prettier. Migration step 6 of
[../docs/wheelhouse-spec-phase2.md](../docs/wheelhouse-spec-phase2.md); since release
0.2.0 it is the only UI (it replaced the Next app's pages, with the same URLs
`/`, `/library`, `/explore`, `/explore/:category`, `/codecs`, polling intervals and look).

Node 22.12+ (24 recommended).

```sh
npm ci
npm run dev           # http://localhost:5173, proxies /api to the Go server
npm run typecheck && npm run lint && npm run format:check && npm test && npm run build
```

`npm run build` type-checks and then builds. The Docker image runs only `vite build`
(the type check needs `../acceptance`, which is outside its build context; CI's `web`
job does it).

`npm run dev` proxies `/api` to `http://127.0.0.1:3000` (the Go server's default
port); set `WHEELHOUSE_API=http://host:port` to point it elsewhere. Run the server
with `make run` in `../server`, configured with the usual service variables.

## Embedding in the Go binary

```sh
npm run build && npm run embed    # copies dist/ to ../server/internal/ui/dist
make -C ../server build           # the binary now serves this UI
```

`make -C ../server ui` does the first line. The embedded copy is never committed
(its `.gitignore` keeps only `.gitkeep`); with nothing there the binary serves a
placeholder page, so `go build` and `go test` work from a clean checkout.
`npm run embed -- --clean` empties it again.

## Security constraints

The server sends `default-src 'self'; script-src 'self'; style-src 'self'; font-src
'self'; connect-src 'self'` and `img-src 'self' data: https://image.tmdb.org
https://artworks.thetvdb.com` (posters from TMDB and, for Sonarr, TheTVDB). So:

- no inline `<script>`, `<style>` or `style=""` in `index.html`, no eval; React
  setting `element.style` through the DOM (progress bars, backdrop) is fine;
- fonts are self-hosted (`@fontsource/bricolage-grotesque`), nothing is inlined
  (`assetsInlineLimit: 0`), React Router's `ScrollRestoration` is not used (it
  renders an inline script);
- no frames: a movie's trailer is a link that opens YouTube in a new tab;
- library deletes go through `confirmedDeleteUrl` (`src/lib/api.ts`), which adds
  `?confirm=true`, and only after the UI's own confirm dialog (a test scans the
  source for any other DELETE).

## Tests

`npm test` runs the component and router tests (jsdom): every route, nav gating and
the "not configured" stubs, the 404 page, the delete flow, Explore add, Codecs
convert and hard-link confirmation, SWR polling intervals, and the Logo with fake
timers.

`npm run test:smoke` is the browser smoke test (`e2e/smoke.test.ts`). It needs the
embedded build in a Go binary and Chromium:

```sh
npm run build && npm run embed && make -C ../server build
npx playwright-core install chromium     # --with-deps on a bare Linux box
npm run test:smoke
```

It starts the binary against the acceptance suite's fake services, loads every page
in headless Chromium, and fails on a console error, a CSP violation, a failed or
erroring request, or a request to a host outside the CSP allow-list; it also checks
the logo, the self-hosted font and the confirm-then-delete flow. Without a binary
or a working Chromium it skips and says why; `WHEELHOUSE_SMOKE_REQUIRED=1` (set in
CI) turns that into a failure. `WHEELHOUSE_BIN` points it at another binary.
