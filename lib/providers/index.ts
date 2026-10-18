import { EnvConfigSource } from "./core/config-source";
import { createRegistry } from "./core/registry";
import { createProviderRuntime, type ProviderRuntime } from "./core/runtime";

export * from "./core/types";
export { createHttpClient, joinUrl, ProviderError, redact } from "./core/http";
export type { HttpClient, HttpClientOptions, RequestOptions } from "./core/http";
export { createRegistry, defineProvider } from "./core/registry";
export type { AnyProviderDefinition, ProviderRegistry } from "./core/registry";
export { EnvConfigSource } from "./core/config-source";
export { createProviderRuntime } from "./core/runtime";
export type { InstanceStatus, ProviderRuntime, ResolvedInstance } from "./core/runtime";

// Adapters are added here as they are ported (SABnzbd, Plex, Sonarr/Radarr, ...).
export const registry = createRegistry([]);

// One runtime per server process; globalThis keeps it across Next.js dev reloads.
const g = globalThis as unknown as { __wheelhouseProviderRuntime?: ProviderRuntime };

export function getRuntime(): ProviderRuntime {
  return (g.__wheelhouseProviderRuntime ??= createProviderRuntime({
    registry,
    source: new EnvConfigSource(),
  }));
}
