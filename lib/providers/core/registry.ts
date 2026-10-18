import type { ProviderDefinition, Role } from "./types";

// Definitions are heterogeneous (each has its own config and client type), so
// the registry stores them type-erased; callers narrow with the client type.
export type AnyProviderDefinition = ProviderDefinition<any, any, any>;

export interface ProviderRegistry {
  all(): readonly AnyProviderDefinition[];
  get(id: string): AnyProviderDefinition | undefined;
  byRole(role: Role): AnyProviderDefinition[];
}

/** Identity helper that keeps each provider's config/client/capability types inferred. */
export function defineProvider<TConfig, TClient, TCaps>(
  definition: ProviderDefinition<TConfig, TClient, TCaps>
): ProviderDefinition<TConfig, TClient, TCaps> {
  return definition;
}

export function createRegistry(definitions: readonly AnyProviderDefinition[]): ProviderRegistry {
  const byId = new Map<string, AnyProviderDefinition>();
  for (const def of definitions) {
    if (byId.has(def.id)) {
      throw new Error(`Duplicate provider id: ${def.id}`);
    }
    byId.set(def.id, def);
  }
  return {
    all: () => [...byId.values()],
    get: (id) => byId.get(id),
    byRole: (role) => [...byId.values()].filter((d) => d.role === role),
  };
}
