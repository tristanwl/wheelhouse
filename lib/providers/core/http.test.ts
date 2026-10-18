import http from "node:http";
import type { AddressInfo } from "node:net";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
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
