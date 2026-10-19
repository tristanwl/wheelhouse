import { useState } from "react";
import useSWR from "swr";
import { BTN_PRIMARY, CARD, errorText } from "@/components/admin/styles";
import { ErrorNote, LiveResult, PageTitle, StringListEditor, Switch } from "@/components/admin/ui";
import { validateMediaRoot, type ConverterSettings } from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { ApiError, apiJson, jsonInit } from "@/lib/http";

function ConverterForm({
  settings,
  onSaved,
}: {
  settings: ConverterSettings;
  onSaved: (s: ConverterSettings) => void;
}) {
  const [roots, setRoots] = useState(settings.mediaRoots);
  const [includeHdr, setIncludeHdr] = useState(settings.includeHdr);
  const [showErrors, setShowErrors] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setSaved(null);
    setError(null);
    setShowErrors(true);
    if (roots.some((r) => validateMediaRoot(r))) {
      setError("Fix the folders marked below, then save again.");
      return;
    }
    setBusy(true);
    try {
      const next = await apiJson<ConverterSettings>(
        "/api/admin/converter",
        jsonInit("PUT", { mediaRoots: roots.map((r) => r.trim()), includeHdr })
      );
      setRoots(next.mediaRoots ?? []);
      setIncludeHdr(next.includeHdr === true);
      setShowErrors(false);
      onSaved(next);
      setSaved("Saved.");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void save(e)} noValidate className="space-y-5">
      <section className={`${CARD} space-y-4 p-4`}>
        <StringListEditor
          label="Media folders"
          hint="The folders on the server where your movies and series live, as the server sees them (inside a container, the path inside the container). The codec page only reads and converts files below these folders."
          values={roots}
          onChange={setRoots}
          validate={validateMediaRoot}
          placeholder="/movies"
          addLabel="Add a folder"
          showErrors={showErrors}
          disabled={busy}
        />
        <p className="text-sm text-slate-400" data-testid="effective-roots">
          {settings.effectiveRoots && settings.effectiveRoots.length > 0
            ? `In use now: ${settings.effectiveRoots.join(", ")}.`
            : "No media folder is in use yet, so conversion cannot start."}
        </p>
      </section>
      <section className={`${CARD} space-y-3 p-4`}>
        <Switch
          label="Include HDR and Dolby Vision files"
          checked={includeHdr}
          onChange={setIncludeHdr}
          disabled={busy}
          description="Off by default. Converting an HDR file to another codec can wash out its colours, so the codec page leaves HDR files alone unless you turn this on."
        />
      </section>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex items-center gap-3">
        <button type="submit" disabled={busy} className={BTN_PRIMARY}>
          {busy ? "Saving…" : "Save"}
        </button>
        <LiveResult>{saved}</LiveResult>
      </div>
    </form>
  );
}

export default function ConverterPage() {
  const { data, error, mutate } = useSWR<ConverterSettings>("/api/admin/converter", fetcher, {
    shouldRetryOnError: false,
  });
  const missing = error instanceof ApiError && error.status === 404;
  return (
    <div className="space-y-4">
      <PageTitle title="Media and converter">
        <p>
          Where your media lives and what the codec converter may touch. Converting keeps a backup
          of every original file next to it.
        </p>
      </PageTitle>
      {missing && (
        <p className={`${CARD} p-4 text-sm text-slate-300`} role="status">
          These settings are not available in this version of Wheelhouse. The media folders are set
          by the server&apos;s environment settings for now.
        </p>
      )}
      {error && !missing && (
        <ErrorNote>Could not load these settings. {errorText(error, "")}</ErrorNote>
      )}
      {data && (
        <ConverterForm
          settings={{
            mediaRoots: data.mediaRoots ?? [],
            includeHdr: data.includeHdr === true,
            effectiveRoots: data.effectiveRoots ?? [],
          }}
          onSaved={(next) => void mutate(next, { revalidate: true })}
        />
      )}
    </div>
  );
}
