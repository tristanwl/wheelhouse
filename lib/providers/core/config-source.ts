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

// Providers that can have more than one instance. Interim, until the database
// config source (Phase 2): SONARR_<ID>_URL and SONARR_<ID>_API_KEY define an
// extra instance "sonarr-<id>" named "Sonarr <ID>" (RADARR_4K_URL becomes
// "radarr-4k", "Radarr 4K"). Underscores inside <ID> become "-" in the id and
// a space in the name.
const MULTI_INSTANCE: readonly { providerId: string; prefix: string; displayName: string }[] = [
  { providerId: "sonarr", prefix: "SONARR", displayName: "Sonarr" },
  { providerId: "radarr", prefix: "RADARR", displayName: "Radarr" },
];

const EXTRA_FIELDS: Record<string, string> = { URL: "url", API_KEY: "apiKey" };

function extraInstances(
  env: Env,
  { providerId, prefix, displayName }: (typeof MULTI_INSTANCE)[number]
): Instance[] {
  const pattern = new RegExp(`^${prefix}_([A-Za-z0-9][A-Za-z0-9_]*?)_(URL|API_KEY)$`);
  const found = new Map<string, { name: string; config: Record<string, string> }>();
  for (const variable of Object.keys(env).sort()) {
    const match = pattern.exec(variable);
    const value = env[variable];
    if (!match || !value) continue;
    const idText = match[1];
    const key = idText.toLowerCase();
    const entry = found.get(key) ?? {
      name: `${displayName} ${idText.replace(/_/g, " ")}`,
      config: {},
    };
    entry.config[EXTRA_FIELDS[match[2]]] = value;
    found.set(key, entry);
  }
  return [...found.entries()].map(([key, { name, config }]) => ({
    id: `${providerId}-${key.replace(/_/g, "-")}`,
    providerId,
    name,
    enabled: true,
    config,
  }));
}

/**
 * Builds instances from environment variables. A provider becomes an instance
 * as soon as any of its variables is set, so a half-configured service (a URL
 * without its key) shows up as "invalid config" instead of silently vanishing.
 * Sonarr and Radarr also accept extra instances (see MULTI_INSTANCE).
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
      if (Object.keys(config).length > 0) {
        instances.push({
          id: mapping.instanceId,
          providerId: mapping.providerId,
          name: mapping.name,
          enabled: true,
          config,
        });
      }
      const multi = MULTI_INSTANCE.find((m) => m.providerId === mapping.providerId);
      if (multi) instances.push(...extraInstances(this.env, multi));
    }
    return instances;
  }
}
