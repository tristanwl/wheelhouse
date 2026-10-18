import { vi } from "vitest";
import {
  GOOD_KEY,
  startFakeRadarr,
  startFakeSonarr,
  type FakeRadarr,
  type FakeSonarr,
} from "./server";

const ARR_VARIABLES = [
  "SONARR_URL",
  "SONARR_API_KEY",
  "RADARR_URL",
  "RADARR_API_KEY",
  "RADARR_4K_URL",
  "RADARR_4K_API_KEY",
];

/** Blanks every Sonarr/Radarr variable, so a test starts from "nothing configured". */
export function clearArrEnv() {
  for (const name of ARR_VARIABLES) vi.stubEnv(name, "");
}

/**
 * Fake Sonarr/Radarr servers wired into the environment the way a user would
 * configure them, for tests that call real route handlers. The key put in the
 * environment is separate from the one the server accepts, so a test can
 * configure a wrong key (pass it explicitly: `undefined` would pick the good one).
 */
export function createArrStack() {
  const servers: { close: () => Promise<void> }[] = [];
  clearArrEnv();

  async function track<T extends { close: () => Promise<void> }>(server: T) {
    servers.push(server);
    return server;
  }

  return {
    async radarr(
      options: Parameters<typeof startFakeRadarr>[0] = {},
      envKey: string = GOOD_KEY
    ): Promise<FakeRadarr> {
      const server = await track(await startFakeRadarr(options));
      vi.stubEnv("RADARR_URL", server.url);
      vi.stubEnv("RADARR_API_KEY", envKey);
      return server;
    },
    /** The second Radarr: RADARR_4K_URL / RADARR_4K_API_KEY, instance id "radarr-4k". */
    async radarr4k(
      options: Parameters<typeof startFakeRadarr>[0] = {},
      envKey: string = GOOD_KEY
    ): Promise<FakeRadarr> {
      const server = await track(await startFakeRadarr(options));
      vi.stubEnv("RADARR_4K_URL", server.url);
      vi.stubEnv("RADARR_4K_API_KEY", envKey);
      return server;
    },
    async sonarr(
      options: Parameters<typeof startFakeSonarr>[0] = {},
      envKey: string = GOOD_KEY
    ): Promise<FakeSonarr> {
      const server = await track(await startFakeSonarr(options));
      vi.stubEnv("SONARR_URL", server.url);
      vi.stubEnv("SONARR_API_KEY", envKey);
      return server;
    },
    async stop() {
      vi.unstubAllEnvs();
      await Promise.all(servers.splice(0).map((s) => s.close()));
    },
  };
}
