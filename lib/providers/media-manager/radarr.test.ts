import { afterEach, describe, expect, it } from "vitest";
import { ProviderError } from "../core/http";
import { describeMediaManagerContract } from "./contract";
import { GOOD_KEY, startFakeRadarr, type FakeRadarr } from "./__fixtures__/server";
import { radarrProvider } from "./radarr";
import type { MovieManager } from "./types";

const CTX = { instanceId: "radarr", instanceName: "Radarr" };

function clientFor(server: FakeRadarr, apiKey = GOOD_KEY): MovieManager {
  return radarrProvider.create({ url: server.url, apiKey }, CTX);
}

describeMediaManagerContract("radarr", "movies", async () => {
  const server = await startFakeRadarr();
  return { client: clientFor(server), newExternalId: 900090, teardown: server.close };
});

describe("radarr adapter", () => {
  const servers: FakeRadarr[] = [];
  async function start(options: Parameters<typeof startFakeRadarr>[0] = {}) {
    const server = await startFakeRadarr(options);
    servers.push(server);
    return server;
  }
  afterEach(async () => {
    await Promise.all(servers.splice(0).map((s) => s.close()));
  });

  it("is registered as a movies media manager", () => {
    expect(radarrProvider.role).toBe("mediaManager");
    expect(radarrProvider.capabilities).toEqual({ kind: "movies" });
    expect(radarrProvider.secretKeys).toEqual(["apiKey"]);
  });

  it("requires a URL and a non-empty key", () => {
    const schema = radarrProvider.configSchema;
    expect(schema.safeParse({ url: "http://radarr:7878", apiKey: "k" }).success).toBe(true);
    expect(schema.safeParse({ url: "not a url", apiKey: "k" }).success).toBe(false);
    expect(schema.safeParse({ url: "http://radarr:7878", apiKey: "" }).success).toBe(false);
    expect(schema.safeParse({ url: "http://radarr:7878" }).success).toBe(false);
  });

  it("sends the API key as X-Api-Key and talks to /api/v3", async () => {
    const server = await start();
    await clientFor(server).getMovies();
    expect(server.requests[0]).toMatchObject({ method: "GET", pathname: "/api/v3/movie" });
  });

  it("reports a wrong key as 'API key incorrect', without echoing it", async () => {
    const server = await start();
    const client = clientFor(server, "wrong-key-value-123");

    expect(await client.testConnection()).toEqual({
      ok: false,
      error: "radarr: API key incorrect",
    });
    const err = await client.getMovies().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ProviderError);
    expect((err as ProviderError).message).toBe("radarr: API key incorrect");
    expect((err as ProviderError).status).toBe(401);
    expect((err as ProviderError).message).not.toContain("wrong-key-value-123");
  });

  it("reports an unreachable server plainly", async () => {
    const server = await start();
    const client = clientFor(server);
    await server.close();
    expect(await client.testConnection()).toEqual({
      ok: false,
      error: "radarr: connection refused",
    });
  });

  it("keeps movie fields the routes rely on", async () => {
    const server = await start();
    const movies = await clientFor(server).getMovies();
    const withFile = movies.find((m) => m.movieFile)!;
    expect(withFile.movieFile?.mediaInfo?.videoCodec).toBeTruthy();
    expect(withFile.movieFile?.size).toBeGreaterThan(0);
    expect(movies.some((m) => !m.hasFile && m.sizeOnDisk === 0)).toBe(true);
  });

  it("looks a movie up by tmdb id, and leaves 'id' off results that aren't in the library", async () => {
    const server = await start();
    const client = clientFor(server);
    const found = await client.lookupByExternalId(900090);
    expect(server.requests.at(-1)?.query.get("tmdbId")).toBe("900090");
    expect(found && "id" in found).toBe(false);
    const hits = await client.lookup("some term");
    expect(server.requests.at(-1)?.query.get("term")).toBe("some term");
    expect(typeof hits[0].id).toBe("number");
    expect(hits[1].id).toBeUndefined();
  });

  it("adds a movie by POSTing the payload", async () => {
    const server = await start();
    const created = await clientFor(server).add({ title: "Example Movie 90", tmdbId: 900090 });
    expect(created.id).toBe(1000);
    const post = server.requests.find((r) => r.method === "POST")!;
    expect(post.pathname).toBe("/api/v3/movie");
    expect(post.body).toEqual({ title: "Example Movie 90", tmdbId: 900090 });
  });

  it("deletes with files, and copes with the empty 200 response", async () => {
    const server = await start();
    await expect(clientFor(server).remove(2)).resolves.toBeUndefined();
    const del = server.requests.find((r) => r.method === "DELETE")!;
    expect(del.pathname).toBe("/api/v3/movie/2");
    expect(del.query.get("deleteFiles")).toBe("true");
    expect(del.query.get("addImportListExclusion")).toBe("false");
    expect(server.state.movies.some((m) => m.id === 2)).toBe(false);
  });

  it("rescans a movie with the RescanMovie command", async () => {
    const server = await start();
    await clientFor(server).rescan(3);
    const post = server.requests.find((r) => r.method === "POST")!;
    expect(post.pathname).toBe("/api/v3/command");
    expect(post.body).toEqual({ name: "RescanMovie", movieId: 3 });
  });

  describe("response cache", () => {
    it("serves the library list from cache, then refetches after a write", async () => {
      const server = await start();
      const client = clientFor(server);

      const first = await client.getMovies();
      const second = await client.getMovies();
      expect(second).toEqual(first);
      expect(server.count("GET", "/api/v3/movie")).toBe(1);

      await client.remove(first[0].id);
      const afterDelete = await client.getMovies();
      expect(server.count("GET", "/api/v3/movie")).toBe(2);
      expect(afterDelete.some((m) => m.id === first[0].id)).toBe(false);
    });

    it("clears on add and on rescan too", async () => {
      const server = await start();
      const client = clientFor(server);

      await client.getMovies();
      await client.add({ title: "Example Movie 91", tmdbId: 900091 });
      expect((await client.getMovies()).some((m) => m.tmdbId === 900091)).toBe(true);
      expect(server.count("GET", "/api/v3/movie")).toBe(2);

      await client.rescan(1);
      await client.getMovies();
      expect(server.count("GET", "/api/v3/movie")).toBe(3);
    });

    it("does not cache details, status or lookups", async () => {
      const server = await start();
      const client = clientFor(server);
      await client.getMovieDetail(1);
      await client.getMovieDetail(1);
      await client.getStatus();
      await client.getStatus();
      expect(server.count("GET", "/api/v3/movie/1")).toBe(2);
      expect(server.count("GET", "/api/v3/system/status")).toBe(2);
    });

    it("keeps each instance's cache separate", async () => {
      const a = await start();
      const b = await start({ movies: [] });
      expect(await clientFor(a).getMovies()).not.toHaveLength(0);
      expect(await clientFor(b).getMovies()).toHaveLength(0);
    });
  });
});
