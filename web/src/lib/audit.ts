/** Plain-language labels and detail text for the audit log. */

const LABELS: Record<string, string> = {
  setup: "Administrator created",
  "login.ok": "Signed in",
  "login.fail": "Failed sign-in",
  "login.lockout": "Account temporarily locked",
  "setup.lockout": "Setup locked after failed attempts",
  "password.change": "Password changed",
  "password.change.fail": "Password change refused",
  "password.reset": "Password reset",
  "user.create": "User created",
  "user.update": "User changed",
  "user.role": "Role changed",
  "user.delete": "User deleted",
  "session.revoke": "Session ended",
  "session.revokeAll": "All sessions ended",
  "instance.create": "Service added",
  "instance.update": "Service changed",
  "instance.delete": "Service deleted",
  "instance.import": "Service imported from the environment",
  "instance.test": "Connection tested",
  "feature.toggle": "Feature switched",
  "network.update": "Network settings changed",
  "converter.update": "Media and converter settings changed",
  "library.add": "Added to the library",
  "library.delete": "Removed from the library",
  "conversion.start": "Conversion started",
  "conversion.cancel": "Conversion cancelled",
  "audit.prune": "Old entries removed",
};

/** A readable name for an action; an unknown action is shown as it is. */
export function actionLabel(action: string): string {
  return LABELS[action] ?? action;
}

const KEY_LABELS: Record<string, string> = {
  wasEnabled: "was on",
  enabled: "on",
  cidrCount: "ranges",
  trustedProxyCount: "trusted proxies",
  cidrs: "ranges",
  trustedProxies: "trusted proxies",
  fields: "settings changed",
  secrets: "credentials replaced",
  provider: "type",
  userId: "user",
  environmentAgain: "environment applies again",
  ok: "worked",
  saved: "saved service",
  count: "count",
  deleted: "entries removed",
  cutoff: "older than",
  retentionDays: "kept for (days)",
};

function show(v: unknown): string {
  if (v === null || v === undefined) return "none";
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (Array.isArray(v)) return v.length === 0 ? "none" : v.map(show).join(", ");
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/** The audit entry's detail as short readable text ("type: radarr; on: yes"). */
export function describeDetail(detail: unknown): string {
  if (detail === null || detail === undefined || detail === "") return "";
  if (typeof detail !== "object" || Array.isArray(detail)) return show(detail);
  const parts = Object.entries(detail as Record<string, unknown>).map(
    ([k, v]) => `${KEY_LABELS[k] ?? k}: ${show(v)}`
  );
  return parts.join("; ");
}
