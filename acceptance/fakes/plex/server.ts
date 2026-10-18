import http from "node:http";
import { readFileSync } from "node:fs";
import type { AddressInfo } from "node:net";
import identityClaimed from "./identity-claimed.json";
import identityUnclaimed from "./identity-unclaimed.json";
import root from "./root.json";
import sessionsActive from "./sessions-active.json";
import sessionsIdle from "./sessions-idle.json";

export const GOOD_TOKEN = "plex-token-0123456789";

const unauthorizedHtml = readFileSync(new URL("./unauthorized.html", import.meta.url), "utf8");

export interface PlexScenario {
  claimed: boolean;
  sessions: "idle" | "active";
  /** Make the token-protected endpoints fail with this status instead of answering. */
  authedFailure?: number;
}

/** A stand-in for Plex that behaves like the real one: tokens are checked, bad ones get a 401 HTML page. */
export async function startFakePlex(scenario: PlexScenario) {
  const requests: { path: string; token: string | undefined }[] = [];
  const server = http.createServer((req, res) => {
    const path = new URL(req.url ?? "/", "http://localhost").pathname;
    const token = req.headers["x-plex-token"] as string | undefined;
    requests.push({ path, token });

    const json = (body: unknown) => {
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(body));
    };

    if (path === "/identity") {
      return json(scenario.claimed ? identityClaimed : identityUnclaimed);
    }
    if (scenario.authedFailure) {
      res.statusCode = scenario.authedFailure;
      return res.end();
    }
    if (token !== GOOD_TOKEN) {
      res.statusCode = 401;
      res.setHeader("content-type", "text/html");
      return res.end(unauthorizedHtml);
    }
    if (path === "/") return json(root);
    if (path === "/status/sessions") {
      return json(scenario.sessions === "active" ? sessionsActive : sessionsIdle);
    }
    res.statusCode = 404;
    res.end();
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    requests,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}
