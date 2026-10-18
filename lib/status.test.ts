import { describe, expect, it } from "vitest";
import {
  describeInstance,
  hasRecentError,
  isWarningVisible,
  serviceHref,
  serviceLink,
  visibleWarnings,
  type StatusInstance,
} from "./status";
import { formatRelativeAge } from "./format";

const NOW = Date.parse("2026-01-10T12:00:00Z");
const ago = (ms: number) => new Date(NOW - ms).toISOString();
const HOUR = 3_600_000;

describe("formatRelativeAge", () => {
  it("steps from seconds to days", () => {
    expect(formatRelativeAge(ago(10_000), NOW)).toBe("just now");
    expect(formatRelativeAge(ago(5 * 60_000), NOW)).toBe("5m ago");
    expect(formatRelativeAge(ago(HOUR), NOW)).toBe("1h ago");
    expect(formatRelativeAge(ago(23.9 * HOUR), NOW)).toBe("23h ago");
    expect(formatRelativeAge(ago(50 * HOUR), NOW)).toBe("2d ago");
  });
  it("treats a future or unparseable time sanely", () => {
    expect(formatRelativeAge(ago(-HOUR), NOW)).toBe("just now");
    expect(formatRelativeAge("nope", NOW)).toBe("-");
  });
});

describe("warning visibility", () => {
  it("hides warnings older than 24 hours, keeps newer ones", () => {
    expect(isWarningVisible({ at: ago(23 * HOUR) }, NOW)).toBe(true);
    expect(isWarningVisible({ at: ago(25 * HOUR) }, NOW)).toBe(false);
  });
  it("keeps warnings without a usable timestamp", () => {
    expect(isWarningVisible({}, NOW)).toBe(true);
    expect(isWarningVisible({ at: "garbage" }, NOW)).toBe(true);
  });
  it("filters a list, preserving order", () => {
    const list = [{ at: ago(30 * HOUR) }, { at: ago(HOUR) }, {}];
    expect(visibleWarnings(list, NOW)).toEqual([list[1], list[2]]);
  });
  it("counts only recent errors", () => {
    const w = (severity: "warning" | "error", at?: string) => ({ message: "m", severity, at });
    expect(hasRecentError([w("error", ago(HOUR))], NOW)).toBe(true);
    expect(hasRecentError([w("error", ago(30 * HOUR))], NOW)).toBe(false);
    expect(hasRecentError([w("warning", ago(HOUR))], NOW)).toBe(false);
    expect(hasRecentError([w("error")], NOW)).toBe(true);
  });
});

describe("serviceLink", () => {
  it("keeps only the port and path prefix", () => {
    expect(serviceLink("http://user:pass@sonarr.internal:8989/sonarr/?apikey=x#f")).toEqual({
      port: 8989,
      path: "/sonarr",
    });
  });
  it("omits the port when the URL uses the default one", () => {
    expect(serviceLink("https://media.example.com")).toEqual({ path: "/" });
    expect(serviceLink("http://media.example.com:80/")).toEqual({ path: "/" });
  });
  it("appends a web UI path", () => {
    expect(serviceLink("http://plex.example.com:32400/", "/web")).toEqual({
      port: 32400,
      path: "/web",
    });
  });
  it("is undefined for anything that is not an http(s) URL", () => {
    expect(serviceLink(undefined)).toBeUndefined();
    expect(serviceLink("not a url")).toBeUndefined();
    expect(serviceLink("ftp://example.com")).toBeUndefined();
  });
});

describe("serviceHref", () => {
  it("builds the address from the browser's host", () => {
    expect(serviceHref("192.0.2.5", { port: 7878, path: "/radarr" })).toBe(
      "http://192.0.2.5:7878/radarr"
    );
    expect(serviceHref("example.com", { path: "/" })).toBe("http://example.com/");
  });
});

describe("describeInstance", () => {
  it("turns arr health into warnings, dropping ok entries", () => {
    const instance: StatusInstance = {
      id: "radarr",
      providerId: "radarr",
      name: "Radarr",
      role: "mediaManager",
      state: "degraded",
      detail: {
        version: "5.0.0",
        queueCount: 2,
        indexers: { total: 3, enabled: 2, issues: [] },
        health: [
          { source: "a", type: "ok", message: "fine" },
          { source: "b", type: "warning", message: "careful" },
          { source: "c", type: "error", message: "broken" },
        ],
      },
    };
    expect(describeInstance(instance)).toEqual({
      lines: ["queue: 2", "indexers: 2/3", "v5.0.0"],
      warnings: [
        { message: "careful", severity: "warning" },
        { message: "broken", severity: "error" },
      ],
    });
  });

  it("describes a download client's speed, queue and servers", () => {
    const instance: StatusInstance = {
      id: "sabnzbd",
      providerId: "sabnzbd",
      name: "SABnzbd",
      role: "downloadClient",
      state: "ok",
      detail: {
        paused: false,
        statusText: "Downloading",
        speedBytesPerSec: 2048,
        queueCount: 4,
        etaSeconds: 3725,
        warnings: [],
        usenet: { servers: [{ name: "news", connections: 8, ssl: true, enabled: true }] },
      },
    };
    expect(describeInstance(instance).lines).toEqual([
      "status: Downloading",
      "2.0 KB/s",
      "4 in queue",
      "1:02:05 left",
      "server: news (8 conn, ssl)",
    ]);
  });

  it("describes a media server, with its notice as a warning", () => {
    const instance: StatusInstance = {
      id: "plex",
      providerId: "plex",
      name: "Plex",
      role: "mediaServer",
      state: "needs-setup",
      detail: { state: "needs-setup", authenticated: false, name: "Example", notice: "Claim it" },
    };
    expect(describeInstance(instance)).toEqual({
      lines: ["Example"],
      warnings: [{ message: "Claim it", severity: "warning" }],
    });
  });

  it("has nothing to say about a down instance", () => {
    expect(
      describeInstance({
        id: "x",
        providerId: "sonarr",
        name: "X",
        role: "mediaManager",
        state: "down",
      })
    ).toEqual({ lines: [], warnings: [] });
  });
});
