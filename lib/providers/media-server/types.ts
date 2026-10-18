import type { ConnectionResult } from "../core/types";

export interface MediaServerStatus {
  /**
   * "needs-setup" means the server itself isn't ready to use (an unclaimed
   * Plex, say). A server that is fine but that Wheelhouse can't fully read
   * (no or rejected credentials) is "ok" with `authenticated: false`.
   */
  state: "ok" | "needs-setup";
  /** Something the user should know or do, in plain words. Never mentions configuration key names. */
  notice?: string;
  name?: string;
  version?: string;
  /** Sessions actually playing something (not merely connected clients). undefined when the server can't say (no credentials, or they were rejected). */
  activeSessions?: number;
  /** Whether Wheelhouse is authenticated to the server (so session data is available). */
  authenticated: boolean;
}

/** Static, per provider. */
export interface MediaServerCapabilities {
  sessions: boolean;
}

export interface MediaServer {
  testConnection(): Promise<ConnectionResult>;
  /** Throws a ProviderError when the server can't be reached at all. */
  getStatus(): Promise<MediaServerStatus>;
}
