import { useCallback, useEffect, useState } from "react";
import { fetcher } from "./fetcher";
import type { AddMeta, InstanceRef } from "./types";

/**
 * What the add dialogs need (quality profiles, root folder) for the instance
 * the user is adding to. It starts with the server's default for the kind (the
 * first instance that answers) and the server lists every ready instance, so a
 * picker can offer the others; choosing one loads that instance's profiles.
 */
export function useAddMeta(kind: "movies" | "series" = "movies") {
  const [meta, setMeta] = useState<AddMeta | null>(null);
  const [instances, setInstances] = useState<InstanceRef[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    fetcher<AddMeta>(`/api/add-meta?kind=${kind}`)
      .then((m) => {
        if (!live) return;
        setMeta(m);
        setSelectedId(m.instanceId);
        setInstances(m.instances ?? []);
      })
      .catch((err) => {
        if (live) setError(err instanceof Error ? err.message : "Failed to load options.");
      });
    return () => {
      live = false;
    };
  }, [kind]);

  const choose = useCallback((instanceId: string) => {
    setError(null);
    setMeta(null);
    setSelectedId(instanceId);
    fetcher<AddMeta>(`/api/add-meta?instanceId=${encodeURIComponent(instanceId)}`)
      .then(setMeta)
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load options."));
  }, []);

  return { meta, instances, selectedId, error, choose };
}
