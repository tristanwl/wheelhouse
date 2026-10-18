import http from "node:http";
import type { AddressInfo } from "node:net";
import history from "./history.json";
import historyIdle from "./history-idle.json";
import queueActive from "./queue-active.json";
import queueIdle from "./queue-idle.json";
import queuePaused from "./queue-paused.json";
import servers from "./servers.json";
import version from "./version.json";
import warnings from "./warnings.json";

export type Scenario = "idle" | "active" | "paused";

export const GOOD_KEY = "test-key-0123456789";

/**
 * A stand-in for SABnzbd's /api endpoint, replaying (sanitized) real responses.
 * Behaves like SABnzbd 4.5: a wrong key is a 403 with a plain-text body.
 */
export async function startFakeSabnzbd(scenario: Scenario) {
  const requests: URL[] = [];
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    requests.push(url);
    if (url.pathname !== "/api") {
      res.statusCode = 404;
      return res.end();
    }
    if (url.searchParams.get("apikey") !== GOOD_KEY) {
      res.statusCode = 403;
      return res.end("API Key Incorrect");
    }
    const mode = url.searchParams.get("mode");
    let body: unknown;
    if (mode === "queue") {
      body = { idle: queueIdle, active: queueActive, paused: queuePaused }[scenario];
    } else if (mode === "history") {
      body = scenario === "idle" ? historyIdle : history;
    } else if (mode === "get_config") {
      body = servers;
    } else if (mode === "warnings") {
      body = warnings;
    } else if (mode === "version") {
      body = version;
    } else {
      body = { status: false, error: `not implemented: ${mode}` };
    }
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify(body));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    requests,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}
