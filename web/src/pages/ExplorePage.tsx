import { useEffect, useState } from "react";
import { fetcher } from "@/lib/fetcher";
import type { AddMeta } from "@/lib/types";
import { ExploreSection } from "@/components/ExploreSection";
import { TmdbAttribution } from "@/components/TmdbAttribution";

const MIN_AVAILABILITY_OPTIONS = [
  { value: "announced", label: "Announced" },
  { value: "inCinemas", label: "In cinemas" },
  { value: "released", label: "Released" },
];

export default function ExplorePage() {
  const [meta, setMeta] = useState<AddMeta | null>(null);
  const [metaError, setMetaError] = useState<string | null>(null);
  const [monitored, setMonitored] = useState(true);
  const [searchNow, setSearchNow] = useState(true);
  const [minimumAvailability, setMinimumAvailability] = useState("released");

  useEffect(() => {
    fetcher<AddMeta>("/api/add-meta?kind=movies")
      .then(setMeta)
      .catch((err) =>
        setMetaError(err instanceof Error ? err.message : "Failed to load quality profiles.")
      );
  }, []);

  return (
    <div className="space-y-8">
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-slate-800 bg-slate-900 p-3">
        <select
          value={minimumAvailability}
          onChange={(e) => setMinimumAvailability(e.target.value)}
          className="rounded-md border border-slate-800 bg-slate-950 px-2 py-1.5 text-sm outline-hidden focus:border-slate-600"
        >
          {MIN_AVAILABILITY_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1.5 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={monitored}
            onChange={(e) => setMonitored(e.target.checked)}
          />
          Monitored
        </label>
        <label className="flex items-center gap-1.5 text-sm text-slate-300">
          <input
            type="checkbox"
            checked={searchNow}
            onChange={(e) => setSearchNow(e.target.checked)}
          />
          Search now
        </label>
      </div>

      {metaError && <p className="text-sm text-red-400">{metaError}</p>}

      <ExploreSection
        category="theaters"
        label="In Theaters"
        instanceId={meta?.instanceId ?? null}
        qualityProfiles={meta?.qualityProfiles ?? []}
        rootFolderPath={meta?.rootFolderPath ?? null}
        monitored={monitored}
        searchNow={searchNow}
        minimumAvailability={minimumAvailability}
      />
      <ExploreSection
        category="bluray"
        label="Recent Blu-ray / Digital Releases"
        instanceId={meta?.instanceId ?? null}
        qualityProfiles={meta?.qualityProfiles ?? []}
        rootFolderPath={meta?.rootFolderPath ?? null}
        monitored={monitored}
        searchNow={searchNow}
        minimumAvailability={minimumAvailability}
      />
      <ExploreSection
        category="popular"
        label="Popular"
        instanceId={meta?.instanceId ?? null}
        qualityProfiles={meta?.qualityProfiles ?? []}
        rootFolderPath={meta?.rootFolderPath ?? null}
        monitored={monitored}
        searchNow={searchNow}
        minimumAvailability={minimumAvailability}
      />
      <TmdbAttribution />
    </div>
  );
}
