import { describe, expect, it } from "vitest";
import { EnvConfigSource } from "./config-source";

describe("EnvConfigSource", () => {
  it("returns nothing when no provider variables are set", async () => {
    expect(await new EnvConfigSource({}).getInstances()).toEqual([]);
  });

  it("maps the legacy variables to stable instance ids", async () => {
    const instances = await new EnvConfigSource({
      SONARR_URL: "http://sonarr:8989",
      SONARR_API_KEY: "sk",
      RADARR_URL: "http://radarr:7878",
      RADARR_API_KEY: "rk",
      SABNZBD_URL: "http://sab:8080",
      SABNZBD_API_KEY: "ak",
      PLEX_URL: "http://plex:32400",
      PLEX_TOKEN: "pt",
      JELLYFIN_URL: "http://jellyfin:8096",
      JELLYFIN_API_KEY: "jk",
      TMDB_API_KEY: "tk",
    }).getInstances();

    expect(instances.map((i) => i.id)).toEqual([
      "sonarr",
      "radarr",
      "sabnzbd",
      "plex",
      "jellyfin",
      "tmdb",
    ]);
    expect(instances[0]).toEqual({
      id: "sonarr",
      providerId: "sonarr",
      name: "Sonarr",
      enabled: true,
      config: { url: "http://sonarr:8989", apiKey: "sk" },
    });
    expect(instances[3].config).toEqual({ url: "http://plex:32400", token: "pt" });
    expect(instances[4]).toEqual({
      id: "jellyfin",
      providerId: "jellyfin",
      name: "Jellyfin",
      enabled: true,
      config: { url: "http://jellyfin:8096", apiKey: "jk" },
    });
  });

  it("keeps a half-configured provider so it can be reported as invalid", async () => {
    const instances = await new EnvConfigSource({
      SONARR_URL: "http://sonarr:8989",
    }).getInstances();
    expect(instances).toHaveLength(1);
    expect(instances[0].config).toEqual({ url: "http://sonarr:8989" });
  });

  it("maps QBITTORRENT_* to the qbittorrent instance, and reports a half-configured one", async () => {
    const full = await new EnvConfigSource({
      QBITTORRENT_URL: "http://qbittorrent:8080",
      QBITTORRENT_USERNAME: "admin",
      QBITTORRENT_PASSWORD: "pw",
    }).getInstances();
    expect(full).toEqual([
      {
        id: "qbittorrent",
        providerId: "qbittorrent",
        name: "qBittorrent",
        enabled: true,
        config: { url: "http://qbittorrent:8080", username: "admin", password: "pw" },
      },
    ]);

    const half = await new EnvConfigSource({
      QBITTORRENT_URL: "http://qbittorrent:8080",
    }).getInstances();
    expect(half[0].config).toEqual({ url: "http://qbittorrent:8080" });
  });

  it("treats empty variables as unset", async () => {
    const instances = await new EnvConfigSource({
      SONARR_URL: "",
      SONARR_API_KEY: "",
    }).getInstances();
    expect(instances).toEqual([]);
  });

  it("reads the environment on every call", async () => {
    const env: Record<string, string | undefined> = {};
    const source = new EnvConfigSource(env);
    expect(await source.getInstances()).toHaveLength(0);
    env.TMDB_API_KEY = "late";
    expect(await source.getInstances()).toHaveLength(1);
  });

  describe("extra Sonarr and Radarr instances", () => {
    it("builds an instance from RADARR_<ID>_URL and RADARR_<ID>_API_KEY", async () => {
      const instances = await new EnvConfigSource({
        RADARR_URL: "http://radarr:7878",
        RADARR_API_KEY: "rk",
        RADARR_4K_URL: "http://radarr-4k:7878",
        RADARR_4K_API_KEY: "rk4",
      }).getInstances();

      expect(instances.map((i) => i.id)).toEqual(["radarr", "radarr-4k"]);
      expect(instances[1]).toEqual({
        id: "radarr-4k",
        providerId: "radarr",
        name: "Radarr 4K",
        enabled: true,
        config: { url: "http://radarr-4k:7878", apiKey: "rk4" },
      });
    });

    it("works the same for Sonarr, and without the default instance", async () => {
      const instances = await new EnvConfigSource({
        SONARR_ANIME_URL: "http://anime:8989",
        SONARR_ANIME_API_KEY: "ak",
      }).getInstances();
      expect(instances).toEqual([
        {
          id: "sonarr-anime",
          providerId: "sonarr",
          name: "Sonarr ANIME",
          enabled: true,
          config: { url: "http://anime:8989", apiKey: "ak" },
        },
      ]);
    });

    it("keeps a half-configured extra instance so it shows up as invalid", async () => {
      const instances = await new EnvConfigSource({
        RADARR_4K_URL: "http://radarr-4k:7878",
      }).getInstances();
      expect(instances).toHaveLength(1);
      expect(instances[0].id).toBe("radarr-4k");
      expect(instances[0].config).toEqual({ url: "http://radarr-4k:7878" });
    });

    it("does not mistake the default variables for extra instances", async () => {
      const instances = await new EnvConfigSource({
        RADARR_URL: "http://radarr:7878",
        RADARR_API_KEY: "rk",
        SONARR_URL: "http://sonarr:8989",
        SONARR_API_KEY: "sk",
      }).getInstances();
      expect(instances.map((i) => i.id)).toEqual(["sonarr", "radarr"]);
    });

    it("lists extras right after their default, in a stable order, ignoring empty values", async () => {
      const instances = await new EnvConfigSource({
        RADARR_B_URL: "http://b",
        RADARR_B_API_KEY: "k",
        RADARR_A_URL: "http://a",
        RADARR_A_API_KEY: "k",
        RADARR_EMPTY_URL: "",
        RADARR_URL: "http://radarr",
        RADARR_API_KEY: "k",
        SABNZBD_URL: "http://sab",
        SABNZBD_API_KEY: "k",
      }).getInstances();
      expect(instances.map((i) => i.id)).toEqual(["radarr", "radarr-a", "radarr-b", "sabnzbd"]);
    });

    it("turns underscores in the ID into hyphens (id) and spaces (name)", async () => {
      const [instance] = await new EnvConfigSource({
        RADARR_UHD_EXTRA_URL: "http://x",
        RADARR_UHD_EXTRA_API_KEY: "k",
      }).getInstances();
      expect(instance.id).toBe("radarr-uhd-extra");
      expect(instance.name).toBe("Radarr UHD EXTRA");
    });

    it("does not apply to providers that have a single instance", async () => {
      expect(
        await new EnvConfigSource({
          PLEX_X_URL: "http://x",
          SABNZBD_X_URL: "http://x",
        }).getInstances()
      ).toEqual([]);
    });
  });
});
