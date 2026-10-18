import http from "node:http";
import type { AddressInfo } from "node:net";
import { GOOD_KEY, movie, movieDetail } from "./fake-tmdb";

export { GOOD_KEY };

export interface FakeTmdbServerOptions {
  /** Ids returned by every list endpoint. */
  movieIds?: number[];
  /** Hold every answer back this long (a hanging upstream when large). */
  delayMs?: number;
}

/**
 * The same data as installFakeTmdb, served over real HTTP, for black-box tests
 * (the app is pointed at it with TMDB_BASE_URL = `${url}/3`). A missing or
 * wrong key is a 401 with a JSON body, as at TMDB.
 */
export async function startFakeTmdbServer(options: FakeTmdbServerOptions = {}) {
  const { movieIds = [101, 102, 103], delayMs = 0 } = options;
  const requests: URL[] = [];
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    requests.push(url);
    if (delayMs > 0) await new Promise((r) => setTimeout(r, delayMs));
    const send = (status: number, body: unknown) => {
      res.statusCode = status;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(body));
    };
    if (url.searchParams.get("api_key") !== GOOD_KEY) {
      return send(401, { status_code: 7, status_message: "Invalid API key." });
    }
    const path = url.pathname.replace(/^\/3/, "");
    if (path === "/configuration") {
      return send(200, { images: { base_url: "http://image.example.com" } });
    }
    const detail = /^\/movie\/(\d+)$/.exec(path);
    if (detail) return send(200, movieDetail(Number(detail[1])));
    send(200, { results: movieIds.map(movie), total_pages: 1 });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    requests,
    close: () => {
      server.closeAllConnections();
      return new Promise<void>((resolve) => server.close(() => resolve()));
    },
  };
}
