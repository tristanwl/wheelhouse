import http from "node:http";
import type { AddressInfo } from "node:net";
import infoPublicSetup from "./info-public-setup.json";
import infoPublic from "./info-public.json";
import info from "./info.json";
import sessionsIdle from "./sessions-idle.json";
import sessionsPlaying from "./sessions-playing.json";

// Captured from a real Jellyfin 12.2.0 (official Docker image), then sanitized.
// info-public-setup.json is /System/Info/Public before the setup wizard ran.
// sessions-idle.json is one connected client that isn't playing anything;
// sessions-playing.json is one client playing a film. A wrong or missing key
// gets HTTP 401 with an empty body; /System/Info/Public ignores keys.
export const GOOD_KEY = "jellyfin-key-0123456789";

export interface JellyfinScenario {
  /** Whether the first-run setup wizard has been completed. */
  setupCompleted: boolean;
  /** none: no sessions; idle: one connected client; playing: one playing; mixed: one of each. */
  sessions: "none" | "idle" | "playing" | "mixed";
  /** Make the key-protected endpoints fail with this status instead of answering. */
  authedFailure?: number;
}

/** Pulls the key out of `Authorization: MediaBrowser Client="...", Token="..."`. */
function keyFrom(header: string | undefined): string | undefined {
  return header && /^MediaBrowser\s/.test(header) ? /Token="([^"]*)"/.exec(header)?.[1] : undefined;
}

/**
 * A stand-in for Jellyfin that behaves like the real one: keys are checked
 * (only in the Authorization header, as in 12.x) and bad ones get an empty 401.
 */
export async function startFakeJellyfin(scenario: JellyfinScenario) {
  const requests: { path: string; key: string | undefined; embyToken: string | undefined }[] = [];
  const server = http.createServer((req, res) => {
    const path = new URL(req.url ?? "/", "http://localhost").pathname;
    const key = keyFrom(req.headers.authorization);
    requests.push({ path, key, embyToken: req.headers["x-emby-token"] as string | undefined });

    const json = (body: unknown) => {
      res.setHeader("content-type", "application/json; charset=utf-8");
      res.end(JSON.stringify(body));
    };

    if (path === "/System/Info/Public") {
      return json(scenario.setupCompleted ? infoPublic : infoPublicSetup);
    }
    if (scenario.authedFailure) {
      res.statusCode = scenario.authedFailure;
      return res.end();
    }
    if (key !== GOOD_KEY) {
      res.statusCode = 401;
      return res.end();
    }
    if (path === "/System/Info") return json(info);
    if (path === "/Sessions") {
      const list = {
        none: [],
        idle: sessionsIdle,
        playing: sessionsPlaying,
        mixed: [...sessionsIdle, ...sessionsPlaying],
      }[scenario.sessions];
      return json(list);
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
