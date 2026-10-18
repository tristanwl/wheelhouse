import { afterEach, describe, expect, it } from "vitest";
import { createProviderRuntime } from "../core/runtime";
import { createRegistry } from "../core/registry";
import { GOOD_KEY, installFakeTmdb } from "./__fixtures__/fake-tmdb";
import { startFakeTmdbServer } from "./__fixtures__/tmdb-server";
import { tmdbProvider, type TmdbClient } from "./tmdb";

const create = (apiKey: string) =>
  tmdbProvider.create({ apiKey }, { instanceId: "tmdb", instanceName: "TMDB" });

describe("tmdb provider", () => {
  let fake: ReturnType<typeof installFakeTmdb>;
  afterEach(() => fake?.restore());

  it("is a metadata provider whose key is a secret", () => {
    expect(tmdbProvider.role).toBe("metadata");
    expect(tmdbProvider.secretKeys).toEqual(["apiKey"]);
    expect(tmdbProvider.configSchema.safeParse({}).success).toBe(false);
    expect(tmdbProvider.configSchema.safeParse({ apiKey: "" }).success).toBe(false);
    expect(tmdbProvider.configSchema.safeParse({ apiKey: GOOD_KEY }).success).toBe(true);
  });

  it("tests the connection with a cheap authenticated call", async () => {
    fake = installFakeTmdb();
    expect(await create(GOOD_KEY).testConnection()).toEqual({ ok: true });
    expect(fake.requests.map((r) => r.pathname)).toEqual(["/3/configuration"]);
    expect(fake.requests[0].searchParams.get("api_key")).toBe(GOOD_KEY);
  });

  it("reports a rejected key without echoing it", async () => {
    fake = installFakeTmdb();
    const result = await create("wrong-key-value-123").testConnection();
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/API key rejected/);
    expect(JSON.stringify(result)).not.toContain("wrong-key-value-123");
  });

  it("reports an unreachable TMDB without echoing the key", async () => {
    fake = installFakeTmdb({ mode: "unreachable" });
    const result = await create(GOOD_KEY).testConnection();
    expect(result).toEqual({ ok: false, error: "tmdb: connection refused" });
  });

  it("keeps the key out of errors from other calls too", async () => {
    fake = installFakeTmdb();
    const err = await create("wrong-key-value-123")
      .get("/movie/popular")
      .catch((e: Error) => e);
    expect((err as Error).message).toBe("tmdb /movie/popular failed: 401 ");
    expect((err as Error).message).not.toContain("wrong-key-value-123");
  });

  it("resolves through the runtime from TMDB_API_KEY-style config", async () => {
    fake = installFakeTmdb();
    const runtime = createProviderRuntime({
      registry: createRegistry([tmdbProvider]),
      source: {
        getInstances: async () => [
          {
            id: "tmdb",
            providerId: "tmdb",
            name: "TMDB",
            enabled: true,
            config: { apiKey: GOOD_KEY },
          },
        ],
      },
    });
    const [{ client }] = await runtime.getClients<TmdbClient>("metadata");
    expect((await client.testConnection()).ok).toBe(true);
  });

  it("talks to the configured base URL instead of api.themoviedb.org", async () => {
    const server = await startFakeTmdbServer();
    try {
      const client = tmdbProvider.create(
        { apiKey: GOOD_KEY, url: `${server.url}/3` },
        { instanceId: "tmdb", instanceName: "TMDB" }
      );
      expect(await client.testConnection()).toEqual({ ok: true });
      const popular = await client.get<{ results: { id: number }[] }>("/movie/popular");
      expect(popular.results.map((m) => m.id)).toEqual([101, 102, 103]);
      expect(server.requests.map((r) => r.pathname)).toEqual([
        "/3/configuration",
        "/3/movie/popular",
      ]);
    } finally {
      await server.close();
    }
  });

  it("rejects a base URL that is not a URL", () => {
    expect(tmdbProvider.configSchema.safeParse({ apiKey: GOOD_KEY, url: "nope" }).success).toBe(
      false
    );
  });
});
