import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { ManagerKind, MediaManager } from "./types";

const HEALTH_TYPES = ["ok", "notice", "warning", "error"];

/**
 * The invariants every media-manager adapter must satisfy. `setup` returns a
 * client pointed at a service whose library has at least one item, plus the
 * external id (tmdbId or tvdbId) of an item that is NOT in the library yet and
 * a teardown.
 */
export function describeMediaManagerContract(
  name: string,
  kind: ManagerKind,
  setup: () => Promise<{
    client: MediaManager;
    newExternalId: number;
    teardown?: () => Promise<void>;
  }>
) {
  describe(`${name} satisfies the MediaManager contract`, () => {
    let client: MediaManager;
    let newExternalId: number;
    let teardown: (() => Promise<void>) | undefined;

    beforeAll(async () => {
      ({ client, newExternalId, teardown } = await setup());
    });
    afterAll(async () => teardown?.());

    it("knows its kind", () => {
      expect(client.kind).toBe(kind);
    });

    it("testConnection reports ok with a version", async () => {
      const result = await client.testConnection();
      expect(result.ok).toBe(true);
      expect(result.error).toBeUndefined();
      expect(typeof result.version).toBe("string");
      expect(result.version).toBeTruthy();
    });

    it("getStatus returns a version, health, queue length and indexers", async () => {
      const status = await client.getStatus();
      expect(status.version).toBeTruthy();
      expect(Number.isInteger(status.queueCount)).toBe(true);
      expect(status.queueCount).toBeGreaterThanOrEqual(0);
      for (const issue of status.health) {
        expect(HEALTH_TYPES).toContain(issue.type);
        expect(issue.source).toBeTruthy();
        expect(issue.message).toBeTruthy();
      }
      expect(status.indexers.total).toBeGreaterThanOrEqual(status.indexers.enabled);
      expect(status.indexers.enabled).toBeGreaterThanOrEqual(0);
      // Only indexer problems are listed, and each is one of the health messages.
      const messages = status.health.map((h) => h.message);
      for (const issue of status.indexers.issues) expect(messages).toContain(issue);
    });

    it("root folders have a path and non-negative free space in bytes", async () => {
      const roots = await client.getRootFolders();
      expect(roots.length).toBeGreaterThan(0);
      for (const root of roots) {
        expect(root.path).toMatch(/^\//);
        expect(Number.isInteger(root.freeSpace)).toBe(true);
        expect(root.freeSpace).toBeGreaterThanOrEqual(0);
        expect(typeof root.accessible).toBe("boolean");
      }
    });

    it("quality profiles have an id and a name", async () => {
      const profiles = await client.getQualityProfiles();
      expect(profiles.length).toBeGreaterThan(0);
      for (const p of profiles) {
        expect(Number.isInteger(p.id)).toBe(true);
        expect(p.name).toBeTruthy();
      }
    });

    it("library items have unique ids, a path inside a root folder and byte sizes", async () => {
      const roots = await client.getRootFolders();
      const items =
        client.kind === "movies"
          ? (await client.getMovies()).map((m) => ({ ...m, size: m.sizeOnDisk }))
          : (await client.getSeries()).map((s) => ({ ...s, size: s.statistics?.sizeOnDisk ?? 0 }));
      expect(items.length).toBeGreaterThan(0);
      expect(new Set(items.map((i) => i.id)).size).toBe(items.length);
      for (const item of items) {
        expect(item.title).toBeTruthy();
        expect(Number.isInteger(item.size)).toBe(true);
        expect(item.size).toBeGreaterThanOrEqual(0);
        expect(typeof item.monitored).toBe("boolean");
        expect(roots.some((r) => item.path.startsWith(`${r.path}/`))).toBe(true);
        expect(Number.isNaN(Date.parse(item.added))).toBe(false);
      }
    });

    it("details of a library item come back for its id", async () => {
      if (client.kind === "movies") {
        const [first] = await client.getMovies();
        expect((await client.getMovieDetail(first.id)).id).toBe(first.id);
      } else {
        const [first] = await client.getSeries();
        expect((await client.getSeriesDetail(first.id)).id).toBe(first.id);
        const full = await client.getSeriesById(first.id);
        expect(Array.isArray(full.seasons)).toBe(true);
      }
    });

    it("lookup by the external id finds what is not yet in the library", async () => {
      const result = await client.lookupByExternalId(newExternalId);
      expect(result?.title).toBeTruthy();
      expect(result?.id ?? null).toBeNull();
      expect(kind === "movies" ? result?.tmdbId : result?.tvdbId).toBe(newExternalId);
    });

    it("free-text lookup returns titled results", async () => {
      const results = await client.lookup("example");
      expect(results.length).toBeGreaterThan(0);
      for (const r of results) expect(r.title).toBeTruthy();
    });
  });
}
