import { getRuntime } from "@/lib/providers";
import { hasFfmpeg } from "./ffmpeg";
import {
  describeRequirement,
  getAvailableFeatures,
  type FeatureId,
  type FeatureInstance,
} from "./registry";

/** What /api/features returns: nothing about hosts, keys or instance names. */
export interface FeaturesResponse {
  features: {
    id: FeatureId;
    label: string;
    route: string;
    available: boolean;
    /** Human-readable reasons, e.g. "a Radarr instance". */
    missing: string[];
  }[];
}

export async function getFeatures(): Promise<FeaturesResponse> {
  const resolved = await getRuntime().listInstances();
  const instances: FeatureInstance[] = [];
  for (const { definition, status } of resolved) {
    // Unknown providers have no role; they can't satisfy anything.
    if (!definition) continue;
    instances.push({
      role: definition.role,
      kind: definition.role === "mediaManager" ? definition.capabilities.kind : undefined,
      ready: status === "ready",
    });
  }
  const availability = getAvailableFeatures(instances, { ffmpeg: await hasFfmpeg() });
  return {
    features: availability.map(({ feature, available, missing }) => ({
      id: feature.id,
      label: feature.label,
      route: feature.route,
      available,
      missing: missing.map(describeRequirement),
    })),
  };
}
