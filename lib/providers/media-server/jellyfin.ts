import { z } from "zod";
import { createHttpClient, ProviderError, redact } from "../core/http";
import { defineProvider } from "../core/registry";
import type { MediaServer, MediaServerCapabilities, MediaServerStatus } from "./types";

const configSchema = z.object({
  url: z.url(),
  // Optional: without a key only the public status is available.
  apiKey: z.string().min(1).optional(),
});

type JellyfinConfig = z.infer<typeof configSchema>;

interface JellyfinPublicInfo {
  ServerName?: string;
  Version?: string;
  StartupWizardCompleted?: boolean;
}

interface JellyfinInfo {
  ServerName?: string;
  Version?: string;
}

interface JellyfinSession {
  NowPlayingItem?: unknown;
}

// Jellyfin answers a missing or wrong key with HTTP 401 and an empty body.
function isAuthError(err: unknown): boolean {
  return err instanceof ProviderError && (err.status === 401 || err.status === 403);
}

function createClient(config: JellyfinConfig, label: string): MediaServer {
  const http = createHttpClient({
    baseUrl: config.url,
    label,
    headers: { Accept: "application/json" },
    secrets: config.apiKey ? [config.apiKey] : [],
  });

  // Jellyfin 12 only accepts the key in the Authorization header (the older
  // X-Emby-Token header gets a 401); this form also works on 10.x.
  const authed = config.apiKey
    ? { headers: { Authorization: `MediaBrowser Token="${config.apiKey}"` } }
    : undefined;

  // Needs no key and works before the setup wizard has run.
  function publicInfo(): Promise<JellyfinPublicInfo> {
    return http.json<JellyfinPublicInfo>("/System/Info/Public");
  }

  return {
    async testConnection() {
      try {
        const { Version: version } = await publicInfo();
        if (authed) {
          try {
            await http.json("/System/Info", authed);
          } catch (err) {
            if (isAuthError(err))
              return { ok: false, version, error: `${label}: API key rejected` };
            throw err;
          }
        }
        return { ok: true, version };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : "unknown error" };
      }
    },

    async getStatus(): Promise<MediaServerStatus> {
      const pub = await publicInfo();
      const version = pub.Version;

      if (pub.StartupWizardCompleted === false) {
        return {
          state: "needs-setup",
          notice: "Setup isn't finished yet. Finish setting up Jellyfin in its web interface.",
          name: pub.ServerName,
          version,
          authenticated: false,
        };
      }
      if (!authed) {
        return {
          state: "ok",
          notice: "No Jellyfin API key is configured, so only basic status is shown.",
          name: pub.ServerName,
          version,
          authenticated: false,
        };
      }

      try {
        const [info, sessions] = await Promise.all([
          http.json<JellyfinInfo>("/System/Info", authed),
          http.json<JellyfinSession[]>("/Sessions", authed),
        ]);
        return {
          state: "ok",
          name: info.ServerName ?? pub.ServerName,
          version: info.Version ?? version,
          // Only sessions playing something count; Jellyfin also lists every
          // connected client (an open web page, say) as a session.
          activeSessions: sessions.filter((s) => s.NowPlayingItem != null).length,
          authenticated: true,
        };
      } catch (err) {
        // The server is up (the public endpoint answered), so don't report it as down.
        const notice = isAuthError(err)
          ? "The Jellyfin API key was rejected, so only basic status is shown."
          : `Couldn't read Jellyfin details (${err instanceof Error ? redact(err.message) : "unknown error"}).`;
        return { state: "ok", notice, name: pub.ServerName, version, authenticated: false };
      }
    },
  };
}

export const jellyfinProvider = defineProvider<
  JellyfinConfig,
  MediaServer,
  MediaServerCapabilities
>({
  id: "jellyfin",
  role: "mediaServer",
  displayName: "Jellyfin",
  configSchema,
  secretKeys: ["apiKey"],
  capabilities: { sessions: true },
  create: (config, ctx) => createClient(config, ctx.instanceId),
});
