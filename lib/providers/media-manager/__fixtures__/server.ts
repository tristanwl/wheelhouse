import http from "node:http";
import type { AddressInfo } from "node:net";
import radarrHealth from "./radarr/health.json";
import radarrIndexers from "./radarr/indexer.json";
import radarrLookupInLibrary from "./radarr/lookup-in-library.json";
import radarrLookupNew from "./radarr/lookup-new.json";
import radarrMovies from "./radarr/movies.json";
import radarrProfiles from "./radarr/qualityprofile.json";
import radarrQueue from "./radarr/queue.json";
import radarrRoots from "./radarr/rootfolder.json";
import radarrStatus from "./radarr/system-status.json";
import sonarrEpisodeFiles from "./sonarr/episodefile.json";
import sonarrEpisodes from "./sonarr/episode.json";
import sonarrHealth from "./sonarr/health.json";
import sonarrIndexers from "./sonarr/indexer.json";
import sonarrLookupNew from "./sonarr/lookup-new.json";
import sonarrProfiles from "./sonarr/qualityprofile.json";
import sonarrQueue from "./sonarr/queue.json";
import sonarrRoots from "./sonarr/rootfolder.json";
import sonarrSeries from "./sonarr/series.json";
import sonarrStatus from "./sonarr/system-status.json";

export const GOOD_KEY = "test-key-0123456789";

// The fixtures are sanitized real responses; the fake server serves (copies
// of) them, so tests treat them as plain objects.
type Json = Record<string, any>;
const copy = <T>(value: T): T => structuredClone(value);

export interface FakeArrRequest {
  method: string;
  pathname: string;
  query: URLSearchParams;
  body?: unknown;
}

export interface FakeArrOptions {
  /** The key the server accepts; anything else gets HTTP 401, as in the real apps. */
  apiKey: string;
  /** Delay every response, so concurrent calls overlap. */
  delayMs: number;
}

const DEFAULTS: FakeArrOptions = { apiKey: GOOD_KEY, delayMs: 0 };

/** Fixture movies moved under another root folder with their own ids, e.g. for a second (4K) instance. */
export function relocateMovies(
  movies: Json[],
  options: { root: string; idOffset: number; tmdbOffset: number }
): Json[] {
  return movies.map((m) => {
    const folder = (m.path as string).split("/").pop();
    const path = `${options.root}/${folder}`;
    const movieFile = m.movieFile && {
      ...m.movieFile,
      path: `${path}/${(m.movieFile.path as string).split("/").pop()}`,
    };
    return {
      ...copy(m),
      id: m.id + options.idOffset,
      tmdbId: m.tmdbId + options.tmdbOffset,
      rootFolderPath: options.root,
      path,
      ...(movieFile ? { movieFile } : {}),
    };
  });
}

/** The fixture library (a fresh copy each call). */
export const fixtureMovies = (): Json[] => copy(radarrMovies);

/** A second Radarr's library: the two movies with files, under /movies-4k with their own ids. */
export function fourKMovies(): Json[] {
  return relocateMovies(
    fixtureMovies()
      .filter((m) => m.hasFile)
      .slice(0, 2),
    {
      root: "/movies-4k",
      idOffset: 100,
      tmdbOffset: 100,
    }
  );
}

async function listen(handler: http.RequestListener) {
  const server = http.createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    server,
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

function readBody(req: http.IncomingMessage): Promise<unknown> {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (chunk) => (raw += chunk));
    req.on("end", () => {
      try {
        resolve(raw ? JSON.parse(raw) : undefined);
      } catch {
        resolve(raw);
      }
    });
  });
}

/**
 * Shared plumbing of the two fakes: auth (401 on a wrong key), request log and
 * a route function that returns the JSON body (or a status for errors).
 */
async function startFakeArr(
  options: Partial<FakeArrOptions>,
  route: (req: FakeArrRequest) => { status?: number; body?: unknown } | undefined
) {
  const { apiKey, delayMs } = { ...DEFAULTS, ...options };
  const requests: FakeArrRequest[] = [];
  const { url, close } = await listen(async (req, res) => {
    const parsed = new URL(req.url ?? "/", "http://localhost");
    const entry: FakeArrRequest = {
      method: req.method ?? "GET",
      pathname: parsed.pathname,
      query: parsed.searchParams,
      body: await readBody(req),
    };
    requests.push(entry);
    if (delayMs > 0) await new Promise((r) => setTimeout(r, delayMs));

    if (req.headers["x-api-key"] !== apiKey) {
      res.statusCode = 401;
      res.setHeader("content-type", "application/json");
      return res.end(JSON.stringify({ error: "Unauthorized" }));
    }
    const result = route(entry);
    if (!result) {
      res.statusCode = 404;
      return res.end();
    }
    res.statusCode = result.status ?? 200;
    if (result.body === undefined) return res.end();
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify(result.body));
  });

  return {
    url,
    requests,
    close,
    /** How many requests of this method and path (no query) reached the server. */
    count: (method: string, pathname: string) =>
      requests.filter((r) => r.method === method && r.pathname === pathname).length,
  };
}

export interface FakeRadarrState {
  movies: Json[];
  rootFolders: Json[];
}

/**
 * A stand-in for Radarr's v5 API replaying (sanitized) real responses. State
 * is mutable: adding or deleting a movie changes what the list returns.
 */
export async function startFakeRadarr(
  options: Partial<FakeArrOptions> & Partial<FakeRadarrState> = {}
) {
  const state: FakeRadarrState = {
    movies: options.movies ?? copy(radarrMovies),
    rootFolders: options.rootFolders ?? copy(radarrRoots),
  };
  let nextId = 1000;

  const server = await startFakeArr(options, ({ method, pathname, query, body }) => {
    const movieId = /^\/api\/v3\/movie\/(\d+)$/.exec(pathname)?.[1];
    if (method === "GET") {
      switch (pathname) {
        case "/api/v3/system/status":
          return { body: radarrStatus };
        case "/api/v3/health":
          return { body: radarrHealth };
        case "/api/v3/queue":
          return { body: radarrQueue };
        case "/api/v3/rootfolder":
          return { body: state.rootFolders };
        case "/api/v3/qualityprofile":
          return { body: radarrProfiles };
        case "/api/v3/indexer":
          return { body: radarrIndexers };
        case "/api/v3/movie":
          return { body: state.movies };
        case "/api/v3/movie/lookup": {
          // A term lookup returns the library's own record (with an id) for movies
          // that are in it. The tmdb-id lookup never does: real Radarr leaves id off.
          const inLibrary = state.movies.find((m) => m.tmdbId === radarrLookupInLibrary.tmdbId);
          return { body: [inLibrary ?? radarrLookupInLibrary, radarrLookupNew] };
        }
        case "/api/v3/movie/lookup/tmdb": {
          const tmdbId = Number(query.get("tmdbId"));
          const found = state.movies.find((m) => m.tmdbId === tmdbId);
          return { body: found ?? { ...radarrLookupNew, tmdbId } };
        }
      }
      if (movieId) {
        const movie = state.movies.find((m) => m.id === Number(movieId));
        return movie ? { body: movie } : { status: 404, body: { message: "NotFound" } };
      }
    }
    if (method === "POST" && pathname === "/api/v3/movie") {
      const created = { ...(body as Json), id: nextId++ };
      state.movies.push(created);
      return { status: 201, body: created };
    }
    if (method === "POST" && pathname === "/api/v3/command") {
      return { status: 201, body: { id: 1, name: (body as Json).name, status: "queued" } };
    }
    if (method === "DELETE" && movieId) {
      state.movies = state.movies.filter((m) => m.id !== Number(movieId));
      // Radarr answers a delete with 200 and an empty body, not 204.
      return {};
    }
    return undefined;
  });

  return { ...server, state };
}

export interface FakeSonarrState {
  series: Json[];
  rootFolders: Json[];
  episodeFiles: Json[];
  episodes: Json[];
}

/** Same idea as startFakeRadarr, for Sonarr's v4 API. */
export async function startFakeSonarr(
  options: Partial<FakeArrOptions> & Partial<FakeSonarrState> = {}
) {
  const state: FakeSonarrState = {
    series: options.series ?? copy(sonarrSeries),
    rootFolders: options.rootFolders ?? copy(sonarrRoots),
    episodeFiles: options.episodeFiles ?? copy(sonarrEpisodeFiles),
    episodes: options.episodes ?? copy(sonarrEpisodes),
  };
  let nextId = 1000;

  const server = await startFakeArr(options, ({ method, pathname, query, body }) => {
    const seriesId = /^\/api\/v3\/series\/(\d+)$/.exec(pathname)?.[1];
    const fileId = /^\/api\/v3\/episodefile\/(\d+)$/.exec(pathname)?.[1];
    if (method === "GET") {
      switch (pathname) {
        case "/api/v3/system/status":
          return { body: sonarrStatus };
        case "/api/v3/health":
          return { body: sonarrHealth };
        case "/api/v3/queue":
          return { body: sonarrQueue };
        case "/api/v3/rootfolder":
          return { body: state.rootFolders };
        case "/api/v3/qualityprofile":
          return { body: sonarrProfiles };
        case "/api/v3/indexer":
          return { body: sonarrIndexers };
        case "/api/v3/series":
          return { body: state.series };
        case "/api/v3/series/lookup": {
          const term = query.get("term") ?? "";
          const tvdb = /^tvdb:(\d+)$/.exec(term)?.[1];
          const known = state.series.find((s) => String(s.tvdbId) === tvdb);
          return {
            body: [known ?? { ...sonarrLookupNew, ...(tvdb ? { tvdbId: Number(tvdb) } : {}) }],
          };
        }
        case "/api/v3/episodefile":
          return {
            body: state.episodeFiles.filter((f) => f.seriesId === Number(query.get("seriesId"))),
          };
        case "/api/v3/episode":
          return {
            body: state.episodes.filter((e) => e.seriesId === Number(query.get("seriesId"))),
          };
      }
      if (seriesId) {
        const series = state.series.find((s) => s.id === Number(seriesId));
        return series ? { body: series } : { status: 404, body: { message: "NotFound" } };
      }
    }
    if (method === "POST" && pathname === "/api/v3/series") {
      const created = { ...(body as Json), id: nextId++ };
      state.series.push(created);
      return { status: 201, body: created };
    }
    if (method === "PUT" && seriesId) {
      state.series = state.series.map((s) => (s.id === Number(seriesId) ? (body as Json) : s));
      return { status: 202, body };
    }
    if (method === "POST" && pathname === "/api/v3/command") {
      return { status: 201, body: { id: 1, name: (body as Json).name, status: "queued" } };
    }
    if (method === "DELETE" && seriesId) {
      state.series = state.series.filter((s) => s.id !== Number(seriesId));
      return {};
    }
    if (method === "DELETE" && fileId) {
      state.episodeFiles = state.episodeFiles.filter((f) => f.id !== Number(fileId));
      return {};
    }
    return undefined;
  });

  return { ...server, state };
}

export type FakeRadarr = Awaited<ReturnType<typeof startFakeRadarr>>;
export type FakeSonarr = Awaited<ReturnType<typeof startFakeSonarr>>;
