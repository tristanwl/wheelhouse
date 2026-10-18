import { env } from "./env";

interface PlexIdentity {
  claimed: boolean;
  version: string;
  machineIdentifier: string;
}

interface PlexServerInfo {
  friendlyName: string;
  version: string;
}

interface PlexSessions {
  size: number;
}

async function plexFetch<T>(path: string, useToken: boolean): Promise<T> {
  const baseUrl = env.plexUrl;
  if (!baseUrl) {
    throw new Error("PLEX_URL not configured");
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  if (useToken) {
    const token = env.plexToken;
    if (!token) {
      throw new Error("PLEX_TOKEN not configured");
    }
    headers["X-Plex-Token"] = token;
  }
  const res = await fetch(`${baseUrl}${path}`, {
    headers,
    cache: "no-store",
  });
  if (!res.ok) {
    throw new Error(`plex ${path} failed: ${res.status} ${res.statusText}`);
  }
  const data = await res.json();
  return data.MediaContainer as T;
}

// Unauthenticated — works even before the server is claimed.
export function getPlexIdentity() {
  return plexFetch<PlexIdentity>("/identity", false);
}

// Everything below requires PLEX_TOKEN.
export function getPlexServerInfo() {
  return plexFetch<PlexServerInfo>("/", true);
}

export function getPlexSessions() {
  return plexFetch<PlexSessions>("/status/sessions", true);
}
