import { redact } from "../core/http";
import type { ProviderRuntime } from "../core/runtime";
import type { ManagerKind, ManagerOfKind, MediaManager } from "./types";

/** One configured media manager, working or not. */
export interface ManagerEntry<K extends ManagerKind = ManagerKind> {
  instanceId: string;
  name: string;
  kind: K;
  /** Absent when the instance isn't ready (see `error`). */
  client?: ManagerOfKind<K>;
  error?: string;
}

/** What routes tell the UI about each instance, so a broken one is visible and not just missing. */
export interface ManagerSummary {
  instanceId: string;
  name: string;
  kind: ManagerKind;
  up: boolean;
  error?: string;
}

export interface ManagerProblem {
  instanceId: string;
  name: string;
  error: string;
}

export const KIND_LABEL: Record<ManagerKind, string> = { movies: "Movies", series: "TV" };

/** "No Radarr instance is configured": for a route that needs a kind and has none. */
export function noManagerMessage(kind: ManagerKind): string {
  return `No ${kind === "movies" ? "Radarr" : "Sonarr"} instance is configured`;
}

/**
 * Every enabled media-manager instance (of one kind, if given), in configured
 * order. Misconfigured ones are included without a client so callers can
 * report them.
 */
export async function listManagers<K extends ManagerKind>(
  runtime: ProviderRuntime,
  kind?: K
): Promise<ManagerEntry<K>[]> {
  const ready = new Map(
    (await runtime.getClients<MediaManager>("mediaManager")).map((c) => [c.instance.id, c.client])
  );
  const entries: ManagerEntry<K>[] = [];
  for (const resolved of await runtime.listInstances()) {
    const { instance, definition } = resolved;
    if (definition?.role !== "mediaManager" || resolved.status === "disabled") continue;
    const instanceKind = definition.capabilities.kind as ManagerKind;
    if (kind && instanceKind !== kind) continue;
    const client = ready.get(instance.id);
    entries.push({
      instanceId: instance.id,
      name: instance.name,
      kind: instanceKind as K,
      client: client as ManagerOfKind<K> | undefined,
      error: client ? undefined : (resolved.error ?? resolved.status),
    });
  }
  return entries;
}

/**
 * Runs `fn` against every ready entry in parallel. An instance that fails
 * (or isn't ready) is reported in `problems` and doesn't affect the others.
 */
export async function collectFromManagers<K extends ManagerKind, T>(
  entries: ManagerEntry<K>[],
  fn: (client: ManagerOfKind<K>, entry: ManagerEntry<K>) => Promise<T>
): Promise<{ results: { entry: ManagerEntry<K>; value: T }[]; problems: ManagerProblem[] }> {
  const settled = await Promise.all(
    entries.map(async (entry) => {
      if (!entry.client) return { entry, error: entry.error ?? "not ready" };
      try {
        return { entry, value: await fn(entry.client, entry) };
      } catch (err) {
        return { entry, error: err instanceof Error ? redact(err.message) : "unknown error" };
      }
    })
  );
  const results: { entry: ManagerEntry<K>; value: T }[] = [];
  const problems: ManagerProblem[] = [];
  for (const s of settled) {
    if ("error" in s && s.error !== undefined) {
      problems.push({ instanceId: s.entry.instanceId, name: s.entry.name, error: s.error });
    } else {
      results.push({ entry: s.entry, value: (s as { value: T }).value });
    }
  }
  return { results, problems };
}

/** Per-instance summary for a response: every entry, marked down if it had a problem. */
export function summarizeManagers(
  entries: ManagerEntry[],
  problems: ManagerProblem[]
): ManagerSummary[] {
  const failed = new Map(problems.map((p) => [p.instanceId, p.error]));
  return entries.map((e) => {
    const error = failed.get(e.instanceId);
    return {
      instanceId: e.instanceId,
      name: e.name,
      kind: e.kind,
      up: error === undefined,
      ...(error === undefined ? {} : { error }),
    };
  });
}
