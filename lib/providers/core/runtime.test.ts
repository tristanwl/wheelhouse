import { describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { createRegistry, defineProvider } from "./registry";
import { createProviderRuntime } from "./runtime";
import type { ConfigSource, Instance } from "./types";

const fakeConfig = z.object({ url: z.url(), apiKey: z.string().min(1) });

function fakeProvider(id: string, role: "downloadClient" | "mediaServer" = "downloadClient") {
  const create = vi.fn((config: z.infer<typeof fakeConfig>) => ({ id, url: config.url }));
  return {
    create,
    definition: defineProvider({
      id,
      role,
      displayName: id,
      configSchema: fakeConfig,
      secretKeys: ["apiKey"],
      capabilities: { protocol: "usenet" as const },
      create,
    }),
  };
}

function source(instances: Instance[]): ConfigSource {
  return { getInstances: async () => instances };
}

const inst = (id: string, providerId: string, config: unknown, enabled = true): Instance => ({
  id,
  providerId,
  name: id,
  enabled,
  config,
});

describe("createRegistry", () => {
  it("rejects duplicate provider ids", () => {
    const a = fakeProvider("dup").definition;
    expect(() => createRegistry([a, a])).toThrow("Duplicate provider id: dup");
  });

  it("looks providers up by id and role", () => {
    const sab = fakeProvider("sab", "downloadClient").definition;
    const plex = fakeProvider("plex", "mediaServer").definition;
    const registry = createRegistry([sab, plex]);
    expect(registry.get("plex")?.id).toBe("plex");
    expect(registry.get("nope")).toBeUndefined();
    expect(registry.byRole("downloadClient").map((d) => d.id)).toEqual(["sab"]);
  });
});

describe("provider runtime", () => {
  const good = { url: "http://sab:8080", apiKey: "k" };

  it("resolves statuses: ready, disabled, unknown provider, invalid config", async () => {
    const sab = fakeProvider("sab");
    const runtime = createProviderRuntime({
      registry: createRegistry([sab.definition]),
      source: source([
        inst("sab", "sab", good),
        inst("off", "sab", good, false),
        inst("ghost", "nonexistent", {}),
        inst("broken", "sab", { url: "not a url" }),
      ]),
    });
    const list = await runtime.listInstances();
    expect(list.map((r) => [r.instance.id, r.status])).toEqual([
      ["sab", "ready"],
      ["off", "disabled"],
      ["ghost", "unknown-provider"],
      ["broken", "invalid-config"],
    ]);
    const broken = list.find((r) => r.instance.id === "broken");
    expect(broken?.error).toContain("url:");
    expect(broken?.error).toContain("apiKey:");
  });

  it("gives clients only for ready instances of the requested role", async () => {
    const sab = fakeProvider("sab", "downloadClient");
    const plex = fakeProvider("plex", "mediaServer");
    const runtime = createProviderRuntime({
      registry: createRegistry([sab.definition, plex.definition]),
      source: source([
        inst("sab", "sab", good),
        inst("sab-2", "sab", { url: "http://sab2:8080", apiKey: "k" }),
        inst("plex", "plex", { url: "http://plex:32400", apiKey: "t" }),
        inst("bad", "sab", {}),
      ]),
    });
    const downloaders = await runtime.getClients<{ id: string; url: string }>("downloadClient");
    expect(downloaders.map((d) => d.instance.id)).toEqual(["sab", "sab-2"]);
    expect(downloaders[1].client.url).toBe("http://sab2:8080");
  });

  it("reuses a client until its config changes", async () => {
    const sab = fakeProvider("sab");
    const instances = [inst("sab", "sab", good)];
    const runtime = createProviderRuntime({
      registry: createRegistry([sab.definition]),
      source: { getInstances: async () => instances },
    });
    const first = await runtime.getClient("sab");
    expect(await runtime.getClient("sab")).toBe(first);
    expect(sab.create).toHaveBeenCalledTimes(1);

    instances[0] = inst("sab", "sab", { url: "http://other:8080", apiKey: "k" });
    const second = await runtime.getClient("sab");
    expect(second).not.toBe(first);
    expect(sab.create).toHaveBeenCalledTimes(2);
  });

  it("passes the instance identity to the adapter", async () => {
    const sab = fakeProvider("sab");
    const runtime = createProviderRuntime({
      registry: createRegistry([sab.definition]),
      source: source([inst("sab-4k", "sab", good)]),
    });
    await runtime.getClient("sab-4k");
    expect(sab.create).toHaveBeenCalledWith(good, { instanceId: "sab-4k", instanceName: "sab-4k" });
  });

  it("explains why getClient failed", async () => {
    const sab = fakeProvider("sab");
    const runtime = createProviderRuntime({
      registry: createRegistry([sab.definition]),
      source: source([inst("broken", "sab", {}), inst("off", "sab", good, false)]),
    });
    await expect(runtime.getClient("missing")).rejects.toThrow('No instance "missing"');
    await expect(runtime.getClient("off")).rejects.toThrow('Instance "off" is disabled');
    await expect(runtime.getClient("broken")).rejects.toThrow(
      'Instance "broken" is invalid-config'
    );
  });
});
