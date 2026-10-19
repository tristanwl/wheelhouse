/**
 * The one way the UI talks to the Go server. Every request goes through
 * `apiFetch`, which
 *
 *  - sends the session cookie (same-origin, HttpOnly: this code never sees it);
 *  - sends the CSRF token in `X-CSRF-Token` on every request that is not a GET
 *    or HEAD. The token comes from `GET /api/auth/session` (or the login
 *    response) and lives only in this module's memory, never in storage; after a
 *    CSRF refusal it is fetched again and the request is retried once;
 *  - reports a 401 (the session is gone) and a 503 "setup required" to the
 *    session provider, which sends the person to the login page or the setup
 *    wizard. Login and setup requests are `anonymous`: their 401/503 answers are
 *    ordinary results for the form that made them.
 *
 * A test scans the source so nothing else calls `fetch`.
 */

export const SESSION_URL = "/api/auth/session";

/** An error answer from the API, with the server's own message. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    /** Machine-readable `code` some answers carry, e.g. "password-change-required". */
    readonly code?: string,
    /** Seconds from a Retry-After header, when the server sent one. */
    readonly retryAfter?: number
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiHandlers {
  /** The server said 401 for a request that needed a session. */
  onUnauthorized(): void;
  /** The server said 503 "setup required". */
  onSetupRequired(): void;
  /** The server said the account must change its password first. */
  onPasswordChangeRequired(): void;
}

export interface ApiInit extends RequestInit {
  /** Login, setup and the session probe: 401/503 are results, not redirects. */
  anonymous?: boolean;
}

let csrfToken: string | null = null;
let handlers: ApiHandlers | null = null;
let csrfRefresh: Promise<void> | null = null;

export function setCsrfToken(token: string | null | undefined) {
  csrfToken = token || null;
}

export function getCsrfToken(): string | null {
  return csrfToken;
}

export function setApiHandlers(next: ApiHandlers | null) {
  handlers = next;
}

/** Forgets everything held in memory (sign-out, tests). */
export function resetApiState() {
  csrfToken = null;
  handlers = null;
  csrfRefresh = null;
}

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

/** Fetches the CSRF token from the session endpoint. Concurrent callers share one request. */
export function refreshCsrfToken(): Promise<void> {
  csrfRefresh ??= (async () => {
    try {
      const res = await fetch(SESSION_URL, { credentials: "same-origin" });
      if (res.ok) {
        const body = (await res.json().catch(() => null)) as { csrfToken?: string } | null;
        setCsrfToken(body?.csrfToken);
      }
    } finally {
      csrfRefresh = null;
    }
  })();
  return csrfRefresh;
}

/** Reads an error body once, without consuming the response the caller gets. */
async function errorBody(res: Response): Promise<{ error?: string; code?: string }> {
  try {
    const body: unknown = await res.clone().json();
    if (body && typeof body === "object") return body as { error?: string; code?: string };
  } catch {
    /* not JSON */
  }
  return {};
}

function send(url: string, init: ApiInit, token: string | null): Promise<Response> {
  const { anonymous: _anonymous, ...rest } = init;
  void _anonymous;
  const headers = new Headers(rest.headers);
  if (token && !headers.has("X-CSRF-Token")) headers.set("X-CSRF-Token", token);
  return fetch(url, { ...rest, credentials: "same-origin", headers });
}

/**
 * fetch with the session, CSRF and 401/503 handling above. It returns the
 * response whatever its status (callers check `ok`); use `apiJson` to get the
 * parsed body or an `ApiError`.
 */
export async function apiFetch(url: string, init: ApiInit = {}): Promise<Response> {
  const method = (init.method ?? "GET").toUpperCase();
  const mutating = !SAFE_METHODS.has(method);

  if (mutating && !init.anonymous && !csrfToken) await refreshCsrfToken();
  let res = await send(url, init, mutating && !init.anonymous ? csrfToken : null);

  if (mutating && !init.anonymous && res.status === 403) {
    const body = await errorBody(res);
    if (body.code !== "password-change-required" && /csrf/i.test(body.error ?? "")) {
      setCsrfToken(null);
      await refreshCsrfToken();
      res = await send(url, init, csrfToken);
    }
  }

  if (!init.anonymous) {
    if (res.status === 401) {
      handlers?.onUnauthorized();
    } else if (res.status === 503) {
      if ((await errorBody(res)).error === "setup required") handlers?.onSetupRequired();
    } else if (res.status === 403) {
      if ((await errorBody(res)).code === "password-change-required") {
        handlers?.onPasswordChangeRequired();
      }
    }
  }
  return res;
}

/** The error an API answer describes: our routes explain themselves in an `error` field. */
export async function toApiError(res: Response, url: string): Promise<ApiError> {
  const body = await errorBody(res);
  const retry = Number.parseInt(res.headers.get("Retry-After") ?? "", 10);
  return new ApiError(
    typeof body.error === "string" ? body.error : `request to ${url} failed: ${res.status}`,
    res.status,
    body.code,
    Number.isFinite(retry) ? retry : undefined
  );
}

/** `apiFetch`, then the parsed JSON body; anything but 2xx throws an `ApiError`. */
export async function apiJson<T>(url: string, init: ApiInit = {}): Promise<T> {
  const res = await apiFetch(url, init);
  if (!res.ok) throw await toApiError(res, url);
  return (await res.json()) as T;
}

/** JSON request helper: sets the content type and stringifies the body. */
export function jsonInit(method: string, body: unknown, extra: ApiInit = {}): ApiInit {
  return {
    ...extra,
    method,
    headers: { "Content-Type": "application/json", ...(extra.headers as Record<string, string>) },
    body: JSON.stringify(body),
  };
}
