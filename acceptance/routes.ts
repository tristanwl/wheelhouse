/**
 * The route table: every HTTP route a Wheelhouse server must answer, in one
 * place. A server that passes this suite implements all of them.
 *
 * - `PAGES` are the HTML pages. A port may serve one SPA shell for all of them,
 *   so only the status and content type are required.
 * - `GET_ROUTES` lists each GET API route with concrete example requests
 *   (`cases`) and the status each must answer in the two reference situations:
 *   `none` (no service configured) and `full` (the full fake stack, all
 *   healthy). The secret sweep requests every case in every scenario, so a
 *   route that is not listed here is not checked for leaks.
 * - `MUTATING_ROUTES` lists the POST/PUT/DELETE routes. They are exercised in
 *   `mutations.test.ts`; the real-file side effects (converting, saving
 *   settings) are deliberately only tested through their refusal paths.
 *
 * To add a route, see "Adding a route" in acceptance/README.md.
 */

export interface PageRoute {
  path: string;
}

export const PAGES: PageRoute[] = [
  { path: "/" },
  { path: "/library" },
  { path: "/explore" },
  { path: "/codecs" },
  { path: "/explore/popular" },
];

export interface GetCase {
  name: string;
  /** Path and query, with example values filled in. */
  path: string;
  /** Required status with no service configured. */
  none: number;
  /** Required status with every fake service healthy. */
  full: number;
}

export interface GetRoute {
  /** Route pattern; `{x}` is a path parameter. Must match the real route list. */
  pattern: string;
  cases: GetCase[];
}

export const GET_ROUTES: GetRoute[] = [
  {
    pattern: "/api/features",
    cases: [{ name: "features", path: "/api/features", none: 200, full: 200 }],
  },
  {
    pattern: "/api/status",
    cases: [{ name: "status", path: "/api/status", none: 200, full: 200 }],
  },
  {
    pattern: "/api/downloads",
    cases: [{ name: "downloads", path: "/api/downloads", none: 200, full: 200 }],
  },
  {
    pattern: "/api/storage",
    cases: [{ name: "storage", path: "/api/storage", none: 200, full: 200 }],
  },
  {
    pattern: "/api/library",
    cases: [{ name: "library", path: "/api/library", none: 200, full: 200 }],
  },
  {
    pattern: "/api/library/{instanceId}/{id}/details",
    cases: [
      { name: "details radarr", path: "/api/library/radarr/1/details", none: 404, full: 200 },
      // The second Radarr has its own ids (see the 4K fixture: +100).
      {
        name: "details radarr-4k",
        path: "/api/library/radarr-4k/101/details",
        none: 404,
        full: 200,
      },
      { name: "details sonarr", path: "/api/library/sonarr/1/details", none: 404, full: 200 },
      { name: "details bad id", path: "/api/library/radarr/x/details", none: 400, full: 400 },
    ],
  },
  {
    pattern: "/api/library/{instanceId}/{id}/seasons",
    cases: [
      { name: "seasons sonarr", path: "/api/library/sonarr/1/seasons", none: 404, full: 200 },
      // Seasons only exist for series managers.
      { name: "seasons radarr", path: "/api/library/radarr/1/seasons", none: 404, full: 400 },
    ],
  },
  {
    pattern: "/api/search",
    cases: [
      { name: "search movies", path: "/api/search?kind=movies&term=example", none: 404, full: 200 },
      { name: "search series", path: "/api/search?kind=series&term=example", none: 404, full: 200 },
      // An empty term is an empty answer, even with nothing configured.
      { name: "search empty term", path: "/api/search?kind=movies", none: 200, full: 200 },
      { name: "search bad kind", path: "/api/search?kind=nope&term=example", none: 400, full: 400 },
    ],
  },
  {
    pattern: "/api/add-meta",
    cases: [
      { name: "add-meta movies", path: "/api/add-meta?kind=movies", none: 404, full: 200 },
      { name: "add-meta series", path: "/api/add-meta?kind=series", none: 404, full: 200 },
      {
        name: "add-meta instance",
        path: "/api/add-meta?instanceId=radarr-4k",
        none: 404,
        full: 200,
      },
      { name: "add-meta no params", path: "/api/add-meta", none: 400, full: 400 },
    ],
  },
  {
    pattern: "/api/explore",
    cases: [
      {
        name: "explore theaters",
        path: "/api/explore?category=theaters&limit=20&page=1",
        none: 502,
        full: 200,
      },
      { name: "explore popular", path: "/api/explore?category=popular", none: 502, full: 200 },
      { name: "explore bluray", path: "/api/explore?category=bluray", none: 502, full: 200 },
      { name: "explore bad category", path: "/api/explore?category=nope", none: 400, full: 400 },
    ],
  },
  {
    pattern: "/api/explore/{id}",
    cases: [
      { name: "explore detail", path: "/api/explore/101", none: 502, full: 200 },
      { name: "explore detail bad id", path: "/api/explore/abc", none: 400, full: 400 },
    ],
  },
  {
    pattern: "/api/codec-analysis",
    cases: [
      {
        name: "codec-analysis movies",
        path: "/api/codec-analysis?kind=movies",
        none: 404,
        full: 200,
      },
      {
        name: "codec-analysis series",
        path: "/api/codec-analysis?kind=series",
        none: 404,
        full: 200,
      },
    ],
  },
  {
    pattern: "/api/codec-analysis/settings",
    cases: [{ name: "codec settings", path: "/api/codec-analysis/settings", none: 200, full: 200 }],
  },
  {
    pattern: "/api/codec-analysis/encoders",
    cases: [{ name: "encoders", path: "/api/codec-analysis/encoders", none: 200, full: 200 }],
  },
  {
    pattern: "/api/codec-analysis/convert",
    cases: [{ name: "convert jobs", path: "/api/codec-analysis/convert", none: 200, full: 200 }],
  },
  {
    pattern: "/api/codec-analysis/convert/{jobId}",
    cases: [
      {
        name: "convert job unknown",
        path: "/api/codec-analysis/convert/nope",
        none: 404,
        full: 404,
      },
    ],
  },
];

export interface MutatingRoute {
  method: "POST" | "PUT" | "DELETE";
  pattern: string;
}

export const MUTATING_ROUTES: MutatingRoute[] = [
  { method: "POST", pattern: "/api/add" },
  { method: "DELETE", pattern: "/api/library/{instanceId}/{id}" },
  { method: "DELETE", pattern: "/api/library/{instanceId}/{id}/seasons/{season}" },
  { method: "PUT", pattern: "/api/codec-analysis/settings" },
  { method: "POST", pattern: "/api/codec-analysis/convert" },
  { method: "POST", pattern: "/api/codec-analysis/convert/{jobId}/cancel" },
];

export type Level = "public" | "authenticated" | "admin";

/**
 * Every route the server registers with its access level, as the server's own
 * router test lists them (a Go test parses this table, so the two cannot
 * drift). The enumerating tests in auth-levels.test.ts walk it:
 *   - an unauthenticated request to a non-public route is 401,
 *   - a `user` gets 403 on every admin route,
 *   - a mutating route refuses a request without the CSRF token and one from a
 *     foreign origin.
 * Keep one entry per line, in this exact shape.
 */
export const ROUTE_LEVELS: { route: string; level: Level }[] = [
  { route: "GET /healthz", level: "public" },
  { route: "GET /api/auth/setup", level: "public" },
  { route: "POST /api/auth/setup", level: "public" },
  { route: "POST /api/auth/login", level: "public" },
  { route: "/api", level: "public" },
  { route: "/api/", level: "public" },
  { route: "/", level: "public" },
  { route: "GET /api/features", level: "authenticated" },
  { route: "GET /api/status", level: "authenticated" },
  { route: "GET /api/downloads", level: "authenticated" },
  { route: "GET /api/library", level: "authenticated" },
  { route: "GET /api/storage", level: "authenticated" },
  { route: "GET /api/search", level: "authenticated" },
  { route: "GET /api/add-meta", level: "authenticated" },
  { route: "POST /api/add", level: "authenticated" },
  { route: "GET /api/library/{instanceId}/{id}/details", level: "authenticated" },
  { route: "GET /api/library/{instanceId}/{id}/seasons", level: "authenticated" },
  { route: "DELETE /api/library/{instanceId}/{id}", level: "authenticated" },
  { route: "DELETE /api/library/{instanceId}/{id}/seasons/{season}", level: "authenticated" },
  { route: "GET /api/explore", level: "authenticated" },
  { route: "GET /api/explore/{id}", level: "authenticated" },
  { route: "GET /api/codec-analysis", level: "authenticated" },
  { route: "GET /api/codec-analysis/settings", level: "authenticated" },
  { route: "PUT /api/codec-analysis/settings", level: "admin" },
  { route: "GET /api/codec-analysis/encoders", level: "authenticated" },
  { route: "GET /api/codec-analysis/convert", level: "authenticated" },
  { route: "POST /api/codec-analysis/convert", level: "authenticated" },
  { route: "GET /api/codec-analysis/convert/{jobId}", level: "authenticated" },
  { route: "POST /api/codec-analysis/convert/{jobId}/cancel", level: "authenticated" },
  { route: "POST /api/auth/logout", level: "authenticated" },
  { route: "GET /api/auth/session", level: "authenticated" },
  { route: "POST /api/auth/password", level: "authenticated" },
  { route: "GET /api/auth/sessions", level: "authenticated" },
  { route: "DELETE /api/auth/sessions/{id}", level: "authenticated" },
  { route: "GET /api/users", level: "admin" },
  { route: "POST /api/users", level: "admin" },
  { route: "PATCH /api/users/{id}", level: "admin" },
  { route: "POST /api/users/{id}/password", level: "admin" },
  { route: "DELETE /api/users/{id}", level: "admin" },
  { route: "GET /api/users/{id}/sessions", level: "admin" },
  { route: "DELETE /api/users/{id}/sessions", level: "admin" },
  { route: "GET /api/audit", level: "admin" },
  { route: "GET /api/admin/providers", level: "admin" },
  { route: "GET /api/admin/instances", level: "admin" },
  { route: "POST /api/admin/instances", level: "admin" },
  { route: "POST /api/admin/instances/test", level: "admin" },
  { route: "PUT /api/admin/instances/{id}", level: "admin" },
  { route: "DELETE /api/admin/instances/{id}", level: "admin" },
  { route: "POST /api/admin/instances/{id}/import", level: "admin" },
  { route: "GET /api/admin/features", level: "admin" },
  { route: "PUT /api/admin/features", level: "admin" },
];

/** A concrete request for a route pattern; the values only need to be well formed. */
export function sampleRequest(route: string): { method: string; path: string } {
  const [method, path] = route.includes(" ") ? route.split(" ") : ["GET", route];
  return {
    method,
    path: path
      .replace("{instanceId}", "radarr")
      .replace("{jobId}", "nope")
      .replace("{season}", "1")
      .replace(/\{id\}/g, "0".repeat(32)),
  };
}

export const ALL_GET_CASES: GetCase[] = GET_ROUTES.flatMap((r) => r.cases);
