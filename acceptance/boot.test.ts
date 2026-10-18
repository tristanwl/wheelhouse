import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { startServer, type RunningServer } from "./harness";
import { ALL_GET_CASES, PAGES } from "./routes";

// Scenario 1: the server boots with no configuration at all.
describe("boot with no environment variables", () => {
  let server: RunningServer;
  beforeAll(async () => {
    server = await startServer({ env: {} });
  });
  afterAll(async () => {
    await server?.stop();
  });

  it.each(PAGES)("page $path answers 200 HTML", async ({ path }) => {
    const res = await server.request(path);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/text\/html/);
  });

  it.each(ALL_GET_CASES)("GET $name ($path) answers $none with JSON", async (c) => {
    const res = await server.request(c.path);
    expect(res.status).toBe(c.none);
    expect(res.headers.get("content-type")).toMatch(/application\/json/);
    expect(res.json).toBeTypeOf("object");
    if (c.none >= 400) expect(res.json.error).toBeTypeOf("string");
  });

  it("reports only the Dashboard as available", async () => {
    const res = await server.request("/api/features");
    const features = res.json.features as { id: string; available: boolean; missing: string[] }[];
    expect(features.map((f) => f.id)).toEqual(["dashboard", "library", "explore", "codecs"]);
    expect(features.filter((f) => f.available).map((f) => f.id)).toEqual(["dashboard"]);
    // Each unavailable feature says what it is missing, in words.
    for (const f of features.filter((f) => !f.available)) {
      expect(f.missing.length).toBeGreaterThan(0);
    }
  });

  it("has nothing to show on the aggregate routes", async () => {
    expect((await server.request("/api/status")).json).toEqual({ instances: [] });
    expect((await server.request("/api/downloads")).json).toEqual({ instances: [], items: [] });
    expect((await server.request("/api/library")).json).toEqual({ items: [], instances: [] });
    expect((await server.request("/api/storage")).json).toEqual({ entries: [], instances: [] });
  });

  it("explains what is missing in an error message", async () => {
    for (const path of [
      "/api/search?kind=movies&term=example",
      "/api/codec-analysis?kind=series",
      "/api/explore?category=popular",
    ]) {
      const { json } = await server.request(path);
      expect(json.error).toBeTypeOf("string");
    }
  });

  it("answers unknown routes and wrong methods with an error, never 2xx", async () => {
    expect((await server.request("/api/does-not-exist")).status).toBe(404);
    const wrongMethod = await server.request("/api/status", { method: "DELETE" });
    expect(wrongMethod.status).toBeGreaterThanOrEqual(400);
    expect(wrongMethod.status).toBeLessThan(500);
  });
});
