import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  apiFetch,
  apiJson,
  ApiError,
  getCsrfToken,
  resetApiState,
  setApiHandlers,
  setCsrfToken,
} from "./http";
import { passwordStrength } from "./password";
import { loginUrl, passwordChangeUrl, safeRedirect } from "./redirect";

function res(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

function stubFetch(...answers: (Response | (() => Response))[]) {
  const queue = [...answers];
  const fn = vi.fn<typeof fetch>(async () => {
    const next = queue.shift();
    if (!next) throw new Error("unexpected request");
    return typeof next === "function" ? next() : next;
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

const header = (fn: ReturnType<typeof stubFetch>, i: number, name: string) =>
  new Headers(fn.mock.calls[i][1]?.headers).get(name);

afterEach(() => {
  vi.unstubAllGlobals();
  resetApiState();
});

describe("apiFetch", () => {
  it("sends cookies (same-origin) and no CSRF header on a GET", async () => {
    setCsrfToken("tok");
    const fn = stubFetch(res(200, {}));
    await apiFetch("/api/status");
    expect(fn.mock.calls[0][1]?.credentials).toBe("same-origin");
    expect(header(fn, 0, "X-CSRF-Token")).toBeNull();
  });

  it("sends the CSRF token on every non-GET method", async () => {
    setCsrfToken("tok");
    const fn = stubFetch(res(200, {}), res(200, {}), res(200, {}), res(200, {}));
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) await apiFetch("/api/x", { method });
    for (let i = 0; i < 4; i++) expect(header(fn, i, "X-CSRF-Token")).toBe("tok");
  });

  it("fetches the token from the session first when it has none", async () => {
    const fn = stubFetch(res(200, { csrfToken: "fresh" }), res(200, {}));
    await apiFetch("/api/add", { method: "POST" });
    expect(fn.mock.calls[0][0]).toBe("/api/auth/session");
    expect(header(fn, 1, "X-CSRF-Token")).toBe("fresh");
    expect(getCsrfToken()).toBe("fresh");
  });

  it("refetches the token once on a CSRF 403 and retries", async () => {
    setCsrfToken("stale");
    const fn = stubFetch(
      res(403, { error: "missing or invalid CSRF token" }),
      res(200, { csrfToken: "new" }),
      res(200, { ok: true })
    );
    const out = await apiFetch("/api/add", { method: "POST", body: "{}" });
    expect(out.status).toBe(200);
    expect(fn).toHaveBeenCalledTimes(3);
    expect(header(fn, 0, "X-CSRF-Token")).toBe("stale");
    expect(header(fn, 2, "X-CSRF-Token")).toBe("new");
  });

  it("does not retry a second time, or for another kind of 403", async () => {
    setCsrfToken("stale");
    const fn = stubFetch(
      res(403, { error: "missing or invalid CSRF token" }),
      res(200, { csrfToken: "new" }),
      res(403, { error: "missing or invalid CSRF token" })
    );
    expect((await apiFetch("/api/add", { method: "POST" })).status).toBe(403);
    expect(fn).toHaveBeenCalledTimes(3);

    setCsrfToken("tok");
    const other = stubFetch(res(403, { error: "forbidden" }));
    expect((await apiFetch("/api/x", { method: "POST" })).status).toBe(403);
    expect(other).toHaveBeenCalledTimes(1);
  });

  it("reports a 401 to the handler, but not for an anonymous (login) request", async () => {
    const onUnauthorized = vi.fn();
    setApiHandlers({ onUnauthorized, onSetupRequired: vi.fn(), onPasswordChangeRequired: vi.fn() });
    stubFetch(res(401, { error: "authentication required" }), res(401, { error: "invalid" }));
    await apiFetch("/api/status");
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    await apiFetch("/api/auth/login", { method: "POST", anonymous: true });
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("reports 503 'setup required' but not other 503s", async () => {
    const onSetupRequired = vi.fn();
    setApiHandlers({ onUnauthorized: vi.fn(), onSetupRequired, onPasswordChangeRequired: vi.fn() });
    stubFetch(res(503, { error: "service unavailable" }), res(503, { error: "setup required" }));
    await apiFetch("/api/status");
    expect(onSetupRequired).not.toHaveBeenCalled();
    await apiFetch("/api/status");
    expect(onSetupRequired).toHaveBeenCalledTimes(1);
  });

  it("reports the password-change-required 403 without treating it as CSRF", async () => {
    const onPasswordChangeRequired = vi.fn();
    setCsrfToken("tok");
    setApiHandlers({
      onUnauthorized: vi.fn(),
      onSetupRequired: vi.fn(),
      onPasswordChangeRequired,
    });
    const fn = stubFetch(
      res(403, {
        error: "you must change your password (CSRF no)",
        code: "password-change-required",
      })
    );
    await apiFetch("/api/status", { method: "POST" });
    expect(onPasswordChangeRequired).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("apiJson", () => {
  it("throws an ApiError carrying the server's message, code and Retry-After", async () => {
    stubFetch(res(429, { error: "too many attempts", code: "x" }, { "Retry-After": "120" }));
    const err = await apiJson("/api/auth/password").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 429,
      message: "too many attempts",
      code: "x",
      retryAfter: 120,
    });
  });
});

describe("no other fetch", () => {
  it("only lib/http.ts calls fetch, so every request gets the session, CSRF and 401 handling", () => {
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) walk(path);
        else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) files.push(path);
      }
    };
    walk(resolve(process.cwd(), "src"));
    for (const file of files) {
      if (file.endsWith("lib/http.ts")) continue;
      const text = readFileSync(file, "utf8");
      expect(text, `${file} calls fetch directly`).not.toMatch(/(^|[^\w.])fetch\(/m);
      expect(text, `${file} uses browser storage`).not.toMatch(
        /localStorage|sessionStorage|document\.cookie/
      );
    }
  });
});

describe("safeRedirect", () => {
  it("keeps in-app paths with query and hash", () => {
    expect(safeRedirect("/library?path=%2Fmovies#x")).toBe("/library?path=%2Fmovies#x");
  });
  it.each([
    "//example.com",
    "/\\example.com",
    "https://example.com",
    "javascript:alert(1)",
    "login",
    "",
    "/login",
    "/setup?x=1",
  ])("falls back for %j", (raw) => {
    expect(safeRedirect(raw)).toBe("/");
  });
  it("builds login and password-change URLs", () => {
    expect(loginUrl("/")).toBe("/login");
    expect(loginUrl("/library?a=1")).toBe("/login?redirect=%2Flibrary%3Fa%3D1");
    expect(passwordChangeUrl("/codecs")).toBe("/change-password?redirect=%2Fcodecs");
  });
});

describe("passwordStrength", () => {
  it("requires 12 characters and rates length", () => {
    expect(passwordStrength("").level).toBe(0);
    expect(passwordStrength("short")).toMatchObject({ meetsMinimum: false, level: 1 });
    expect(passwordStrength("abcdefghijkl")).toMatchObject({ meetsMinimum: true, level: 2 });
    expect(passwordStrength("correct horse battery staple")).toMatchObject({ level: 3 });
  });
});
