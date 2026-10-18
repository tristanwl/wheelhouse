import useSWR from "swr";
import { fetcher } from "./fetcher";
import type { FeatureId, FeaturesResponse } from "./types";

export const README_CONFIG_URL = "https://github.com/tristanwl/wheelhouse#configuration";

/** The server's feature list; `features` is undefined until it has loaded (or if it failed). */
export function useFeatures() {
  // Rarely changes (only with the configuration), so revalidating on focus is plenty.
  const { data, error } = useSWR<FeaturesResponse>("/api/features", fetcher);
  return { features: data?.features, error: error as Error | undefined };
}

/** `undefined` while unknown, so callers can tell "not yet" from "no". */
export function useFeatureAvailable(id: FeatureId): boolean | undefined {
  return useFeatures().features?.find((f) => f.id === id)?.available;
}

/** Dashboard is always there; the rest show once the server says they're available. */
export function visibleFeatures(features: FeaturesResponse["features"] | undefined) {
  return (features ?? []).filter((f) => f.id === "dashboard" || f.available);
}
