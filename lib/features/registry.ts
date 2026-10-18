// Pure on purpose (no server-only imports, no zod): the nav and the stub import
// this from the browser bundle too.
import type { Role } from "../providers/core/types";

export const README_CONFIG_URL = "https://github.com/tristanwl/wheelhouse#configuration";

export type FeatureId = "dashboard" | "explore" | "library" | "codecs";

/** Something the server needs before a feature makes sense. */
export type Requirement =
  /** A ready instance with this role (for media managers, optionally of one kind). */
  | { role: Role; kind?: "movies" | "series" }
  /** A tool on the host. */
  | { tool: "ffmpeg" };

export interface FeatureDefinition {
  id: FeatureId;
  label: string;
  route: string;
  requires: Requirement[];
}

/** A configured instance, reduced to what gating needs. */
export interface FeatureInstance {
  role: Role;
  /** Media managers only. */
  kind?: "movies" | "series";
  /** Valid config and enabled, not merely present. */
  ready: boolean;
}

export interface FeatureEnvironment {
  ffmpeg: boolean;
}

export interface FeatureAvailability {
  feature: FeatureDefinition;
  available: boolean;
  /** The requirements that aren't met. */
  missing: Requirement[];
}

export const FEATURES: readonly FeatureDefinition[] = [
  { id: "dashboard", label: "Dashboard", route: "/", requires: [] },
  {
    id: "library",
    label: "Library",
    route: "/library",
    requires: [{ role: "mediaManager" }],
  },
  {
    id: "explore",
    label: "Explore",
    route: "/explore",
    requires: [{ role: "metadata" }, { role: "mediaManager", kind: "movies" }],
  },
  {
    id: "codecs",
    label: "Codecs",
    route: "/codecs",
    requires: [{ role: "mediaManager" }, { tool: "ffmpeg" }],
  },
];

function isMet(
  requirement: Requirement,
  instances: readonly FeatureInstance[],
  env: FeatureEnvironment
): boolean {
  if ("tool" in requirement) return env[requirement.tool];
  return instances.some(
    (i) =>
      i.ready && i.role === requirement.role && (!requirement.kind || i.kind === requirement.kind)
  );
}

export function getAvailableFeatures(
  instances: readonly FeatureInstance[],
  env: FeatureEnvironment
): FeatureAvailability[] {
  return FEATURES.map((feature) => {
    const missing = feature.requires.filter((r) => !isMet(r, instances, env));
    return { feature, available: missing.length === 0, missing };
  });
}

const ROLE_REASON: Record<Role, string> = {
  downloadClient: "a download client",
  mediaManager: "a Sonarr or Radarr instance",
  mediaServer: "a media server",
  metadata: "a TMDB API key",
};

/** What to tell the user when a requirement isn't met: "a Radarr instance", "ffmpeg". */
export function describeRequirement(requirement: Requirement): string {
  if ("tool" in requirement) return requirement.tool;
  if (requirement.role === "mediaManager" && requirement.kind) {
    return requirement.kind === "movies" ? "a Radarr instance" : "a Sonarr instance";
  }
  return ROLE_REASON[requirement.role];
}
