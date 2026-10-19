import useSWR, { useSWRConfig } from "swr";
import { fetcher } from "@/lib/fetcher";
import type { ProvidersResponse } from "@/lib/admin";

/** The provider registry with each provider's settings schema. It changes only with a new version. */
export function useProviders() {
  return useSWR<ProvidersResponse>("/api/admin/providers", fetcher, {
    revalidateOnFocus: false,
  });
}

/**
 * After a change to services or features, everything the rest of the app shows
 * from them (nav, dashboard, setup banner) is read again.
 */
export function useRefreshApp() {
  const { mutate } = useSWRConfig();
  return () =>
    Promise.all(
      [
        "/api/admin/instances",
        "/api/features",
        "/api/status",
        "/api/storage",
        "/api/downloads",
      ].map((key) => mutate(key))
    );
}
