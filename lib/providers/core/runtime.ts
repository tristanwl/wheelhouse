import type { ZodError } from "zod";
import { ProviderError, redact } from "./http";
import type { AnyProviderDefinition, ProviderRegistry } from "./registry";
import type { ConfigSource, Instance, Role } from "./types";

export type InstanceStatus = "ready" | "disabled" | "unknown-provider" | "invalid-config";

export interface ResolvedInstance {
  instance: Instance;
  /** Absent when the provider id isn't registered. */
  definition?: AnyProviderDefinition;
  status: InstanceStatus;
  /** Why it isn't ready. Redacted. */
  error?: string;
}

export interface ProviderRuntime {
  /** Every configured instance with its status, including broken ones (so the UI can say why). */
  listInstances(): Promise<ResolvedInstance[]>;
  /** The ready instances for a role, each with its client. */
  getClients<T = unknown>(role: Role): Promise<{ instance: Instance; client: T }[]>;
  /** One instance's client. Throws a ProviderError if it isn't ready. */
  getClient<T = unknown>(instanceId: string): Promise<T>;
}

function formatZodError(error: ZodError): string {
  return error.issues.map((i) => `${i.path.join(".") || "config"}: ${i.message}`).join("; ");
}

export function createProviderRuntime(options: {
  registry: ProviderRegistry;
  source: ConfigSource;
}): ProviderRuntime {
  const { registry, source } = options;
  // Clients are reused while an instance's config is unchanged.
  const clients = new Map<string, { configKey: string; client: unknown }>();

  async function resolveAll(): Promise<{ resolved: ResolvedInstance; parsed?: unknown }[]> {
    const instances = await source.getInstances();
    return instances.map((instance) => {
      if (!instance.enabled) return { resolved: { instance, status: "disabled" as const } };
      const definition = registry.get(instance.providerId);
      if (!definition) {
        return {
          resolved: {
            instance,
            status: "unknown-provider" as const,
            error: `Unknown provider "${instance.providerId}"`,
          },
        };
      }
      const result = definition.configSchema.safeParse(instance.config);
      if (!result.success) {
        return {
          resolved: {
            instance,
            definition,
            status: "invalid-config" as const,
            error: redact(formatZodError(result.error)),
          },
        };
      }
      return { resolved: { instance, definition, status: "ready" as const }, parsed: result.data };
    });
  }

  function clientFor(resolved: ResolvedInstance, parsed: unknown): unknown {
    const { instance, definition } = resolved;
    const configKey = JSON.stringify(parsed);
    const cached = clients.get(instance.id);
    if (cached && cached.configKey === configKey) return cached.client;
    const client = definition!.create(parsed, {
      instanceId: instance.id,
      instanceName: instance.name,
    });
    clients.set(instance.id, { configKey, client });
    return client;
  }

  return {
    async listInstances() {
      return (await resolveAll()).map((r) => r.resolved);
    },

    async getClients<T>(role: Role) {
      const out: { instance: Instance; client: T }[] = [];
      for (const { resolved, parsed } of await resolveAll()) {
        if (resolved.status !== "ready" || resolved.definition!.role !== role) continue;
        out.push({ instance: resolved.instance, client: clientFor(resolved, parsed) as T });
      }
      return out;
    },

    async getClient<T>(instanceId: string) {
      const entry = (await resolveAll()).find((r) => r.resolved.instance.id === instanceId);
      if (!entry) throw new ProviderError(`No instance "${instanceId}" is configured`);
      if (entry.resolved.status !== "ready") {
        throw new ProviderError(
          `Instance "${instanceId}" is ${entry.resolved.status}${
            entry.resolved.error ? `: ${entry.resolved.error}` : ""
          }`
        );
      }
      return clientFor(entry.resolved, entry.parsed) as T;
    },
  };
}
