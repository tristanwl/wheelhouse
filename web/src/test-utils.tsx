import { render } from "@testing-library/react";
import { vi } from "vitest";
import { SWRConfig } from "swr";
import { createMemoryRouter, RouterProvider } from "react-router";
import { routes } from "./routes";
import type { FeaturesResponse } from "./lib/types";

export interface Call {
  method: string;
  /** Path and query, as the UI asked for it. */
  url: string;
  body: unknown;
  headers: Record<string, string>;
}

type Reply = unknown | { status: number; body: unknown } | ((call: Call) => unknown);

/**
 * Replaces fetch with a fake API. `replies` maps "METHOD /path" (the query is
 * ignored unless the key has one) to a JSON body, a { status, body } pair or a
 * function. Anything unmapped answers 404 and is recorded, so a test can assert
 * on exactly what the UI sent.
 */
export function mockApi(replies: Record<string, Reply>) {
  replies = { "GET /api/auth/session": ADMIN_SESSION, ...replies };
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? "GET").toUpperCase();
    const call: Call = {
      method,
      url,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
      headers: Object.fromEntries(new Headers(init?.headers).entries()),
    };
    calls.push(call);
    const path = url.split("?")[0];
    let reply: Reply = undefined;
    let found = false;
    for (const key of [`${method} ${url}`, `${method} ${path}`]) {
      if (key in replies) {
        reply = replies[key];
        found = true;
        break;
      }
    }
    if (!found) return jsonResponse(404, { error: `no mock for ${method} ${url}` });
    if (typeof reply === "function") reply = await (reply as (c: Call) => unknown)(call);
    if (reply && typeof reply === "object" && "status" in reply && "body" in reply) {
      const r = reply as { status: number; body: unknown };
      return jsonResponse(r.status, r.body);
    }
    return jsonResponse(200, reply);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock };
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** The session answer of a signed-in administrator (the default for mockApi). */
export const ADMIN_SESSION = {
  user: { id: "a1", username: "admin", role: "admin", mustChangePassword: false },
  csrfToken: "csrf-test-token",
};
export const USER_SESSION = {
  user: { id: "u1", username: "alice", role: "user", mustChangePassword: false },
  csrfToken: "csrf-test-token",
};

export const ALL_FEATURES: FeaturesResponse = {
  features: [
    { id: "dashboard", label: "Dashboard", route: "/", available: true, missing: [] },
    { id: "library", label: "Library", route: "/library", available: true, missing: [] },
    { id: "explore", label: "Explore", route: "/explore", available: true, missing: [] },
    { id: "codecs", label: "Codecs", route: "/codecs", available: true, missing: [] },
  ],
};

/** The whole app (layout, nav, routes) at a URL, with a fresh SWR cache. */
export function renderApp(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const view = render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <RouterProvider router={router} />
    </SWRConfig>
  );
  return { router, ...view };
}
