import http from "node:http";
import type { AddressInfo } from "node:net";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { createHttpClient, joinUrl, ProviderError, redact } from "./http";

describe("redact", () => {
  it("strips known secret values, including URL-encoded ones", () => {
    expect(redact("key abc123XYZ and abc%2B123", ["abc123XYZ", "abc+123"])).toBe("key *** and ***");
  });

  it("strips common secret query parameters it wasn't told about", () => {
    expect(redact("GET /api?mode=queue&apikey=s3cr3tvalue&output=json")).toBe(
      "GET /api?mode=queue&apikey=***&output=json"
    );
    expect(redact("https://x/y?X-Plex-Token=abcdefgh&a=1")).toBe(
      "https://x/y?X-Plex-Token=***&a=1"
    );
  });

  it("ignores very short secrets that would mangle ordinary text", () => {
    expect(redact("a b c", ["a"])).toBe("a b c");
  });
});

describe("joinUrl", () => {
  it("keeps a path prefix on the base URL", () => {
    expect(joinUrl("http://h:8989/sonarr", "/api/v3/x")).toBe("http://h:8989/sonarr/api/v3/x");
    expect(joinUrl("http://h:8989/sonarr/", "api/v3/x")).toBe("http://h:8989/sonarr/api/v3/x");
    expect(joinUrl("http://h:8989", "/api")).toBe("http://h:8989/api");
  });
});

describe("createHttpClient", () => {
  let server: http.Server;
  let base: string;
  let lastRequest: { url?: string; headers: http.IncomingHttpHeaders } | undefined;

  beforeAll(async () => {
    server = http.createServer((req, res) => {
      lastRequest = { url: req.url, headers: req.headers };
      if (req.url?.startsWith("/sonarr/ok")) {
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ hello: "world" }));
      } else if (req.url?.startsWith("/slow")) {
        setTimeout(() => res.end("late"), 1500);
      } else if (req.url?.startsWith("/notjson")) {
        res.end("<html>");
      } else {
        res.statusCode = 401;
        res.statusMessage = "Unauthorized";
        res.end();
      }
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });

  afterAll(() => new Promise<void>((resolve) => server.close(() => resolve())));

  it("sends the path prefix, query, user agent and configured headers", async () => {
    const client = createHttpClient({
      baseUrl: `${base}/sonarr`,
      label: "sonarr",
      headers: { "X-Api-Key": "k-1234" },
    });
    const body = await client.json<{ hello: string }>("/ok", { query: { a: 1, b: undefined } });
    expect(body).toEqual({ hello: "world" });
    expect(lastRequest?.url).toBe("/sonarr/ok?a=1");
    expect(lastRequest?.headers["x-api-key"]).toBe("k-1234");
    expect(lastRequest?.headers["user-agent"]).toMatch(/^Wheelhouse\/\d/);
  });

  it("throws a ProviderError with the status, without the URL or key", async () => {
    const client = createHttpClient({
      baseUrl: base,
      label: "sab",
      secrets: ["topsecretkey"],
    });
    const err = await client
      .fetch("/denied", { query: { apikey: "topsecretkey" } })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ProviderError);
    expect((err as ProviderError).status).toBe(401);
    expect((err as ProviderError).message).toBe("sab /denied failed: 401 Unauthorized");
    expect((err as ProviderError).message).not.toContain("topsecretkey");
  });

  it("times out instead of hanging", async () => {
    const client = createHttpClient({ baseUrl: base, label: "slowsvc", timeoutMs: 150 });
    await expect(client.fetch("/slow")).rejects.toThrow("slowsvc: timed out after 150ms");
  });

  it("allows a per-call timeout override", async () => {
    const client = createHttpClient({ baseUrl: base, label: "x", timeoutMs: 5000 });
    await expect(client.fetch("/slow", { timeoutMs: 100 })).rejects.toThrow(
      "timed out after 100ms"
    );
  });

  it("reports a refused connection plainly", async () => {
    // Take a free port, then close it again so nothing is listening there.
    const probe = http.createServer();
    await new Promise<void>((resolve) => probe.listen(0, "127.0.0.1", resolve));
    const port = (probe.address() as AddressInfo).port;
    await new Promise<void>((resolve) => probe.close(() => resolve()));

    const client = createHttpClient({ baseUrl: `http://127.0.0.1:${port}`, label: "down" });
    await expect(client.fetch("/x")).rejects.toThrow("down: connection refused");
  });

  it("falls back to the underlying error message for unusual failures", async () => {
    // Port 1 is on fetch's blocked-port list, so it fails before connecting.
    const client = createHttpClient({ baseUrl: "http://127.0.0.1:1", label: "blocked" });
    await expect(client.fetch("/x")).rejects.toThrow("blocked: bad port");
  });

  it("reports invalid JSON as a ProviderError", async () => {
    const client = createHttpClient({ baseUrl: base, label: "bad" });
    await expect(client.json("/notjson")).rejects.toThrow("bad /notjson returned invalid JSON");
  });
});

describe("createHttpClient response cache", () => {
  let server: http.Server;
  let base: string;
  let hits: Record<string, number>;
  let delayMs = 0;

  beforeAll(async () => {
    server = http.createServer((req, res) => {
      const path = new URL(req.url ?? "/", "http://x").pathname;
      hits[path] = (hits[path] ?? 0) + 1;
      setTimeout(() => {
        if (path === "/flaky" && hits[path] === 1) {
          res.statusCode = 500;
          res.statusMessage = "Server Error";
          return res.end();
        }
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ path, n: hits[path] }));
      }, delayMs);
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });

  beforeEach(() => {
    hits = {};
    delayMs = 0;
  });

  afterAll(() => new Promise<void>((resolve) => server.close(() => resolve())));

  const client = () => createHttpClient({ baseUrl: base, label: "cached" });

  it("does not cache unless the call asks for it", async () => {
    const c = client();
    await c.json("/list");
    await c.json("/list");
    expect(hits["/list"]).toBe(2);
  });

  it("answers a second call inside the TTL without going upstream", async () => {
    const c = client();
    const first = await c.json("/list", { cacheTtlMs: 5000 });
    const second = await c.json("/list", { cacheTtlMs: 5000 });
    expect(second).toEqual(first);
    expect(hits["/list"]).toBe(1);
  });

  it("goes upstream again once the TTL has passed", async () => {
    const c = client();
    await c.json("/list", { cacheTtlMs: 40 });
    await new Promise((r) => setTimeout(r, 80));
    await c.json("/list", { cacheTtlMs: 40 });
    expect(hits["/list"]).toBe(2);
  });

  it("keys on path and query", async () => {
    const c = client();
    await c.json("/list", { cacheTtlMs: 5000, query: { page: 1 } });
    await c.json("/list", { cacheTtlMs: 5000, query: { page: 2 } });
    await c.json("/other", { cacheTtlMs: 5000 });
    await c.json("/list", { cacheTtlMs: 5000, query: { page: 1 } });
    expect(hits).toEqual({ "/list": 2, "/other": 1 });
  });

  it("shares one upstream call between concurrent identical requests", async () => {
    delayMs = 60;
    const c = client();
    const results = await Promise.all([
      c.json("/list", { cacheTtlMs: 5000 }),
      c.json("/list", { cacheTtlMs: 5000 }),
      c.json("/list", { cacheTtlMs: 5000 }),
    ]);
    expect(hits["/list"]).toBe(1);
    expect(results[1]).toEqual(results[0]);
    expect(results[2]).toEqual(results[0]);
  });

  it("hands each caller its own copy", async () => {
    const c = client();
    const first = await c.json<{ n: number }>("/list", { cacheTtlMs: 5000 });
    first.n = 999;
    expect((await c.json<{ n: number }>("/list", { cacheTtlMs: 5000 })).n).toBe(1);
  });

  it("never caches errors, and a failed call doesn't poison the next", async () => {
    const c = client();
    await expect(c.json("/flaky", { cacheTtlMs: 5000 })).rejects.toThrow("failed: 500");
    expect(await c.json("/flaky", { cacheTtlMs: 5000 })).toMatchObject({ n: 2 });
    expect(await c.json("/flaky", { cacheTtlMs: 5000 })).toMatchObject({ n: 2 });
    expect(hits["/flaky"]).toBe(2);
  });

  it("shares a failure between concurrent callers but retries afterwards", async () => {
    delayMs = 40;
    const c = client();
    const settled = await Promise.allSettled([
      c.json("/flaky", { cacheTtlMs: 5000 }),
      c.json("/flaky", { cacheTtlMs: 5000 }),
    ]);
    expect(settled.map((s) => s.status)).toEqual(["rejected", "rejected"]);
    expect(hits["/flaky"]).toBe(1);
    await expect(c.json("/flaky", { cacheTtlMs: 5000 })).resolves.toMatchObject({ n: 2 });
  });

  it("invalidate() makes the next read go upstream", async () => {
    const c = client();
    await c.json("/list", { cacheTtlMs: 5000 });
    c.invalidate();
    await c.json("/list", { cacheTtlMs: 5000 });
    expect(hits["/list"]).toBe(2);
  });

  it("does not let a read that was in flight during a write repopulate the cache", async () => {
    delayMs = 60;
    const c = client();
    const inFlight = c.json("/list", { cacheTtlMs: 5000 });
    await new Promise((r) => setTimeout(r, 10));
    c.invalidate(); // a write completed while the read was still pending
    await inFlight;
    delayMs = 0;
    await c.json("/list", { cacheTtlMs: 5000 });
    expect(hits["/list"]).toBe(2);
  });

  it("only caches GET", async () => {
    const c = client();
    await c.fetch("/list", { method: "POST", cacheTtlMs: 5000 });
    await c.fetch("/list", { method: "POST", cacheTtlMs: 5000 });
    expect(hits["/list"]).toBe(2);
  });

  it("keeps caches of different clients (instances) apart", async () => {
    await client().json("/list", { cacheTtlMs: 5000 });
    await client().json("/list", { cacheTtlMs: 5000 });
    expect(hits["/list"]).toBe(2);
  });
});
