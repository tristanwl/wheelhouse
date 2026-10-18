import type { ConfigSource, Instance } from "./types";

interface EnvMapping {
  providerId: string;
  instanceId: string;
  name: string;
  /** config field -> environment variable */
  vars: Record<string, string>;
}

// The legacy single-instance environment variables. Each provider here yields
// one instance whose id is stable (so existing routes like
// /api/library/[service] keep working). New providers add a row when their
// adapter lands.
export const ENV_MAPPINGS: readonly EnvMapping[] = [
  {
    providerId: "sonarr",
    instanceId: "sonarr",
    name: "Sonarr",
    vars: { url: "SONARR_URL", apiKey: "SONARR_API_KEY" },
  },
  {
    providerId: "radarr",
    instanceId: "radarr",
    name: "Radarr",
    vars: { url: "RADARR_URL", apiKey: "RADARR_API_KEY" },
  },
  {
    providerId: "sabnzbd",
    instanceId: "sabnzbd",
    name: "SABnzbd",
    vars: { url: "SABNZBD_URL", apiKey: "SABNZBD_API_KEY" },
  },
  {
    providerId: "plex",
    instanceId: "plex",
    name: "Plex",
    vars: { url: "PLEX_URL", token: "PLEX_TOKEN" },
  },
  {
    providerId: "tmdb",
    instanceId: "tmdb",
    name: "TMDB",
    vars: { apiKey: "TMDB_API_KEY" },
  },
];

type Env = Record<string, string | undefined>;

/**
 * Builds instances from environment variables. A provider becomes an instance
 * as soon as any of its variables is set, so a half-configured service (a URL
 * without its key) shows up as "invalid config" instead of silently vanishing.
 * Read lazily, on every call, so tests and late-set variables behave.
 */
export class EnvConfigSource implements ConfigSource {
  constructor(private readonly env: Env = process.env) {}

  async getInstances(): Promise<Instance[]> {
    const instances: Instance[] = [];
    for (const mapping of ENV_MAPPINGS) {
      const config: Record<string, string> = {};
      for (const [field, variable] of Object.entries(mapping.vars)) {
        const value = this.env[variable];
        if (value) config[field] = value;
      }
      if (Object.keys(config).length === 0) continue;
      instances.push({
        id: mapping.instanceId,
        providerId: mapping.providerId,
        name: mapping.name,
        enabled: true,
        config,
      });
    }
    return instances;
  }
}
