import crypto from "node:crypto";
import http from "node:http";
import type { AddressInfo } from "node:net";
import auth from "./auth.json";
import torrentsV4 from "./torrents-v4.json";
import torrentsV5 from "./torrents-v5.json";
import transferV4 from "./transfer-v4.json";
import transferV5 from "./transfer-v5.json";
import versions from "./versions.json";

// Fixtures: sanitized responses captured from real qBittorrent v5.2.4 and
// v4.6.7 containers (official qbittorrentofficial/qbittorrent-nox images), with
// no network and no real content: a magnet link with a random infohash, and
// tiny torrents made from local files. The v5 torrent list is assembled from
// the real objects seen for each state (identities replaced). No real
// qBittorrent run produced "downloading", "uploading", "forcedDL",
// "forcedMetaDL", "error" or "missingFiles" (they need peers or a failing
// disk), so the adapter tests cover those by hand.

export type QbVersion = "v4" | "v5";
export type Scenario = "active" | "idle";

export const GOOD_USER = "admin";
export const GOOD_PASSWORD = "test-password-0123456789";

/** qBittorrent bans an IP after this many failed logins. */
const MAX_FAILURES = 5;

type Obj = Record<string, unknown>;

/**
 * A stand-in for qBittorrent's WebUI API. Behaves like the real thing: a login
 * sets a session cookie, anything without it is a 403, and repeated bad logins
 * ban the client (403 on login). v4 answers login with 200 "Ok."/"Fails.";
 * v5 with 204 and 401.
 */
export async function startFakeQbittorrent(version: QbVersion, scenario: Scenario = "active") {
  const sessions = new Set<string>();
  const requests: { method: string; path: string; cookie?: string }[] = [];
  const state = {
    logins: 0,
    /** Simulates a broken session handling: logins succeed but the cookie is never accepted. */
    refuseSessions: false,
    failures: 0,
    torrents: (scenario === "idle" ? [] : version === "v5" ? torrentsV5 : torrentsV4) as Obj[],
    transfer: (version === "v5" ? transferV5 : transferV4) as Obj,
  };
  const spec = auth[version];
  const cookieName = spec.login.ok.setCookie?.split("=")[0] ?? "SID";

  const server = http.createServer(async (req, res) => {
    const path = new URL(req.url ?? "/", "http://localhost").pathname;
    const cookieHeader = req.headers.cookie;
    requests.push({ method: req.method ?? "GET", path, cookie: cookieHeader });

    const send = (status: number, body: string, contentType = "text/plain; charset=UTF-8") => {
      res.statusCode = status;
      if (body) res.setHeader("content-type", contentType);
      res.end(body);
    };

    if (path === "/api/v2/auth/login" && req.method === "POST") {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(chunk as Buffer);
      const form = new URLSearchParams(Buffer.concat(chunks).toString());
      if (state.failures >= MAX_FAILURES) return send(auth.banned.status, auth.banned.body);
      if (form.get("username") === GOOD_USER && form.get("password") === GOOD_PASSWORD) {
        state.logins += 1;
        const sid = crypto.randomBytes(16).toString("base64url");
        sessions.add(sid);
        res.setHeader("set-cookie", `${cookieName}=${sid}; HttpOnly; path=/`);
        return send(spec.login.ok.status, spec.login.ok.body);
      }
      state.failures += 1;
      return send(spec.login.wrong.status, spec.login.wrong.body);
    }

    const sid = cookieHeader
      ?.split(";")
      .map((c) => c.trim().split("="))
      .find(([name]) => name === cookieName)?.[1];
    if (state.refuseSessions || !sid || !sessions.has(sid))
      return send(spec.noCookie.status, spec.noCookie.body);

    if (path === "/api/v2/app/version") return send(200, versions[version].version);
    if (path === "/api/v2/transfer/info") {
      return send(200, JSON.stringify(state.transfer), "application/json");
    }
    if (path === "/api/v2/torrents/info") {
      return send(200, JSON.stringify(state.torrents), "application/json");
    }
    return send(404, "Not Found");
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));

  return {
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    requests,
    /** The live data the server answers with; tests may replace or edit it. */
    state,
    /** Forget every session, as a qBittorrent restart (or its one-hour timeout) would. */
    expireSessions: () => sessions.clear(),
    close: () => {
      server.closeAllConnections();
      return new Promise<void>((resolve) => server.close(() => resolve()));
    },
  };
}
