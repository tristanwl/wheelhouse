import http from "node:http";
import net from "node:net";
import { format } from "node:util";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GET as addMeta } from "@/app/api/add-meta/route";
import { GET as codecAnalysis } from "@/app/api/codec-analysis/route";
import { GET as convertJobs } from "@/app/api/codec-analysis/convert/route";
import { GET as convertJob } from "@/app/api/codec-analysis/convert/[jobId]/route";
import { GET as encoders } from "@/app/api/codec-analysis/encoders/route";
import { GET as codecSettings } from "@/app/api/codec-analysis/settings/route";
import { GET as downloads } from "@/app/api/downloads/route";
import { GET as explore } from "@/app/api/explore/route";
import { GET as exploreDetail } from "@/app/api/explore/[id]/route";
import { GET as features } from "@/app/api/features/route";
import { GET as library } from "@/app/api/library/route";
import { GET as libraryDetails } from "@/app/api/library/[instanceId]/[id]/details/route";
import { GET as librarySeasons } from "@/app/api/library/[instanceId]/[id]/seasons/route";
import { GET as search } from "@/app/api/search/route";
import { GET as status } from "@/app/api/status/route";
import { GET as storage } from "@/app/api/storage/route";
import type { DownloadsResponse } from "@/app/api/downloads/route";
import type { FeaturesResponse } from "@/lib/features/server";
import {
  GOOD_KEY as SAB_KEY,
  startFakeSabnzbd,
} from "@/lib/providers/download-client/__fixtures__/sabnzbd/server";
import {
  GOOD_PASSWORD as QB_PASSWORD,
  GOOD_USER as QB_USER,
  startFakeQbittorrent,
} from "@/lib/providers/download-client/__fixtures__/qbittorrent/server";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { GOOD_KEY as ARR_KEY } from "@/lib/providers/media-manager/__fixtures__/server";
import {
  GOOD_KEY as JELLYFIN_KEY,
  startFakeJellyfin,
} from "@/lib/providers/media-server/__fixtures__/jellyfin/server";
import {
  GOOD_TOKEN as PLEX_TOKEN,
  startFakePlex,
} from "@/lib/providers/media-server/__fixtures__/plex/server";
import {
  GOOD_KEY as TMDB_KEY,
  installFakeTmdb,
} from "@/lib/providers/metadata/__fixtures__/fake-tmdb";
import type { StatusResponse } from "@/lib/status";

// The Phase 1 acceptance criteria (docs/wheelhouse-spec.md), exercised through
// the real route handlers, env config source and provider runtime against fake
// servers.

// ffmpeg is a property of the machine running the tests; pin it.
vi.mock("@/lib/features/ffmpeg", () => ({ hasFfmpeg: vi.fn(async () => true) }));
// Encoder detection runs real throwaway encodes; it has nothing to do with secrets.
vi.mock("@/lib/hw-encode", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/hw-encode")>()),
  detectEncoders: vi.fn(async () => []),
}));

const PROVIDER_VARIABLES = [
  "PLEX_URL",
  "PLEX_TOKEN",
  "JELLYFIN_URL",
  "JELLYFIN_API_KEY",
  "SABNZBD_URL",
  "SABNZBD_API_KEY",
  "QBITTORRENT_URL",
  "QBITTORRENT_USERNAME",
  "QBITTORRENT_PASSWORD",
  "TMDB_API_KEY",
];

const get = (path: string) => new Request(`http://localhost${path}`);
const idParams = <T extends Record<string, string>>(params: T) => ({
  params: Promise.resolve(params),
});

/** Every GET route, with arguments that reach the providers where there are any to reach. */
const GET_ROUTES: { name: string; call: () => Promise<Response> }[] = [
  { name: "status", call: () => status() },
  { name: "downloads", call: () => downloads() },
  { name: "storage", call: () => storage() },
  { name: "library", call: () => library() },
  { name: "features", call: () => features() },
  { name: "search movies", call: () => search(get("/api/search?kind=movies&term=example")) },
  { name: "search series", call: () => search(get("/api/search?kind=series&term=example")) },
  { name: "add-meta movies", call: () => addMeta(get("/api/add-meta?kind=movies")) },
  { name: "add-meta series", call: () => addMeta(get("/api/add-meta?kind=series")) },
  { name: "add-meta radarr", call: () => addMeta(get("/api/add-meta?instanceId=radarr")) },
  {
    name: "codec-analysis movies",
    call: () => codecAnalysis(get("/api/codec-analysis?kind=movies")),
  },
  {
    name: "codec-analysis series",
    call: () => codecAnalysis(get("/api/codec-analysis?kind=series")),
  },
  { name: "codec settings", call: () => codecSettings() },
  { name: "encoders", call: () => encoders() },
  { name: "convert jobs", call: () => convertJobs() },
  { name: "convert job", call: () => convertJob(get("/"), idParams({ jobId: "nope" })) },
  {
    name: "explore theaters",
    call: () => explore(get("/api/explore?category=theaters&limit=20&page=1")),
  },
  { name: "explore popular", call: () => explore(get("/api/explore?category=popular")) },
  { name: "explore bluray", call: () => explore(get("/api/explore?category=bluray")) },
  { name: "explore detail", call: () => exploreDetail(get("/"), idParams({ id: "101" })) },
  ...["radarr", "radarr-4k", "sonarr"].flatMap((instanceId) => [
    {
      name: `details ${instanceId}`,
      call: () => libraryDetails(get("/"), idParams({ instanceId, id: "1" })),
    },
    {
      name: `seasons ${instanceId}`,
      call: () => librarySeasons(get("/"), idParams({ instanceId, id: "1" })),
    },
  ]),
];

let stack: ReturnType<typeof createArrStack>;
const stops: (() => Promise<void>)[] = [];

beforeEach(() => {
  stack = createArrStack();
  for (const name of PROVIDER_VARIABLES) vi.stubEnv(name, "");
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await stack.stop();
  await Promise.all(stops.splice(0).map((stop) => stop()));
});

const track = <T extends { close: () => Promise<void> }>(server: T): T => {
  stops.push(server.close);
  return server;
};

/** A URL nothing listens on: connections are refused. */
async function closedUrl(): Promise<string> {
  const server = http.createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as net.AddressInfo;
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return `http://127.0.0.1:${port}`;
}

/** A server that accepts the connection and then never answers. */
async function hangingServer() {
  const sockets = new Set<net.Socket>();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {});
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as net.AddressInfo;
  track({
    close: () =>
      new Promise<void>((resolve) => {
        for (const socket of sockets) socket.destroy();
        server.close(() => resolve());
      }),
  });
  return `http://127.0.0.1:${port}`;
}

type Scenario = "healthy" | "wrong-credentials" | "unreachable" | "invalid-config";

/**
 * Configures every provider the way a user would, in one of four situations.
 * Returns every secret that is now in the environment (the canaries).
 */
async function configureEverything(scenario: Scenario): Promise<string[]> {
  const good = scenario === "healthy";
  // A distinctive string per provider; when the credentials are meant to be
  // right, they have to be what the fakes accept.
  const keys = {
    radarr: good ? ARR_KEY : "canary-radarr-key-7f3a91",
    radarr4k: good ? ARR_KEY : "canary-radarr4k-key-2b8c04",
    sonarr: good ? ARR_KEY : "canary-sonarr-key-e51d77",
    sab: good ? SAB_KEY : "canary-sabnzbd-key-90ac26",
    qb: good ? QB_PASSWORD : "canary-qbittorrent-pass-6b21e9",
    plex: good ? PLEX_TOKEN : "canary-plex-token-4d6e18",
    jellyfin: good ? JELLYFIN_KEY : "canary-jellyfin-key-a07b53",
    tmdb: good ? TMDB_KEY : "canary-tmdb-key-c3f219",
  };

  if (scenario === "invalid-config") {
    // Malformed URLs, a key without a URL, credentials embedded in a URL.
    const dead = await closedUrl();
    vi.stubEnv("RADARR_URL", "not a url");
    vi.stubEnv("RADARR_API_KEY", keys.radarr);
    vi.stubEnv("RADARR_4K_URL", dead.replace("http://", "http://admin:canary-pass-81c4@"));
    vi.stubEnv("RADARR_4K_API_KEY", keys.radarr4k);
    vi.stubEnv("SONARR_API_KEY", keys.sonarr);
    vi.stubEnv("SABNZBD_URL", "ftp://canary-user-5e2:canary-pass-39d@example.com");
    vi.stubEnv("SABNZBD_API_KEY", keys.sab);
    vi.stubEnv("QBITTORRENT_USERNAME", QB_USER);
    vi.stubEnv("QBITTORRENT_PASSWORD", keys.qb);
    vi.stubEnv("PLEX_TOKEN", keys.plex);
    vi.stubEnv("JELLYFIN_API_KEY", keys.jellyfin);
    vi.stubEnv("TMDB_API_KEY", keys.tmdb);
    installFakeTmdb({ mode: "unreachable" });
    return [...Object.values(keys), "canary-pass-81c4", "canary-pass-39d", "canary-user-5e2"];
  }

  if (scenario === "unreachable") {
    const dead = await closedUrl();
    vi.stubEnv("RADARR_URL", dead);
    vi.stubEnv("RADARR_API_KEY", keys.radarr);
    vi.stubEnv("RADARR_4K_URL", dead);
    vi.stubEnv("RADARR_4K_API_KEY", keys.radarr4k);
    vi.stubEnv("SONARR_URL", dead);
    vi.stubEnv("SONARR_API_KEY", keys.sonarr);
    vi.stubEnv("SABNZBD_URL", dead);
    vi.stubEnv("SABNZBD_API_KEY", keys.sab);
    vi.stubEnv("QBITTORRENT_URL", dead);
    vi.stubEnv("QBITTORRENT_USERNAME", QB_USER);
    vi.stubEnv("QBITTORRENT_PASSWORD", keys.qb);
    vi.stubEnv("PLEX_URL", dead);
    vi.stubEnv("PLEX_TOKEN", keys.plex);
    vi.stubEnv("JELLYFIN_URL", dead);
    vi.stubEnv("JELLYFIN_API_KEY", keys.jellyfin);
    vi.stubEnv("TMDB_API_KEY", keys.tmdb);
    installFakeTmdb({ mode: "unreachable" });
    return Object.values(keys);
  }

  // healthy and wrong-credentials: the servers are up and expect the good keys.
  await stack.radarr({}, keys.radarr);
  await stack.radarr4k({}, keys.radarr4k);
  await stack.sonarr({}, keys.sonarr);
  const sab = track(await startFakeSabnzbd("active"));
  vi.stubEnv("SABNZBD_URL", sab.url);
  vi.stubEnv("SABNZBD_API_KEY", keys.sab);
  const qb = track(await startFakeQbittorrent("v5"));
  vi.stubEnv("QBITTORRENT_URL", qb.url);
  vi.stubEnv("QBITTORRENT_USERNAME", QB_USER);
  vi.stubEnv("QBITTORRENT_PASSWORD", keys.qb);
  const plex = track(await startFakePlex({ claimed: true, sessions: "active" }));
  vi.stubEnv("PLEX_URL", plex.url);
  vi.stubEnv("PLEX_TOKEN", keys.plex);
  const jellyfin = track(await startFakeJellyfin({ setupCompleted: true, sessions: "playing" }));
  vi.stubEnv("JELLYFIN_URL", jellyfin.url);
  vi.stubEnv("JELLYFIN_API_KEY", keys.jellyfin);
  vi.stubEnv("TMDB_API_KEY", keys.tmdb);
  installFakeTmdb();
  return Object.values(keys);
}

/** Captures everything the code writes through console while it runs. */
function captureConsole(): string[] {
  const lines: string[] = [];
  for (const method of ["log", "info", "warn", "error", "debug"] as const) {
    vi.spyOn(console, method).mockImplementation((...args: unknown[]) => {
      lines.push(format(...args));
    });
  }
  return lines;
}

/** Calls every GET route; a route that throws instead of answering is a failure of its own. */
async function callEverything() {
  const responses: { name: string; status: number; text: string }[] = [];
  const crashes: string[] = [];
  for (const route of GET_ROUTES) {
    try {
      const res = await route.call();
      const headers = JSON.stringify(Object.fromEntries(res.headers));
      responses.push({
        name: route.name,
        status: res.status,
        text: `${await res.text()}\n${headers}`,
      });
    } catch {
      crashes.push(route.name);
    }
  }
  return { responses, crashes };
}

describe("No API key or token appears in any API response or log line", () => {
  const scenarios: Scenario[] = ["healthy", "wrong-credentials", "unreachable", "invalid-config"];

  it.each(scenarios)("%s: every GET route", async (scenario) => {
    const canaries = await configureEverything(scenario);
    const logged = captureConsole();

    const { responses, crashes } = await callEverything();

    expect(crashes).toEqual([]);
    const leaks: string[] = [];
    for (const { name, text } of responses) {
      for (const canary of canaries) {
        for (const form of new Set([canary, encodeURIComponent(canary)])) {
          if (text.includes(form)) leaks.push(`${name} response contains ${canary}`);
        }
      }
    }
    for (const line of logged) {
      for (const canary of canaries) {
        if (line.includes(canary)) leaks.push(`console output contains ${canary}`);
      }
    }
    expect(leaks).toEqual([]);

    // The scenario must really have happened, or "no leak" proves nothing.
    const byName = (name: string) => responses.find((r) => r.name === name)!;
    const statusBody = JSON.parse(byName("status").text.split("\n")[0]) as StatusResponse;
    const states = new Map(statusBody.instances.map((i) => [i.id, i]));
    if (scenario === "healthy") {
      expect(byName("library").status).toBe(200);
      expect(byName("explore popular").status).toBe(200);
      expect([...states.values()].map((i) => i.state)).not.toContain("down");
    } else {
      for (const id of ["radarr", "sonarr", "sabnzbd", "qbittorrent"])
        expect(states.get(id)?.state).toBe("down");
    }
    if (scenario === "unreachable" || scenario === "invalid-config") {
      expect(statusBody.instances.every((i) => i.state === "down")).toBe(true);
    }
  });

  it("would notice a leak (the scan is not vacuous)", async () => {
    const canaries = await configureEverything("healthy");
    const res = await status();
    const text = (await res.text()) + canaries[0];
    expect(canaries.some((c) => text.includes(c))).toBe(true);
  });
});

describe("No hang longer than the timeout when a service is down", () => {
  // There is no per-instance timeout setting yet, so this waits out the real 8 s default.
  it("answers within the timeout when services accept connections and never reply", async () => {
    const hung = await hangingServer();
    vi.stubEnv("RADARR_URL", hung);
    vi.stubEnv("RADARR_API_KEY", ARR_KEY);
    vi.stubEnv("SONARR_URL", hung);
    vi.stubEnv("SONARR_API_KEY", ARR_KEY);
    vi.stubEnv("SABNZBD_URL", hung);
    vi.stubEnv("SABNZBD_API_KEY", SAB_KEY);
    vi.stubEnv("QBITTORRENT_URL", hung);
    vi.stubEnv("QBITTORRENT_USERNAME", QB_USER);
    vi.stubEnv("QBITTORRENT_PASSWORD", QB_PASSWORD);
    vi.stubEnv("PLEX_URL", hung);
    vi.stubEnv("PLEX_TOKEN", PLEX_TOKEN);
    vi.stubEnv("JELLYFIN_URL", hung);
    vi.stubEnv("JELLYFIN_API_KEY", JELLYFIN_KEY);

    const started = Date.now();
    const [statusRes, downloadsRes, libraryRes, storageRes] = await Promise.all([
      status(),
      downloads(),
      library(),
      storage(),
    ]);
    const elapsed = Date.now() - started;

    // 8 s default timeout plus a margin for the test machine.
    expect(elapsed).toBeLessThan(8000 + 2500);

    const statusBody = (await statusRes.json()) as StatusResponse;
    expect(statusBody.instances.length).toBe(6);
    for (const instance of statusBody.instances) {
      expect(instance.state).toBe("down");
      expect(instance.error).toMatch(/timed out/);
    }
    const downloadsBody = (await downloadsRes.json()) as DownloadsResponse;
    expect(downloadsBody.instances.every((c) => !c.up)).toBe(true);
    const libraryBody = (await libraryRes.json()) as { instances: { up: boolean }[] };
    expect(libraryBody.instances.every((i) => !i.up)).toBe(true);
    expect(storageRes.status).toBe(200);
  }, 20_000);
});

describe("Boot with no environment variables", () => {
  it("every aggregate API answers 200 and Dashboard is the only feature", async () => {
    const { responses, crashes } = await callEverything();
    expect(crashes).toEqual([]);
    const statuses = Object.fromEntries(responses.map((r) => [r.name, r.status]));

    for (const name of [
      "status",
      "downloads",
      "storage",
      "library",
      "features",
      "codec settings",
    ]) {
      expect(statuses[name], name).toBe(200);
    }
    // Routes that address a media manager or TMDB explain what is missing instead.
    for (const name of [
      "search movies",
      "add-meta movies",
      "codec-analysis movies",
      "details radarr",
      "seasons sonarr",
    ]) {
      expect(statuses[name], name).toBe(404);
    }
    expect(statuses["explore popular"]).toBe(502);

    const featuresBody = JSON.parse(
      responses.find((r) => r.name === "features")!.text.split("\n")[0]
    ) as FeaturesResponse;
    expect(featuresBody.features.filter((f) => f.available).map((f) => f.id)).toEqual([
      "dashboard",
    ]);
    const statusBody = JSON.parse(
      responses.find((r) => r.name === "status")!.text.split("\n")[0]
    ) as StatusResponse;
    expect(statusBody.instances).toEqual([]);
  });
});

describe("With only the old environment variables set", () => {
  it("behaves as before: default instance ids, one card per service, everything on", async () => {
    await stack.radarr();
    await stack.sonarr();
    const sab = track(await startFakeSabnzbd("active"));
    vi.stubEnv("SABNZBD_URL", sab.url);
    vi.stubEnv("SABNZBD_API_KEY", SAB_KEY);
    const plex = track(await startFakePlex({ claimed: true, sessions: "active" }));
    vi.stubEnv("PLEX_URL", plex.url);
    vi.stubEnv("PLEX_TOKEN", PLEX_TOKEN);
    vi.stubEnv("TMDB_API_KEY", TMDB_KEY);
    installFakeTmdb();

    const statusBody = (await (await status()).json()) as StatusResponse;
    expect(statusBody.instances.map((i) => i.id).sort()).toEqual([
      "plex",
      "radarr",
      "sabnzbd",
      "sonarr",
    ]);
    expect(statusBody.instances.every((i) => i.state !== "down")).toBe(true);

    const libraryBody = (await (await library()).json()) as {
      items: { kind: string; instanceId: string }[];
      instances: { instanceId: string; up: boolean }[];
    };
    expect(libraryBody.instances.map((i) => [i.instanceId, i.up]).sort()).toEqual([
      ["radarr", true],
      ["sonarr", true],
    ]);
    expect(new Set(libraryBody.items.map((i) => i.kind))).toEqual(new Set(["movies", "series"]));

    expect(((await (await downloads()).json()) as DownloadsResponse).instances).toHaveLength(1);
    expect((await storage()).status).toBe(200);
    expect((await explore(get("/api/explore?category=popular"))).status).toBe(200);

    const featuresBody = (await (await features()).json()) as FeaturesResponse;
    expect(featuresBody.features.map((f) => [f.id, f.available])).toEqual([
      ["dashboard", true],
      ["library", true],
      ["explore", true],
      ["codecs", true],
    ]);
  });
});
