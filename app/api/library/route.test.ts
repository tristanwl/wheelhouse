import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { GET, type LibraryResponse } from "./route";

// Real route handler, real env config source and runtime, fake Radarr/Sonarr servers.
describe("/api/library", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
  });
  afterEach(() => stack.stop());

  async function library() {
    return (await (await GET()).json()) as LibraryResponse;
  }

  it("lists a single default Radarr and Sonarr as before, tagged with their instance", async () => {
    await stack.radarr();
    await stack.sonarr();
    const body = await library();

    expect(body.instances).toEqual([
      { instanceId: "sonarr", name: "Sonarr", kind: "series", up: true },
      { instanceId: "radarr", name: "Radarr", kind: "movies", up: true },
    ]);
    expect(body.items).toHaveLength(5 + 3);
    expect(body.items.filter((i) => i.instanceId === "radarr")).toHaveLength(5);
    expect(body.items.filter((i) => i.kind === "series")).toHaveLength(3);
    expect(body.items.map((i) => i.sizeOnDisk)).toEqual(
      [...body.items.map((i) => i.sizeOnDisk)].sort((a, b) => b - a)
    );
    expect(body.items[0]).toMatchObject({
      instanceName: expect.any(String),
      rootFolderPath: expect.stringMatching(/^\/(movies|tv)$/),
    });
  });

  it("includes both Radarr instances (default and radarr-4k)", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() });
    const body = await library();

    expect(body.instances.map((i) => [i.instanceId, i.name, i.up])).toEqual([
      ["radarr", "Radarr", true],
      ["radarr-4k", "Radarr 4K", true],
    ]);
    const fourK = body.items.filter((i) => i.instanceId === "radarr-4k");
    expect(fourK).toHaveLength(2);
    expect(fourK.every((i) => i.instanceName === "Radarr 4K")).toBe(true);
    expect(fourK.every((i) => i.rootFolderPath === "/movies-4k")).toBe(true);
    // Ids can repeat across instances; instanceId + id is the key.
    expect(body.items.filter((i) => i.instanceId === "radarr")).toHaveLength(5);
  });

  it("reports an invalid second instance without breaking the first", async () => {
    await stack.radarr();
    // The 4K instance is configured with a key its server rejects.
    await stack.radarr4k({ movies: fourKMovies() }, "wrong-key-value-123");
    const body = await library();

    expect(body.items).toHaveLength(5);
    expect(body.items.every((i) => i.instanceId === "radarr")).toBe(true);
    expect(body.instances).toEqual([
      { instanceId: "radarr", name: "Radarr", kind: "movies", up: true },
      {
        instanceId: "radarr-4k",
        name: "Radarr 4K",
        kind: "movies",
        up: false,
        error: "radarr-4k: API key incorrect",
      },
    ]);
    expect(JSON.stringify(body)).not.toContain("wrong-key-value-123");
  });

  it("reports a half-configured second instance as invalid config", async () => {
    await stack.radarr();
    vi.stubEnv("RADARR_4K_URL", "http://radarr-4k.example.com:7878");
    const body = await library();

    expect(body.items).toHaveLength(5);
    const broken = body.instances.find((i) => i.instanceId === "radarr-4k")!;
    expect(broken.up).toBe(false);
    expect(broken.error).toContain("apiKey");
  });

  it("reports an unreachable instance", async () => {
    const radarr = await stack.radarr();
    await stack.sonarr();
    await radarr.close();
    const body = await library();

    expect(body.items.every((i) => i.kind === "series")).toBe(true);
    expect(body.instances.find((i) => i.instanceId === "radarr")).toMatchObject({
      up: false,
      error: "radarr: connection refused",
    });
  });

  it("boots with nothing configured and says so", async () => {
    expect(await library()).toEqual({ items: [], instances: [] });
  });

  it("makes one upstream call per instance for repeated requests inside the cache window", async () => {
    const radarr = await stack.radarr();
    await library();
    await library();
    await library();
    expect(radarr.count("GET", "/api/v3/movie")).toBe(1);
  });
});
