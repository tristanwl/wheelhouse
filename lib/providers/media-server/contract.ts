import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { MediaServer } from "./types";

/**
 * The invariants every media-server adapter must satisfy. `setup` returns a
 * client for a healthy, claimed server it is authenticated to.
 */
export function describeMediaServerContract(
  name: string,
  setup: () => Promise<{ client: MediaServer; teardown?: () => Promise<void> }>
) {
  describe(`${name} satisfies the MediaServer contract`, () => {
    let client: MediaServer;
    let teardown: (() => Promise<void>) | undefined;

    beforeAll(async () => {
      ({ client, teardown } = await setup());
    });
    afterAll(async () => teardown?.());

    it("testConnection reports ok with a version", async () => {
      const result = await client.testConnection();
      expect(result.ok).toBe(true);
      expect(typeof result.version).toBe("string");
    });

    it("getStatus is ok, authenticated and has a version and name", async () => {
      const status = await client.getStatus();
      expect(status.state).toBe("ok");
      expect(status.authenticated).toBe(true);
      expect(typeof status.version).toBe("string");
      expect(typeof status.name).toBe("string");
    });

    it("session count, when present, is a non-negative integer", async () => {
      const { activeSessions } = await client.getStatus();
      expect(Number.isInteger(activeSessions)).toBe(true);
      expect(activeSessions).toBeGreaterThanOrEqual(0);
    });

    it("notices never mention configuration key names", async () => {
      const { notice } = await client.getStatus();
      if (notice) expect(notice).not.toMatch(/[A-Z]{3,}_[A-Z_]+/);
    });
  });
}
