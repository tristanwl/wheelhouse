import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { startServer } from "./harness";

// Issue #88: the cap on a Radarr list is WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB
// (default 96, 16 to 512). A library over it is a notice a user can act on,
// which names no variable; an invalid value refuses to start.

const MIB = 1 << 20;

/** A Radarr whose movie list is `mib` MiB of valid JSON (a few big movies). */
function bigRadarr(mib: number): Promise<{ url: string; server: Server }> {
  const filler = "x".repeat(512 * 1024);
  const count = Math.ceil((mib * MIB) / filler.length);
  const movie = (i: number) =>
    JSON.stringify({ id: i, title: `Example Movie ${i}`, overview: filler });
  const body = `[${Array.from({ length: count }, (_, i) => movie(i + 1)).join(",")}]`;
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      if (req.url?.startsWith("/api/v3/movie")) {
        res.writeHead(200, { "content-type": "application/json" });
        res.end(body);
      } else {
        res.writeHead(404).end();
      }
    }).listen(0, "127.0.0.1", () =>
      resolve({ url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, server })
    );
  });
}

const open: Server[] = [];
afterEach(async () => {
  await Promise.all(open.splice(0).map((s) => new Promise((r) => s.close(() => r(null)))));
});

describe("the library response cap", () => {
  it("refuses a list over the cap with a notice that names no setting, and a larger cap loads it", async () => {
    const radarr = await bigRadarr(18);
    open.push(radarr.server);
    const env = { RADARR_URL: radarr.url, RADARR_API_KEY: "example-radarr-key" };

    const small = await startServer({ env: { ...env, WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB: "16" } });
    try {
      const res = await small.request("/api/library");
      expect(res.status).toBe(200);
      const text = res.text;
      expect(text).toContain("the library is larger than Wheelhouse is set up to read");
      expect(text).toContain("an administrator can raise the limit (see the documentation)");
      expect(text).not.toMatch(/WHEELHOUSE|_MIB|environment variable/);
      // The administrator's line is in the log: the setting, the limit and the size.
      const log = small.output();
      expect(log).toContain("setting=WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB");
      expect(log).toMatch(/limitMiB=16 .*responseMiB=1[89]/);
      expect(log).not.toContain("example-radarr-key");
    } finally {
      await small.stop();
    }

    const big = await startServer({ env: { ...env, WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB: "32" } });
    try {
      const res = await big.request("/api/library");
      expect(res.status).toBe(200);
      expect(res.text).not.toContain("larger than Wheelhouse is set up to read");
      expect((res.json.items as unknown[]).length).toBeGreaterThan(0);
    } finally {
      await big.stop();
    }
  });

  it.each(["15", "513", "0", "huge"])(
    "refuses to start with an invalid value (%s)",
    async (value) => {
      await expect(
        startServer({ env: { WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB: value }, auth: "none" })
      ).rejects.toThrow(
        /WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB must be a whole number of MiB from 16 to 512/
      );
    }
  );
});
