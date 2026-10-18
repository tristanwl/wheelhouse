import { EnvConfigSource } from "./core/config-source";
import { createRegistry } from "./core/registry";
import { createProviderRuntime, type ProviderRuntime } from "./core/runtime";
import { sabnzbdProvider } from "./download-client/sabnzbd";
import { listManagers } from "./media-manager/instances";
import type { ManagerKind } from "./media-manager/types";
import { radarrProvider } from "./media-manager/radarr";
import { sonarrProvider } from "./media-manager/sonarr";
import { plexProvider } from "./media-server/plex";

export * from "./core/types";
export { createHttpClient, joinUrl, ProviderError, redact } from "./core/http";
export type { HttpClient, HttpClientOptions, RequestOptions } from "./core/http";
export { createRegistry, defineProvider } from "./core/registry";
export type { AnyProviderDefinition, ProviderRegistry } from "./core/registry";
export { EnvConfigSource } from "./core/config-source";
export { createProviderRuntime } from "./core/runtime";
export type { InstanceStatus, ProviderRuntime, ResolvedInstance } from "./core/runtime";

export * from "./download-client/types";
export * from "./media-manager/types";
export {
  collectFromManagers,
  KIND_LABEL,
  noManagerMessage,
  summarizeManagers,
} from "./media-manager/instances";
export type { ManagerEntry, ManagerProblem, ManagerSummary } from "./media-manager/instances";
export * from "./media-server/types";

// Adapters are added here as they are ported.
export const registry = createRegistry([
  sonarrProvider,
  radarrProvider,
  sabnzbdProvider,
  plexProvider,
]);

// One runtime per server process; globalThis keeps it across Next.js dev reloads.
const g = globalThis as unknown as { __wheelhouseProviderRuntime?: ProviderRuntime };

export function getRuntime(): ProviderRuntime {
  return (g.__wheelhouseProviderRuntime ??= createProviderRuntime({
    registry,
    source: new EnvConfigSource(),
  }));
}

/** The configured media managers (all, or one kind), including broken ones. */
export function getMediaManagers<K extends ManagerKind = ManagerKind>(kind?: K) {
  return listManagers(getRuntime(), kind);
}
