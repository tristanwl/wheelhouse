import type {
  ClientWarning,
  DownloadClientStatus,
  MediaManagerStatus,
  MediaServerStatus,
} from "@/lib/providers";
import { formatBytes, formatEta } from "@/lib/format";

// Types and pure helpers for the dashboard's service cards. Type imports only
// from the providers, so the browser bundle doesn't pull in server code.

export type InstanceState = "ok" | "degraded" | "needs-setup" | "down";

/** What the browser needs to build an "open this service" link (the host it takes from its own address bar). */
export interface ServiceLink {
  port?: number;
  path: string;
}

interface StatusInstanceBase {
  id: string;
  providerId: string;
  name: string;
  state: InstanceState;
  version?: string;
  /** Why the instance is down. Redacted. */
  error?: string;
  /** Absent when the configured URL is missing or unusable. */
  link?: ServiceLink;
}

/** One configured instance and its normalized status; `detail` is absent while it is down. */
export type StatusInstance = StatusInstanceBase &
  (
    | { role: "mediaManager"; detail?: MediaManagerStatus }
    | { role: "downloadClient"; detail?: DownloadClientStatus }
    | { role: "mediaServer"; detail?: MediaServerStatus }
  );

export interface StatusResponse {
  instances: StatusInstance[];
}

/** Dashboard groups, in display order. */
export const STATUS_ROLES = [
  { role: "mediaManager", heading: "Media managers" },
  { role: "downloadClient", heading: "Download clients" },
  { role: "mediaServer", heading: "Media servers" },
] as const;

/** Warnings older than this are history, not news. */
export const WARNING_MAX_AGE_MS = 24 * 60 * 60 * 1000;

/** What a card shows for a notice: the message, how bad, and (when known) when. */
export type CardWarning = ClientWarning;

/** Whether a warning is recent enough to show. One without a usable timestamp always is. */
export function isWarningVisible(
  warning: Pick<ClientWarning, "at">,
  now: number = Date.now()
): boolean {
  if (!warning.at) return true;
  const at = new Date(warning.at).getTime();
  if (!Number.isFinite(at)) return true;
  return now - at <= WARNING_MAX_AGE_MS;
}

export function visibleWarnings<T extends Pick<ClientWarning, "at">>(
  warnings: T[],
  now: number = Date.now()
): T[] {
  return warnings.filter((w) => isWarningVisible(w, now));
}

/**
 * Port and path prefix of a configured service URL, plus an optional path to
 * append (Plex's web UI lives under /web). Never the host, credentials or
 * query. undefined when there is no usable http(s) URL.
 */
export function serviceLink(url: unknown, appendPath = ""): ServiceLink | undefined {
  if (typeof url !== "string") return undefined;
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return undefined;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
  const prefix = parsed.pathname.replace(/\/+$/, "");
  const link: ServiceLink = { path: `${prefix}${appendPath}` || "/" };
  if (parsed.port) link.port = Number(parsed.port);
  return link;
}

/** The address to open, on whatever host the browser used to reach Wheelhouse. */
export function serviceHref(host: string, link: ServiceLink): string {
  return `http://${host}${link.port === undefined ? "" : `:${link.port}`}${link.path}`;
}

/** Whether a download client's recent warnings include an error. */
export function hasRecentError(warnings: ClientWarning[], now: number = Date.now()): boolean {
  return visibleWarnings(warnings, now).some((w) => w.severity === "error");
}

/** Short text lines and notices for an instance's card. */
export function describeInstance(instance: StatusInstance): {
  lines: string[];
  warnings: CardWarning[];
} {
  switch (instance.role) {
    case "mediaManager": {
      const d = instance.detail;
      if (!d) return { lines: [], warnings: [] };
      return {
        lines: [
          `queue: ${d.queueCount}`,
          `indexers: ${d.indexers.enabled}/${d.indexers.total}`,
          d.version ? `v${d.version}` : "",
        ].filter(Boolean),
        warnings: d.health
          .filter((h) => h.type !== "ok")
          .map((h) => ({ message: h.message, severity: h.type === "error" ? "error" : "warning" })),
      };
    }
    case "downloadClient": {
      const d = instance.detail;
      if (!d) return { lines: [], warnings: [] };
      return {
        lines: [
          `status: ${d.statusText ?? "-"}`,
          d.paused ? "paused" : "",
          d.speedBytesPerSec ? `${formatBytes(d.speedBytesPerSec)}/s` : "",
          d.queueCount ? `${d.queueCount} in queue` : "",
          d.etaSeconds ? `${formatEta(d.etaSeconds)} left` : "",
          d.version ? `v${d.version}` : "",
          ...(d.usenet?.servers ?? []).map(
            (s) =>
              `${s.enabled ? "server" : "server disabled"}: ${s.name} (${s.connections} conn${s.ssl ? ", ssl" : ""})`
          ),
        ].filter(Boolean),
        warnings: d.warnings,
      };
    }
    case "mediaServer": {
      const d = instance.detail;
      if (!d) return { lines: [], warnings: [] };
      return {
        lines: [
          d.name ?? "",
          d.version ? `v${d.version}` : "",
          d.authenticated
            ? `${d.activeSessions ?? 0} active session${d.activeSessions === 1 ? "" : "s"}`
            : "",
        ].filter(Boolean),
        warnings: d.notice ? [{ message: d.notice, severity: "warning" }] : [],
      };
    }
  }
}
