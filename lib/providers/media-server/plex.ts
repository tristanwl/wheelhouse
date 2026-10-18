import { z } from "zod";
import { createHttpClient, ProviderError, redact } from "../core/http";
import { defineProvider } from "../core/registry";
import type { MediaServer, MediaServerCapabilities, MediaServerStatus } from "./types";

const configSchema = z.object({
  url: z.url(),
  // Optional: a server that hasn't been claimed yet has no token to give.
  token: z.string().min(1).optional(),
});

type PlexConfig = z.infer<typeof configSchema>;

interface PlexIdentity {
  claimed: boolean;
  version?: string;
}

interface PlexServerInfo {
  friendlyName?: string;
  version?: string;
}

// Plex wraps every JSON response in { MediaContainer: ... }.
interface Container<T> {
  MediaContainer: T;
}

// Plex answers a missing or wrong token with HTTP 401 and an HTML page.
function isAuthError(err: unknown): boolean {
  return err instanceof ProviderError && (err.status === 401 || err.status === 403);
}

function createClient(config: PlexConfig, label: string): MediaServer {
  const http = createHttpClient({
    baseUrl: config.url,
    label,
    headers: { Accept: "application/json" },
    secrets: config.token ? [config.token] : [],
  });

  const authed = config.token ? { headers: { "X-Plex-Token": config.token } } : undefined;

  // /identity needs no token and works before the server is claimed.
  async function identity(): Promise<PlexIdentity> {
    return (await http.json<Container<PlexIdentity>>("/identity")).MediaContainer;
  }

  return {
    async testConnection() {
      try {
        const { version } = await identity();
        if (authed) {
          try {
            await http.json("/", authed);
          } catch (err) {
            if (isAuthError(err)) return { ok: false, version, error: `${label}: token rejected` };
            throw err;
          }
        }
        return { ok: true, version };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },

    async getStatus(): Promise<MediaServerStatus> {
      const { claimed, version } = await identity();

      if (!claimed) {
        return {
          state: "needs-setup",
          notice: "Not claimed yet. Finish setting up Plex in its web interface.",
          version,
          authenticated: false,
        };
      }
      if (!authed) {
        return {
          state: "ok",
          notice: "No Plex token is configured, so only basic status is shown.",
          version,
          authenticated: false,
        };
      }

      try {
        const [info, sessions] = await Promise.all([
          http.json<Container<PlexServerInfo>>("/", authed),
          http.json<Container<{ size: number }>>("/status/sessions", authed),
        ]);
        return {
          state: "ok",
          name: info.MediaContainer.friendlyName,
          version: info.MediaContainer.version ?? version,
          activeSessions: sessions.MediaContainer.size,
          authenticated: true,
        };
      } catch (err) {
        // The server is up (identity answered), so don't report it as down.
        const notice = isAuthError(err)
          ? "The Plex token was rejected, so only basic status is shown."
          : `Couldn't read Plex details (${err instanceof Error ? redact(err.message) : "unknown error"}).`;
        return { state: "ok", notice, version, authenticated: false };
      }
    },
  };
}

export const plexProvider = defineProvider<PlexConfig, MediaServer, MediaServerCapabilities>({
  id: "plex",
  role: "mediaServer",
  displayName: "Plex",
  configSchema,
  secretKeys: ["token"],
  capabilities: { sessions: true },
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
