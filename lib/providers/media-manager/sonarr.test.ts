import { afterEach, describe, expect, it } from "vitest";
import { ProviderError } from "../core/http";
import { describeMediaManagerContract } from "./contract";
import { GOOD_KEY, startFakeSonarr, type FakeSonarr } from "./__fixtures__/server";
import indexers from "./__fixtures__/sonarr/indexer.json";
import { sonarrProvider } from "./sonarr";
import type { SeriesManager } from "./types";

const CTX = { instanceId: "sonarr", instanceName: "Sonarr" };

function clientFor(server: FakeSonarr, apiKey = GOOD_KEY): SeriesManager {
  return sonarrProvider.create({ url: server.url, apiKey }, CTX);
}

describeMediaManagerContract("sonarr", "series", async () => {
  const server = await startFakeSonarr();
  return { client: clientFor(server), newExternalId: 800099, teardown: server.close };
});

describe("sonarr adapter", () => {
  const servers: FakeSonarr[] = [];
  async function start(options: Parameters<typeof startFakeSonarr>[0] = {}) {
    const server = await startFakeSonarr(options);
    servers.push(server);
    return server;
  }
  afterEach(async () => {
    await Promise.all(servers.splice(0).map((s) => s.close()));
  });

  it("is registered as a series media manager", () => {
    expect(sonarrProvider.role).toBe("mediaManager");
    expect(sonarrProvider.capabilities).toEqual({ kind: "series" });
    expect(sonarrProvider.secretKeys).toEqual(["apiKey"]);
  });

  it("requires a URL and a non-empty key", () => {
    const schema = sonarrProvider.configSchema;
    expect(schema.safeParse({ url: "http://sonarr:8989", apiKey: "k" }).success).toBe(true);
    expect(schema.safeParse({ url: "sonarr", apiKey: "k" }).success).toBe(false);
    expect(schema.safeParse({ url: "http://sonarr:8989", apiKey: "" }).success).toBe(false);
  });

  it("reports a wrong key as 'API key incorrect', without echoing it", async () => {
    const server = await start();
    const client = clientFor(server, "wrong-key-value-123");
    expect(await client.testConnection()).toEqual({
      ok: false,
      error: "sonarr: API key incorrect",
    });
    const err = await client.getSeries().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ProviderError);
    expect((err as ProviderError).message).not.toContain("wrong-key-value-123");
  });

  it("normalizes status: queue length from totalRecords, indexer counts and issues", async () => {
    const server = await start();
    const status = await clientFor(server).getStatus();
    expect(status.version).toBe("4.0.15.2941");
    expect(status.queueCount).toBe(2);
    expect(status.indexers.total).toBe(indexers.length);
    expect(status.indexers.enabled).toBe(indexers.filter((i) => i.enableRss).length);
    expect(status.indexers.issues).toEqual([
      "Example health message for IndexerLongTermStatusCheck",
    ]);
  });

  it("asks for series by tvdb term", async () => {
    const server = await start();
    const found = await clientFor(server).lookupByExternalId(800099);
    expect(server.requests.at(-1)?.query.get("term")).toBe("tvdb:800099");
    expect(found?.tvdbId).toBe(800099);
  });

  it("reads episode files and episodes per series", async () => {
    const server = await start();
    const client = clientFor(server);
    const files = await client.getEpisodeFiles(1);
    const episodes = await client.getEpisodes(1);
    expect(files).toHaveLength(1);
    expect(files[0].mediaInfo?.videoCodec).toBeTruthy();
    expect(episodes.every((e) => e.seriesId === 1)).toBe(true);
    expect(episodes.some((e) => e.episodeFileId === files[0].id)).toBe(true);
  });

  it("deletes a series with files, an episode file, and rescans with RescanSeries", async () => {
    const server = await start();
    const client = clientFor(server);

    await client.remove(2);
    const del = server.requests.find((r) => r.method === "DELETE")!;
    expect(del.pathname).toBe("/api/v3/series/2");
    expect(del.query.get("deleteFiles")).toBe("true");

    const [file] = await client.getEpisodeFiles(1);
    await client.deleteEpisodeFile(file.id);
    expect(server.requests.some((r) => r.pathname === `/api/v3/episodefile/${file.id}`)).toBe(true);

    await client.rescan(1);
    expect(server.requests.at(-1)?.body).toEqual({ name: "RescanSeries", seriesId: 1 });
  });

  it("updates a series with PUT", async () => {
    const server = await start();
    const client = clientFor(server);
    const series = await client.getSeriesById(1);
    series.seasons = series.seasons.map((s) => ({ ...s, monitored: false }));
    await client.updateSeries(series);
    const put = server.requests.find((r) => r.method === "PUT")!;
    expect(put.pathname).toBe("/api/v3/series/1");
    expect(
      (put.body as { seasons: { monitored: boolean }[] }).seasons.every((s) => !s.monitored)
    ).toBe(true);
  });

  describe("response cache", () => {
    it("serves the library list from cache, then refetches after any write", async () => {
      const server = await start();
      const client = clientFor(server);

      await client.getSeries();
      await client.getSeries();
      expect(server.count("GET", "/api/v3/series")).toBe(1);

      await client.remove(3);
      await client.getSeries();
      expect(server.count("GET", "/api/v3/series")).toBe(2);

      const [file] = await client.getEpisodeFiles(1);
      await client.deleteEpisodeFile(file.id);
      await client.getSeries();
      expect(server.count("GET", "/api/v3/series")).toBe(3);

      await client.getSeries();
      expect(server.count("GET", "/api/v3/series")).toBe(3);
    });
  });
});
