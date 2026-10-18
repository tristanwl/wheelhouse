import type { ZodType } from "zod";

/** What a provider does for Wheelhouse. Features declare which roles they need. */
export type Role = "downloadClient" | "mediaManager" | "mediaServer" | "metadata";

/** One configured copy of a provider (e.g. two Radarr instances: HD and 4K). */
export interface Instance {
  /** Stable, URL-safe id: "sonarr", "radarr-4k". Appears in API routes. */
  id: string;
  providerId: string;
  /** Display name. */
  name: string;
  enabled: boolean;
  /** Validated against the provider's configSchema when the instance is resolved. */
  config: unknown;
}

export interface ConnectionResult {
  ok: boolean;
  version?: string;
  /** Already redacted: safe to show in the UI and logs. */
  error?: string;
}

export interface ProviderContext {
  instanceId: string;
  instanceName: string;
}

/**
 * Describes a provider: how to validate its config and how to build a client
 * for it. `capabilities` is static, per provider (not per instance), so the UI
 * can decide what to render without calling the service.
 */
export interface ProviderDefinition<TConfig = unknown, TClient = unknown, TCaps = unknown> {
  /** Unique across all providers: "sonarr", "sabnzbd", "qbittorrent", ... */
  id: string;
  role: Role;
  displayName: string;
  /** Also drives the admin form in a later phase. */
  configSchema: ZodType<TConfig>;
  /** Config fields holding credentials: masked in the UI, stripped from errors. */
  secretKeys?: readonly (keyof TConfig & string)[];
  capabilities: TCaps;
  create(config: TConfig, ctx: ProviderContext): TClient;
}

/** Where instances come from: the environment today, a database later. */
export interface ConfigSource {
  getInstances(): Promise<Instance[]>;
}
