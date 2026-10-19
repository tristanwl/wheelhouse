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

## Signing in

`src/lib/http.ts` is the only code that calls `fetch`: it sends the session cookie,
adds the CSRF token (kept in memory, from `GET /api/auth/session`) to every non-GET
request, retries once after a CSRF refusal, and reports 401 and 503 `setup required`
to `components/SessionProvider.tsx`. The provider holds the user, role and the
`bypass` flag (set by the server for the local-network bypass); `RequireAuth` and
`RequireAdmin` (`components/Guards.tsx`) send people to `/login`, `/setup`,
`/change-password` or the not-authorised page. The pages are `/login`, `/setup`
(wizard step 1), `/account` and `/change-password`. Nothing about the session is
written to browser storage.

### Administration pages (`/admin/*`)

Behind `RequireAdmin`, with a sub-navigation (`components/admin/AdminLayout.tsx`)
and one page per area in `src/pages/admin/`: Services (list, add, edit, test, delete,
import from the environment), Features, Users, Sessions, Network, Media and converter
and Audit log; the "Admin" link in the header shows for administrators only, and the
dashboard shows a first-run checklist banner while no service exists.

- **Service forms are generated from the provider's JSON Schema**
  (`GET /api/admin/providers`): `lib/admin.ts` (`fieldsOf`, `validateAll`,
  `buildConfig`) and `components/admin/SchemaForm.tsx`. A new provider needs no UI
  work. Secrets (`x-secret`) are masked inputs with Show/Hide, never pre-filled: a
  saved service shows "Set (hidden)" and a Replace button, and the request leaves the
  secret out unless it was replaced (the server then keeps the stored one).
- **The schemas the tests use are the real ones.** `src/fixtures/providers.json` is
  what the Go registry returns; `go test ./internal/providers/all` fails when it
  drifts. After changing a provider's settings run
  `UPDATE_PROVIDER_SNAPSHOT=1 go test ./internal/providers/all` (in `server/`) and
  commit the file.
- Every destructive action (delete a service or user, reset a password, end sessions,
  disable or demote an account) goes through `components/ConfirmModal.tsx` (dialog role,
  focus on Cancel, Escape cancels). A test checks that every `DELETE` in
  `src/pages/admin/` sits in a file that uses it.
- A password the server must never see twice (a user's reset or generated password) is
  made in the browser (`generatePassword`), shown once with a Copy button and kept only
  in component state. No admin code touches browser storage (a test scans for it).
- Media and converter reads and writes `GET/PUT /api/admin/converter`
  (`{mediaRoots, includeHdr, effectiveRoots}`); while the server answers 404 the page
  says the settings are not available instead of showing a form.

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

`npm test` runs the component and router tests (jsdom): the API client (CSRF, 401, 503), route guards,
login, setup, forced password change, account and bypass rendering (`src/auth.test.tsx`), every route, nav gating and
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
in headless Chromium (the pages start with the harness administrator's session cookie; the
login, first-run setup and forced password change are driven through the UI itself, using
the setup token from the config directory), and fails on a console error, a CSP violation, a failed or
erroring request, or a request to a host outside the CSP allow-list; it also checks
the logo, the self-hosted font and the confirm-then-delete flow. Without a binary
or a working Chromium it skips and says why; `WHEELHOUSE_SMOKE_REQUIRED=1` (set in
CI) turns that into a failure. `WHEELHOUSE_BIN` points it at another binary.
