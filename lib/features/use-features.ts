"use client";

import useSWR from "swr";
import { fetcher } from "@/lib/fetcher";
import type { FeatureId } from "./registry";
import type { FeaturesResponse } from "./server";

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
