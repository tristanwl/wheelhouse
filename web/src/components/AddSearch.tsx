import { apiFetch } from "@/lib/http";
import { useState } from "react";
import { mutate as globalMutate } from "swr";
import { fetcher } from "@/lib/fetcher";
import type { SearchHit } from "@/lib/types";
import type { AddMeta } from "@/lib/types";
import { AddQualityModal } from "./AddQualityModal";

type Kind = "series" | "movies";

const MIN_AVAILABILITY_OPTIONS = [
  { value: "announced", label: "Announced" },
  { value: "inCinemas", label: "In cinemas" },
  { value: "released", label: "Released" },
];

export function AddSearch() {
  const [kind, setKind] = useState<Kind>("series");
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SearchHit[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);

  const [meta, setMeta] = useState<AddMeta | null>(null);
  const [monitored, setMonitored] = useState(true);
  const [searchNow, setSearchNow] = useState(true);
  const [minimumAvailability, setMinimumAvailability] = useState("released");

  const [pendingAdd, setPendingAdd] = useState<SearchHit | null>(null);
  const [adding, setAdding] = useState(false);
  const [addedKeys, setAddedKeys] = useState<Set<string>>(new Set());
  const [addError, setAddError] = useState<string | null>(null);

  // Results are tagged with the instance they came from; each instance has its
  // own quality profiles and root folders, so those load when you pick a hit.
  function chooseKind(next: Kind) {
    setKind(next);
    setResults(null);
    setSearchError(null);
    setAddError(null);
    setPendingAdd(null);
    setMeta(null);
  }

  function startAdd(hit: SearchHit) {
    setPendingAdd(hit);
    setAddError(null);
    setMeta(null);
    fetcher<AddMeta>(`/api/add-meta?instanceId=${encodeURIComponent(hit.instanceId)}`)
      .then(setMeta)
      .catch((err) => {
        setPendingAdd(null);
        setSearchError(err instanceof Error ? err.message : "Failed to load quality profiles.");
      });
  }

  async function runSearch() {
    if (!term.trim()) {
      setResults(null);
      return;
    }
    setSearching(true);
    setSearchError(null);
    try {
      const data = await fetcher<{ results?: SearchHit[]; error?: string }>(
        `/api/search?kind=${kind}&term=${encodeURIComponent(term)}`
      );
      if (data.error) throw new Error(data.error);
      setResults(data.results ?? []);
    } catch (err) {
      setSearchError(err instanceof Error ? err.message : "Search failed.");
    } finally {
      setSearching(false);
    }
  }

  async function confirmAdd(qualityProfileId: number) {
    if (!pendingAdd || !meta?.rootFolderPath) return;
    setAdding(true);
    setAddError(null);
    try {
      const res = await apiFetch("/api/add", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          instanceId: pendingAdd.instanceId,
          id: pendingAdd.id,
          qualityProfileId,
          rootFolderPath: meta.rootFolderPath,
          monitored,
          searchNow,
          ...(kind === "movies" ? { minimumAvailability } : {}),
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error ?? `add failed: ${res.status}`);
      setAddedKeys((prev) => new Set(prev).add(`${pendingAdd.instanceId}:${pendingAdd.id}`));
      globalMutate("/api/library");
      setPendingAdd(null);
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "Add failed.");
    } finally {
      setAdding(false);
    }
  }

  // Only name the instance when more than one gave results.
  const showInstance = new Set((results ?? []).map((h) => h.instanceId)).size > 1;

  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="flex rounded-md border border-slate-700 text-sm">
          <button
            onClick={() => chooseKind("series")}
            className={`rounded-l-md px-3 py-1.5 ${
              kind === "series" ? "bg-sky-900 text-sky-200" : "text-slate-400 hover:bg-slate-800"
            }`}
          >
            TV Show
          </button>
          <button
            onClick={() => chooseKind("movies")}
            className={`rounded-r-md px-3 py-1.5 ${
              kind === "movies"
                ? "bg-violet-900 text-violet-200"
                : "text-slate-400 hover:bg-slate-800"
            }`}
          >
            Movie
          </button>
        </div>

        <input
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && runSearch()}
          placeholder={`Search for a ${kind === "series" ? "TV show" : "movie"} to add…`}
          className="min-w-[240px] flex-1 rounded-md border border-slate-800 bg-slate-950 px-3 py-1.5 text-sm outline-hidden focus:border-slate-600"
        />
        <button
          onClick={runSearch}
          disabled={searching || !term.trim()}
          className="rounded-md bg-sky-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-sky-600 disabled:opacity-50"
        >
          {searching ? "Searching…" : "Search"}
        </button>

        {kind === "movies" && (
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
        )}

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

      {searchError && <p className="text-sm text-red-400">{searchError}</p>}

      {results && results.length === 0 && !searching && (
        <p className="text-sm text-slate-500">No results.</p>
      )}

      {results && results.length > 0 && (
        <div className="max-h-96 space-y-2 overflow-y-auto">
          {results.map((hit) => {
            const added = hit.alreadyAdded || addedKeys.has(`${hit.instanceId}:${hit.id}`);
            return (
              <div
                key={`${hit.instanceId}:${hit.id}`}
                className="flex gap-3 rounded-md border border-slate-800 bg-slate-950/50 p-2"
              >
                {hit.posterUrl ? (
                  <img
                    src={hit.posterUrl}
                    alt={hit.title}
                    className="h-24 w-16 shrink-0 rounded object-cover"
                  />
                ) : (
                  <div className="flex h-24 w-16 shrink-0 items-center justify-center rounded bg-slate-800 text-[10px] text-slate-600">
                    No poster
                  </div>
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">
                        {hit.title} <span className="text-slate-500">{hit.year}</span>
                      </p>
                      {(hit.subtitle || showInstance) && (
                        <p className="text-xs text-slate-500">
                          {[hit.subtitle, showInstance ? hit.instanceName : null]
                            .filter(Boolean)
                            .join(" · ")}
                        </p>
                      )}
                    </div>
                    {added ? (
                      <span className="shrink-0 rounded-full bg-emerald-900 px-2 py-0.5 text-xs text-emerald-300">
                        {hit.alreadyAdded ? "In library" : "Added ✓"}
                      </span>
                    ) : (
                      <button
                        onClick={() => startAdd(hit)}
                        disabled={pendingAdd !== null}
                        className="shrink-0 rounded-md bg-emerald-700 px-2.5 py-1 text-xs font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
                      >
                        Add
                      </button>
                    )}
                  </div>
                  <p className="mt-1 line-clamp-2 text-xs text-slate-400">{hit.overview}</p>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {pendingAdd && meta && (
        <AddQualityModal
          title={pendingAdd.title}
          posterUrl={pendingAdd.posterUrl}
          qualityProfiles={meta.qualityProfiles}
          busy={adding}
          error={addError}
          onConfirm={confirmAdd}
          onCancel={() => {
            setPendingAdd(null);
            setAddError(null);
          }}
        />
      )}
    </div>
  );
}
