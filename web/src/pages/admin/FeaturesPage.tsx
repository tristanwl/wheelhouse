import { useState } from "react";
import useSWR from "swr";
import { useRefreshApp } from "@/components/admin/hooks";
import { CARD, errorText } from "@/components/admin/styles";
import { ErrorNote, LiveResult, PageTitle, Switch } from "@/components/admin/ui";
import type { AdminFeature } from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { apiJson, jsonInit } from "@/lib/http";

const DESCRIPTIONS: Record<string, string> = {
  dashboard: "Service status, storage and downloads. It is always on.",
  library: "Browse, add and remove movies and series.",
  explore: "Discover movies from TMDB and add them to a Radarr.",
  codecs: "Find files in codecs that are hard to play and convert them.",
};

export default function FeaturesPage() {
  const { data, error, mutate } = useSWR<{ features: AdminFeature[] }>(
    "/api/admin/features",
    fetcher
  );
  const refresh = useRefreshApp();
  const [busy, setBusy] = useState<string | null>(null);
  const [result, setResult] = useState<{ text: string; error?: boolean } | null>(null);

  async function toggle(f: AdminFeature, on: boolean) {
    setBusy(f.id);
    setResult(null);
    try {
      const next = await apiJson<{ features: AdminFeature[] }>(
        "/api/admin/features",
        jsonInit("PUT", { features: { [f.id]: on } })
      );
      await mutate(next, { revalidate: false });
      await refresh();
      setResult({ text: `${f.label} is now ${on ? "on" : "off"} for everyone.` });
    } catch (err) {
      setResult({ text: errorText(err), error: true });
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-4">
      <PageTitle title="Features">
        <p>
          Switch off the parts of Wheelhouse you do not want people to use. A feature that is
          switched off is hidden from the menu, its page says it is <em>disabled by admin</em>, and
          the server refuses its requests, even if the services it needs are set up. Switching it on
          again restores it at once.
        </p>
        <p>
          A feature that is on still needs its services: Library needs a Radarr or Sonarr, Explore
          needs TMDB and a Radarr, Codecs needs ffmpeg and a Radarr or Sonarr. The dashboard shows
          what is missing.
        </p>
      </PageTitle>
      {error && <ErrorNote>Could not load the features. {errorText(error, "")}</ErrorNote>}
      {result?.error ? (
        <ErrorNote>{result.text}</ErrorNote>
      ) : (
        <LiveResult>{result?.text}</LiveResult>
      )}
      <ul className={`${CARD} divide-y divide-slate-800`}>
        {data?.features.map((f) => (
          <li key={f.id} className="p-4">
            <Switch
              label={f.label}
              checked={f.enabled}
              disabled={!f.canDisable || busy !== null}
              onChange={(on) => void toggle(f, on)}
              description={
                <>
                  {DESCRIPTIONS[f.id] ?? ""}
                  {!f.canDisable && " Locked on: the dashboard is how you find out what is wrong."}
                </>
              }
            />
          </li>
        ))}
      </ul>
    </div>
  );
}
