import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startStack, SERVICES, type Stack } from "./fakes";
import { startServer, type RunningServer } from "./harness";

// Scenario 3, continued: services that accept the connection and never answer.
// Nothing here may hang longer than the upstream timeout (8 s today); the test
// waits it out for real, so these run in their own file, in parallel with the rest.

const TIMEOUT_MS = 8000;
const MARGIN_MS = 3000;

describe("no hang longer than the timeout when services never answer", () => {
  let stack: Stack;
  let server: RunningServer;
  beforeAll(async () => {
    stack = await startStack({
      modes: Object.fromEntries(
        SERVICES.filter((s) => s !== "radarr-4k" && s !== "qbittorrent").map((s) => [s, "hang"])
      ),
    });
    server = await startServer({ env: stack.env });
  });
  afterAll(async () => {
    await server?.stop();
    await stack?.close();
  });

  it("hung services are down with a timeout reason; the healthy ones are not slowed", async () => {
    const started = Date.now();
    const timed = async (path: string) => {
      const res = await server.request(path, { timeoutMs: TIMEOUT_MS + MARGIN_MS + 5000 });
      return { res, at: Date.now() - started };
    };
    const [status, downloads, library, storage, search, explore] = await Promise.all([
      timed("/api/status"),
      timed("/api/downloads"),
      timed("/api/library"),
      timed("/api/storage"),
      timed("/api/search?kind=movies&term=example"),
      timed("/api/explore?category=popular"),
    ]);

    for (const { res, at } of [status, downloads, library, storage, search, explore]) {
      expect(at).toBeLessThan(TIMEOUT_MS + MARGIN_MS);
      expect(res.status).toBeLessThan(600);
    }

    const instances = status.res.json.instances;
    expect(instances).toHaveLength(7);
    for (const i of instances) {
      if (["radarr-4k", "qbittorrent"].includes(i.id)) {
        expect(i.state, i.id).not.toBe("down");
      } else {
        expect(i.state, i.id).toBe("down");
        expect(i.error, i.id).toMatch(/timed out/i);
      }
    }

    const up = (list: { instanceId: string; up: boolean }[]) =>
      Object.fromEntries(list.map((i) => [i.instanceId, i.up]));
    expect(up(downloads.res.json.instances)).toEqual({ sabnzbd: false, qbittorrent: true });
    expect(up(library.res.json.instances)).toEqual({
      radarr: false,
      "radarr-4k": true,
      sonarr: false,
    });
    expect(library.res.json.items.length).toBeGreaterThan(0);
    expect(storage.res.status).toBe(200);

    // The search finds results on the healthy instance and reports the other.
    expect(search.res.status).toBe(200);
    expect(search.res.json.results.length).toBeGreaterThan(0);
    expect(search.res.json.problems[0].error).toMatch(/timed out/i);

    // TMDB hangs: Explore says so with a 502 rather than hanging.
    expect(explore.res.status).toBe(502);
    expect(explore.res.json.error).toMatch(/timed out/i);
  });
});
