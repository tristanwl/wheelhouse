import { formatBytes, formatEta } from "./format";
import type {
  ClientWarning,
  InstanceState,
  ServiceLink,
  StatusInstance,
  StatusResponse,
} from "./types";

// Pure helpers for the dashboard's service cards.

export type { InstanceState, ServiceLink, StatusInstance, StatusResponse };

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
  const link: ServiceLink = {
    protocol: parsed.protocol === "https:" ? "https" : "http",
    path: `${prefix}${appendPath}` || "/",
  };
  if (parsed.port) link.port = Number(parsed.port);
  return link;
}

/** The address to open, on whatever host the browser used to reach Wheelhouse. */
export function serviceHref(host: string, link: ServiceLink): string {
  return `${link.protocol}://${host}${link.port === undefined ? "" : `:${link.port}`}${link.path}`;
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
          d.torrent
            ? `seeding: ${d.torrent.seeding}${d.torrent.uploadBytesPerSec ? ` (↑ ${formatBytes(d.torrent.uploadBytesPerSec)}/s)` : ""}`
            : "",
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
