/**
 * What the administration pages know about the server's admin API
 * (`/api/admin/*`, `/api/users`, `/api/audit`), and the pure helpers behind the
 * forms: building a form from a provider's JSON Schema, checking values the way
 * the server will, and the instance id rules. Nothing here touches the network.
 */

// ---- services ----

export type ProviderRole = "mediaManager" | "downloadClient" | "mediaServer" | "metadata";

export const ROLE_HEADINGS: Record<ProviderRole, string> = {
  mediaManager: "Media managers",
  downloadClient: "Download clients",
  mediaServer: "Media servers",
  metadata: "Movie information",
};

export const ROLE_ORDER: ProviderRole[] = [
  "mediaManager",
  "downloadClient",
  "mediaServer",
  "metadata",
];

export interface SchemaProperty {
  type: "string" | "boolean" | "integer";
  title?: string;
  format?: string;
  pattern?: string;
  minLength?: number;
  writeOnly?: boolean;
  "x-secret"?: boolean;
}

export interface ConfigSchema {
  title?: string;
  type?: string;
  properties: Record<string, SchemaProperty>;
  required?: string[];
  "x-order"?: string[];
}

/** One entry of `GET /api/admin/providers`. */
export interface Provider {
  id: string;
  role: ProviderRole;
  displayName: string;
  configSchema: ConfigSchema;
  secretKeys?: string[];
}

export interface ProvidersResponse {
  providers: Provider[];
}

export type InstanceStatus = "ready" | "disabled" | "unknown-provider" | "invalid-config";

export interface TestResult {
  ok: boolean;
  version?: string;
  error?: string;
  durationMs?: number;
}

/** One entry of `GET /api/admin/instances`. Secrets are only ever reported as set or not. */
export interface ServiceInstance {
  id: string;
  providerId: string;
  role: ProviderRole | null;
  name: string;
  enabled: boolean;
  source: "database" | "environment";
  readOnly: boolean;
  overridesEnvironment?: boolean;
  config: Record<string, unknown>;
  secrets: Record<string, { set: boolean }>;
  status: InstanceStatus;
  error?: string;
  lastTest?: { ok: boolean; at: string; error?: string };
}

export interface InstancesResponse {
  instances: ServiceInstance[];
}

export const STATUS_TEXT: Record<InstanceStatus, string> = {
  ready: "Ready",
  disabled: "Switched off",
  "invalid-config": "Settings incomplete or invalid",
  "unknown-provider": "Unknown type of service",
};

// ---- forms generated from a provider's JSON Schema ----

export type FieldKind = "text" | "url" | "secret" | "boolean" | "integer";

export interface FormField {
  name: string;
  label: string;
  kind: FieldKind;
  required: boolean;
}

/** The form fields of a schema, in the order the provider declares them. */
export function fieldsOf(schema: ConfigSchema): FormField[] {
  const props = schema.properties ?? {};
  const known = Object.keys(props);
  const order = (schema["x-order"] ?? []).filter((n) => n in props);
  const names = [...order, ...known.filter((n) => !order.includes(n))];
  const required = new Set(schema.required ?? []);
  return names.map((name) => {
    const p = props[name];
    let kind: FieldKind = "text";
    if (p["x-secret"] || p.writeOnly) kind = "secret";
    else if (p.type === "boolean") kind = "boolean";
    else if (p.type === "integer") kind = "integer";
    else if (p.format === "uri" || p.format === "url") kind = "url";
    return { name, label: p.title || name, kind, required: required.has(name) };
  });
}

export type FormValue = string | boolean;
export type FormValues = Record<string, FormValue>;

/** Empty values for a new service. */
export function emptyValues(fields: FormField[]): FormValues {
  return Object.fromEntries(fields.map((f) => [f.name, f.kind === "boolean" ? false : ""]));
}

/** The values of a saved service. Secrets are never included: the server does not send them. */
export function valuesOf(fields: FormField[], config: Record<string, unknown>): FormValues {
  const out = emptyValues(fields);
  for (const f of fields) {
    const v = config[f.name];
    if (f.kind === "secret" || v === undefined || v === null) continue;
    out[f.name] = f.kind === "boolean" ? v === true : String(v);
  }
  return out;
}

/** What to do with a secret on a saved service: keep the stored one or send a new one. */
export interface SecretState {
  /** The server holds a value for it. */
  stored: boolean;
  /** The administrator chose to replace it. */
  replacing: boolean;
}

/**
 * Checks one field the way the server will (the server has the final say and its
 * messages are shown too). Returns the message, or undefined when it is fine.
 */
export function validateField(
  field: FormField,
  value: FormValue | undefined,
  secret?: SecretState
): string | undefined {
  if (field.kind === "boolean") return undefined;
  const text = typeof value === "string" ? value.trim() : "";
  if (field.kind === "secret" && secret?.stored && !secret.replacing) return undefined;
  if (text === "") return field.required ? `${field.label} is required.` : undefined;
  if (field.kind === "url") {
    if (!/^https?:\/\//i.test(text)) {
      return `${field.label} must be a full address starting with http:// or https://.`;
    }
    let parsed: URL;
    try {
      parsed = new URL(text);
    } catch {
      return `${field.label} is not a valid address.`;
    }
    if (parsed.username || parsed.password) {
      return `${field.label} must not contain a username or password; enter those in their own fields.`;
    }
  }
  if (field.kind === "integer" && !/^-?\d+$/.test(text)) {
    return `${field.label} must be a whole number.`;
  }
  return undefined;
}

export function validateAll(
  fields: FormField[],
  values: FormValues,
  secrets: Record<string, SecretState>
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of fields) {
    const msg = validateField(f, values[f.name], secrets[f.name]);
    if (msg) out[f.name] = msg;
  }
  return out;
}

/**
 * The `config` object to send. An empty optional setting is left out (which
 * clears it on an update); a secret is sent only when it was typed, so leaving it
 * out keeps the stored one.
 */
export function buildConfig(
  fields: FormField[],
  values: FormValues,
  secrets: Record<string, SecretState> = {}
): Record<string, string | number | boolean> {
  const out: Record<string, string | number | boolean> = {};
  for (const f of fields) {
    const v = values[f.name];
    if (f.kind === "boolean") {
      out[f.name] = v === true;
      continue;
    }
    const text = typeof v === "string" ? v.trim() : "";
    if (f.kind === "secret") {
      const s = secrets[f.name];
      // A new service sends what was typed; a saved one only when replaced.
      const send = s ? s.replacing : true;
      if (send && typeof v === "string" && v !== "") out[f.name] = v;
      continue;
    }
    if (text === "") continue;
    out[f.name] = f.kind === "integer" ? Number.parseInt(text, 10) : text;
  }
  return out;
}

// ---- instance ids and names (mirrors server/internal/instances) ----

const ID_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** The rules for an id typed by hand; undefined when it is fine. */
export function validateInstanceId(providerId: string, id: string): string | undefined {
  if (id === "") return undefined;
  if (id.length > 64 || !ID_PATTERN.test(id)) {
    return "The ID may use lowercase letters, digits and single hyphens, at most 64 characters.";
  }
  if (id !== providerId && !id.startsWith(`${providerId}-`)) {
    return `The ID must be "${providerId}" or start with "${providerId}-" (for example "${providerId}-4k").`;
  }
  return undefined;
}

/**
 * The id the server will pick when none is given: the provider's id if it is
 * free, otherwise the provider's id and the name. undefined when both are taken.
 */
export function deriveInstanceId(
  providerId: string,
  name: string,
  taken: ReadonlySet<string>
): string | undefined {
  const slug = slugify(name);
  for (const candidate of [providerId, slug ? `${providerId}-${slug}` : ""]) {
    if (candidate && !taken.has(candidate)) return candidate;
  }
  return undefined;
}

// ---- features ----

export interface AdminFeature {
  id: string;
  label: string;
  route: string;
  enabled: boolean;
  canDisable: boolean;
}

// ---- users, sessions, audit ----

export interface AdminUser {
  id: string;
  username: string;
  role: "admin" | "user";
  disabled: boolean;
  mustChangePassword: boolean;
  createdAt: string;
  passwordChangedAt: string;
}

export interface AdminSession {
  id: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  ip: string;
  userAgent: string;
  current: boolean;
}

export interface AuditEntry {
  id: number;
  at: string;
  actorId: string | null;
  actor: string;
  action: string;
  target: string;
  address: string;
  detail?: unknown;
}

export interface AuditResponse {
  entries: AuditEntry[];
  nextBefore: number | null;
}

export function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** "3 minutes ago" style text for a past time; falls back to the date. */
export function ago(iso: string, now: number = Date.now()): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m} minute${m === 1 ? "" : "s"} ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} hour${h === 1 ? "" : "s"} ago`;
  return when(iso);
}

// ---- network ----

export interface NetworkSettings {
  enabled: boolean;
  cidrs: string[];
  trustedProxies: string[];
  envTrustedProxies?: string[];
  /** Stored host names allowed to use the bypass (besides IPs, localhost, single-label names). */
  allowedHosts?: string[];
  /** The same from WHEELHOUSE_ALLOWED_HOSTS: read-only. */
  envAllowedHosts?: string[];
  /** Set on a save that turned the bypass on: the Host of that request, added to the list. */
  addedHost?: string;
  warnings: string[];
  client?: {
    address: string;
    forwarded?: boolean;
    inRange: boolean;
    host?: string;
    hostAllowed?: boolean;
  };
}

/**
 * A quick check of an allowed host name before it is sent (the server decides).
 * Names need at least two labels; `*.example.lan` allows everything below a name.
 * undefined when it looks right.
 */
export function validateHostName(text: string): string | undefined {
  const t = text.trim().toLowerCase().replace(/\.$/, "");
  if (t === "") return "Enter a host name such as nas.example.lan.";
  if (t === "*" || /^\*\.[^.]*$/.test(t))
    return "A wildcard must be followed by a full name, such as *.example.lan.";
  const base = t.startsWith("*.") ? t.slice(2) : t;
  if (base.includes("*"))
    return "A wildcard is only allowed as the first part, as in *.example.lan.";
  if (/^[0-9.:]+$/.test(base) || base.includes(":"))
    return "IP addresses are always allowed; list host names only.";
  if (!base.includes("."))
    return "Names without a dot (such as nas) and localhost are always allowed.";
  if (
    base.length > 253 ||
    !base.split(".").every((l) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(l))
  )
    return "Use letters, digits and hyphens in dot-separated parts, with no port or path.";
  return undefined;
}

/**
 * A quick check of an address or range before it is sent (the server parses it
 * properly and its message is shown too). undefined when it looks right.
 */
export function validateRange(text: string): string | undefined {
  const t = text.trim();
  if (t === "") return "Enter an address or a range such as 192.168.1.0/24.";
  const [addr, bits, extra] = t.split("/");
  if (extra !== undefined) return "That is not an address or range.";
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(addr);
  let max: number;
  if (v4) {
    if (v4.slice(1).some((o) => Number(o) > 255))
      return "Each part of an IPv4 address is 0 to 255.";
    max = 32;
  } else if (addr.includes(":") && /^[0-9a-fA-F:.]+$/.test(addr) && addr.length >= 2) {
    max = 128;
  } else {
    return "That is not an address or range. Use for example 192.168.1.0/24 or fd00::/8.";
  }
  if (bits !== undefined) {
    if (!/^\d{1,3}$/.test(bits) || Number(bits) > max) {
      return `The number after the slash must be 0 to ${max}.`;
    }
    if (Number(bits) === 0) return "A range of /0 would match every address and is refused.";
  }
  return undefined;
}

/** A media root is an absolute path on the server. */
export function validateMediaRoot(text: string): string | undefined {
  const t = text.trim();
  if (t === "") return "Enter a folder path such as /movies.";
  if (!t.startsWith("/")) return "Use the full path, starting with a slash, such as /movies.";
  if (t.includes("\0")) return "That path contains characters that are not allowed.";
  return undefined;
}

export interface ConverterSettings {
  mediaRoots: string[];
  includeHdr: boolean;
  effectiveRoots: string[];
  /** Where the roots in use come from. */
  source?: "database" | "environment" | "default";
}
