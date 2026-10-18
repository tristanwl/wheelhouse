import { version } from "../../../package.json";

const DEFAULT_TIMEOUT_MS = 8000;

/** An error that is safe to show to users and write to logs: secrets are already stripped. */
export class ProviderError extends Error {
  readonly status?: number;

  constructor(message: string, status?: number) {
    super(message);
    this.name = "ProviderError";
    this.status = status;
  }
}

// Secrets that appear in URLs and error text even when we weren't told about
// them (SABnzbd, for one, takes its API key as a query parameter).
const SECRET_PARAM = /\b(api[_-]?key|token|password|passwd|secret|x-plex-token)=([^&\s"'<>]+)/gi;

/** Replaces known secret values and common secret query parameters with ***. */
export function redact(text: string, secrets: readonly string[] = []): string {
  let out = text;
  for (const secret of secrets) {
    // Very short values would mangle unrelated text (and aren't real secrets).
    if (secret.length < 4) continue;
    for (const variant of new Set([secret, encodeURIComponent(secret)])) {
      out = out.split(variant).join("***");
    }
  }
  return out.replace(SECRET_PARAM, "$1=***");
}

export interface HttpClientOptions {
  /** Instance URL; may include a path prefix ("http://host:8989/sonarr"). */
  baseUrl: string;
  /** Used to prefix error messages: "sonarr", "radarr-4k". */
  label: string;
  headers?: Record<string, string>;
  /** Secret values to strip from any error message. */
  secrets?: readonly string[];
  timeoutMs?: number;
}

export interface RequestOptions extends Omit<RequestInit, "signal"> {
  query?: Record<string, string | number | boolean | undefined>;
  timeoutMs?: number;
}

export interface HttpClient {
  /** Resolves with the Response; throws ProviderError on network errors and non-2xx. */
  fetch(path: string, init?: RequestOptions): Promise<Response>;
  json<T>(path: string, init?: RequestOptions): Promise<T>;
}

/** Joins a base URL (with optional path prefix) and a path without dropping the prefix. */
export function joinUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}`;
}

const NETWORK_REASONS: Record<string, string> = {
  ECONNREFUSED: "connection refused",
  ENOTFOUND: "host not found",
  EAI_AGAIN: "host not found",
  ECONNRESET: "connection reset",
  EHOSTUNREACH: "host unreachable",
  ENETUNREACH: "network unreachable",
  ETIMEDOUT: "connection timed out",
  UND_ERR_CONNECT_TIMEOUT: "connection timed out",
  DEPTH_ZERO_SELF_SIGNED_CERT: "TLS certificate is self-signed",
  SELF_SIGNED_CERT_IN_CHAIN: "TLS certificate is self-signed",
  UNABLE_TO_VERIFY_LEAF_SIGNATURE: "TLS certificate could not be verified",
  CERT_HAS_EXPIRED: "TLS certificate has expired",
};

function describeNetworkError(err: unknown): string {
  if (err instanceof Error) {
    if (err.name === "TimeoutError" || err.name === "AbortError") return "timed out";
    const cause = (err as { cause?: { code?: string; message?: string } }).cause;
    const code = cause?.code;
    if (code && NETWORK_REASONS[code]) return NETWORK_REASONS[code];
    if (code) return code;
    // e.g. "bad port" for a port fetch refuses to use; more useful than "fetch failed".
    return cause?.message || err.message || "request failed";
  }
  return "request failed";
}

export function createHttpClient(options: HttpClientOptions): HttpClient {
  const { baseUrl, label, headers = {}, secrets = [], timeoutMs = DEFAULT_TIMEOUT_MS } = options;

  async function doFetch(path: string, init: RequestOptions = {}): Promise<Response> {
    const { query, timeoutMs: perCallTimeout, headers: extraHeaders, ...rest } = init;
    const url = new URL(joinUrl(baseUrl, path));
    for (const [key, value] of Object.entries(query ?? {})) {
      if (value !== undefined) url.searchParams.set(key, String(value));
    }
    const effectiveTimeout = perCallTimeout ?? timeoutMs;

    let res: Response;
    try {
      res = await fetch(url, {
        cache: "no-store",
        ...rest,
        headers: {
          "User-Agent": `Wheelhouse/${version}`,
          ...headers,
          ...(extraHeaders as Record<string, string> | undefined),
        },
        signal: AbortSignal.timeout(effectiveTimeout),
      });
    } catch (err) {
      const reason = describeNetworkError(err);
      const detail = reason === "timed out" ? `timed out after ${effectiveTimeout}ms` : reason;
      throw new ProviderError(redact(`${label}: ${detail}`, secrets));
    }

    if (!res.ok) {
      // Don't echo the URL: it can carry a key in the query string.
      throw new ProviderError(
        redact(`${label} ${path.split("?")[0]} failed: ${res.status} ${res.statusText}`, secrets),
        res.status
      );
    }
    return res;
  }

  return {
    fetch: doFetch,
    async json<T>(path: string, init?: RequestOptions): Promise<T> {
      const res = await doFetch(path, init);
      try {
        return (await res.json()) as T;
      } catch {
        throw new ProviderError(`${label} ${path.split("?")[0]} returned invalid JSON`);
      }
    },
  };
}
