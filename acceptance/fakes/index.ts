import {
  GOOD_KEY as SAB_KEY,
  startFakeSabnzbd,
} from "./sabnzbd/server";
import {
  GOOD_PASSWORD as QB_PASSWORD,
  GOOD_USER as QB_USER,
  startFakeQbittorrent,
} from "./qbittorrent/server";
import {
  GOOD_KEY as ARR_KEY,
  fourKMovies,
  startFakeRadarr,
  startFakeSonarr,
  type FakeRadarr,
  type FakeSonarr,
} from "./media-manager/server";
import {
  GOOD_KEY as JELLYFIN_KEY,
  startFakeJellyfin,
} from "./jellyfin/server";
import {
  GOOD_TOKEN as PLEX_TOKEN,
  startFakePlex,
} from "./plex/server";
import {
  GOOD_KEY as TMDB_KEY,
  startFakeTmdbServer,
} from "./tmdb/server";
import { Cleanup, closedUrl, startHangingServer } from "../harness";

/** The credentials each fake accepts. */
export const GOOD = {
  arr: ARR_KEY,
  sabnzbd: SAB_KEY,
  qbittorrentUser: QB_USER,
  qbittorrentPassword: QB_PASSWORD,
  plex: PLEX_TOKEN,
  jellyfin: JELLYFIN_KEY,
  tmdb: TMDB_KEY,
};

/** Every service the app can be configured with. */
export const SERVICES = [
  "radarr",
  "radarr-4k",
  "sonarr",
  "sabnzbd",
  "qbittorrent",
  "plex",
  "jellyfin",
  "tmdb",
] as const;
export type Service = (typeof SERVICES)[number];

/** How one service behaves in a scenario. */
export type Mode =
  /** Up, and the configured credentials are the ones the fake accepts. */
  | "healthy"
  /** Up, but the configured credentials are not the ones it accepts. */
  | "wrong-key"
  /** Nothing listens at the configured URL. */
  | "unreachable"
  /** Accepts connections and never answers. */
  | "hang";

export interface Stack {
  /** Environment variables to start the server with. */
  env: Record<string, string>;
  /** Every secret placed in that environment, for leak scans. */
  secrets: string[];
  /** The fakes that are listening, by service. */
  fakes: {
    radarr?: FakeRadarr;
    "radarr-4k"?: FakeRadarr;
    sonarr?: FakeSonarr;
  } & Partial<Record<Service, { url: string; requests: unknown[] }>>;
  /** Stops every fake. */
  close(): Promise<void>;
}

export interface StackOptions {
  /** Mode per service; anything not listed is "healthy". Omitted services (see `only`) are not configured. */
  modes?: Partial<Record<Service, Mode>>;
  /** Configure only these services (default: all of them). */
  only?: readonly Service[];
  /**
   * Replace the credentials with distinctive fake ones that no fake accepts
   * (so every authenticated call fails). Used by the secret sweep, which
   * runs both with canaries and with the good keys.
   */
  canaries?: boolean;
  /** Ids the fake TMDB returns from its list endpoints. */
  tmdbMovieIds?: number[];
  /** Plant extra secrets in the URLs (user:password@host). */
  urlCredentials?: boolean;
}

const CANARY = {
  radarr: "canary-radarr-key-7f3a91",
  radarr4k: "canary-radarr4k-key-2b8c04",
  sonarr: "canary-sonarr-key-e51d77",
  sabnzbd: "canary-sabnzbd-key-90ac26",
  qbittorrent: "canary-qbittorrent-pass-6b21e9",
  plex: "canary-plex-token-4d6e18",
  jellyfin: "canary-jellyfin-key-a07b53",
  tmdb: "canary-tmdb-key-c3f219",
};

export const URL_CANARY_USER = "canary-user-5e2d71";
export const URL_CANARY_PASSWORD = "canary-pass-81c4f0";

/**
 * Starts the fake services and returns the environment that points a server at
 * them, using the variables the app reads today (README, "Configuration").
 */
export async function startStack(options: StackOptions = {}): Promise<Stack> {
  const only = new Set<Service>(options.only ?? SERVICES);
  const cleanup = new Cleanup();
  const env: Record<string, string> = {};
  const secrets: string[] = [];
  const fakes: Stack["fakes"] = {};
  const mode = (service: Service): Mode => options.modes?.[service] ?? "healthy";

  /** The URL and key to configure for a service in its mode, starting its fake if it needs one. */
  async function endpoint<T extends { url: string; requests: unknown[]; close(): Promise<void> }>(
    service: Service,
    good: string,
    canary: string,
    start: () => Promise<T>
  ): Promise<{ url: string; secret: string }> {
    const m = mode(service);
    // A configured wrong key is simply a value the fake does not accept.
    const secret = options.canaries || m === "wrong-key" ? canary : good;
    if (m === "unreachable") return { url: await closedUrl(), secret };
    if (m === "hang") {
      const hung = cleanup.add(await startHangingServer());
      return { url: hung.url, secret };
    }
    const fake = cleanup.add(await start());
    (fakes as Record<string, unknown>)[service] = fake;
    return { url: fake.url, secret };
  }

  const withCredentials = (url: string) =>
    options.urlCredentials
      ? url.replace("http://", `http://${URL_CANARY_USER}:${URL_CANARY_PASSWORD}@`)
      : url;
  if (options.urlCredentials) secrets.push(URL_CANARY_USER, URL_CANARY_PASSWORD);

  const set = (name: string, value: string, secret = false) => {
    env[name] = value;
    if (secret) secrets.push(value);
  };

  if (only.has("radarr")) {
    const e = await endpoint("radarr", GOOD.arr, CANARY.radarr, () => startFakeRadarr());
    set("RADARR_URL", withCredentials(e.url));
    set("RADARR_API_KEY", e.secret, true);
  }
  if (only.has("radarr-4k")) {
    const e = await endpoint("radarr-4k", GOOD.arr, CANARY.radarr4k, () =>
      startFakeRadarr({ movies: fourKMovies() })
    );
    set("RADARR_4K_URL", withCredentials(e.url));
    set("RADARR_4K_API_KEY", e.secret, true);
  }
  if (only.has("sonarr")) {
    const e = await endpoint("sonarr", GOOD.arr, CANARY.sonarr, () => startFakeSonarr());
    set("SONARR_URL", withCredentials(e.url));
    set("SONARR_API_KEY", e.secret, true);
  }
  if (only.has("sabnzbd")) {
    const e = await endpoint("sabnzbd", GOOD.sabnzbd, CANARY.sabnzbd, () =>
      startFakeSabnzbd("active")
    );
    set("SABNZBD_URL", withCredentials(e.url));
    set("SABNZBD_API_KEY", e.secret, true);
  }
  if (only.has("qbittorrent")) {
    const e = await endpoint("qbittorrent", GOOD.qbittorrentPassword, CANARY.qbittorrent, () =>
      startFakeQbittorrent("v5")
    );
    set("QBITTORRENT_URL", withCredentials(e.url));
    set("QBITTORRENT_USERNAME", GOOD.qbittorrentUser);
    set("QBITTORRENT_PASSWORD", e.secret, true);
  }
  if (only.has("plex")) {
    const e = await endpoint("plex", GOOD.plex, CANARY.plex, () =>
      startFakePlex({ claimed: true, sessions: "active" })
    );
    set("PLEX_URL", withCredentials(e.url));
    set("PLEX_TOKEN", e.secret, true);
  }
  if (only.has("jellyfin")) {
    const e = await endpoint("jellyfin", GOOD.jellyfin, CANARY.jellyfin, () =>
      startFakeJellyfin({ setupCompleted: true, sessions: "playing" })
    );
    set("JELLYFIN_URL", withCredentials(e.url));
    set("JELLYFIN_API_KEY", e.secret, true);
  }
  if (only.has("tmdb")) {
    const e = await endpoint("tmdb", GOOD.tmdb, CANARY.tmdb, () =>
      startFakeTmdbServer({ movieIds: options.tmdbMovieIds })
    );
    set("TMDB_BASE_URL", `${withCredentials(e.url)}/3`);
    set("TMDB_API_KEY", e.secret, true);
  }

  return { env, secrets, fakes, close: () => cleanup.run() };
}
