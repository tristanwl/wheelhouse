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
      TMDB_API_KEY: "tk",
    }).getInstances();

    expect(instances.map((i) => i.id)).toEqual(["sonarr", "radarr", "sabnzbd", "plex", "tmdb"]);
    expect(instances[0]).toEqual({
      id: "sonarr",
      providerId: "sonarr",
      name: "Sonarr",
      enabled: true,
      config: { url: "http://sonarr:8989", apiKey: "sk" },
    });
    expect(instances[3].config).toEqual({ url: "http://plex:32400", token: "pt" });
  });

  it("keeps a half-configured provider so it can be reported as invalid", async () => {
    const instances = await new EnvConfigSource({
      SONARR_URL: "http://sonarr:8989",
    }).getInstances();
    expect(instances).toHaveLength(1);
    expect(instances[0].config).toEqual({ url: "http://sonarr:8989" });
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
});
