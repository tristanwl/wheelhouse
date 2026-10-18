import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { DownloadClient, DownloadState } from "./types";

const STATES: DownloadState[] = [
  "queued",
  "downloading",
  "stalled",
  "postprocessing",
  "seeding",
  "paused",
  "completed",
  "failed",
];

/**
 * The invariants every download-client adapter must satisfy, whatever the
 * backend. `setup` returns a client pointed at a service that has at least one
 * active job, plus a teardown.
 */
export function describeDownloadClientContract(
  name: string,
  setup: () => Promise<{
    client: DownloadClient;
    protocol: "usenet" | "torrent";
    teardown?: () => Promise<void>;
  }>
) {
  describe(`${name} satisfies the DownloadClient contract`, () => {
    let client: DownloadClient;
    let protocol: "usenet" | "torrent";
    let teardown: (() => Promise<void>) | undefined;

    beforeAll(async () => {
      ({ client, protocol, teardown } = await setup());
    });
    afterAll(async () => teardown?.());

    it("testConnection reports ok with a version", async () => {
      const result = await client.testConnection();
      expect(result.ok).toBe(true);
      expect(result.error).toBeUndefined();
      expect(typeof result.version).toBe("string");
    });

    it("getStatus returns sane, normalized values", async () => {
      const status = await client.getStatus();
      expect(typeof status.paused).toBe("boolean");
      expect(Number.isInteger(status.speedBytesPerSec)).toBe(true);
      expect(status.speedBytesPerSec).toBeGreaterThanOrEqual(0);
      expect(Number.isInteger(status.queueCount)).toBe(true);
      expect(status.queueCount).toBeGreaterThanOrEqual(0);
      expect(status.etaSeconds === null || status.etaSeconds > 0).toBe(true);
      for (const warning of status.warnings) {
        expect(warning.message.length).toBeGreaterThan(0);
        expect(["warning", "error"]).toContain(warning.severity);
        if (warning.at) expect(Number.isNaN(Date.parse(warning.at))).toBe(false);
      }
    });

    it("listItems returns items that honour the unit and range rules", async () => {
      const items = await client.listItems();
      expect(items.length).toBeGreaterThan(0);
      for (const item of items) {
        expect(item.id).toBeTruthy();
        expect(item.name).toBeTruthy();
        expect(item.protocol).toBe(protocol);
        expect(STATES).toContain(item.state);
        // Bytes, not megabytes: whole numbers.
        expect(Number.isInteger(item.sizeBytes)).toBe(true);
        expect(Number.isInteger(item.remainingBytes)).toBe(true);
        expect(item.remainingBytes).toBeGreaterThanOrEqual(0);
        expect(item.remainingBytes).toBeLessThanOrEqual(item.sizeBytes);
        expect(item.progress).toBeGreaterThanOrEqual(0);
        expect(item.progress).toBeLessThanOrEqual(1);
        expect(item.etaSeconds === null || item.etaSeconds > 0).toBe(true);
      }
    });

    it("item ids are unique", async () => {
      const items = await client.listItems();
      expect(new Set(items.map((i) => i.id)).size).toBe(items.length);
    });
  });
}
