import { useId, useMemo, useState } from "react";
import useSWR from "swr";
import { BTN, CARD, INPUT, errorText } from "@/components/admin/styles";
import { ErrorNote, LiveResult, PageTitle } from "@/components/admin/ui";
import { when, type AuditEntry, type AuditResponse } from "@/lib/admin";
import { actionLabel, describeDetail } from "@/lib/audit";
import { fetcher } from "@/lib/fetcher";
import { apiJson } from "@/lib/http";

const PAGE = 50;

export default function AuditPage() {
  const { data, error } = useSWR<AuditResponse>(`/api/audit?limit=${PAGE}`, fetcher);
  // Pages after the first, loaded on request.
  const [more, setMore] = useState<AuditEntry[]>([]);
  const [next, setNext] = useState<number | null | undefined>(undefined);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const ids = { a: useId(), b: useId() };

  const entries = useMemo(() => [...(data?.entries ?? []), ...more], [data, more]);
  const cursor = next === undefined ? (data?.nextBefore ?? null) : next;
  const actions = useMemo(
    () => [...new Set(entries.map((e) => e.action))].sort((a, b) => a.localeCompare(b)),
    [entries]
  );
  const shown = entries.filter(
    (e) =>
      (action === "" || e.action === action) &&
      (actor.trim() === "" || e.actor.toLowerCase().includes(actor.trim().toLowerCase()))
  );

  async function loadMore() {
    if (cursor === null) return;
    setLoading(true);
    setLoadError(null);
    try {
      const page = await apiJson<AuditResponse>(`/api/audit?limit=${PAGE}&before=${cursor}`);
      setMore((cur) => [...cur, ...page.entries]);
      setNext(page.nextBefore);
    } catch (err) {
      setLoadError(errorText(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="space-y-4">
      <PageTitle title="Audit log">
        <p>
          A record of who did what and when: sign-ins, changes to accounts, services and settings.
          Passwords, keys and tokens are never recorded. Newest first.
        </p>
      </PageTitle>

      <div className="flex flex-wrap items-end gap-4">
        <div className="space-y-1">
          <label htmlFor={ids.a} className="block text-sm text-slate-300">
            Action
          </label>
          <select
            id={ids.a}
            value={action}
            onChange={(e) => setAction(e.target.value)}
            className={INPUT}
          >
            <option value="">All actions</option>
            {actions.map((a) => (
              <option key={a} value={a}>
                {actionLabel(a)}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor={ids.b} className="block text-sm text-slate-300">
            Who
          </label>
          <input
            id={ids.b}
            value={actor}
            onChange={(e) => setActor(e.target.value)}
            placeholder="Part of a username"
            autoComplete="off"
            className={INPUT}
          />
        </div>
      </div>
      <LiveResult tone="neutral">
        {data ? `Showing ${shown.length} of ${entries.length} loaded entries.` : ""}
      </LiveResult>

      {error && <ErrorNote>Could not load the audit log. {errorText(error, "")}</ErrorNote>}
      {data && (
        <div className={`${CARD} overflow-x-auto`}>
          <table className="w-full min-w-[44rem] text-left text-sm">
            <caption className="sr-only">Audit log, newest first</caption>
            <thead className="text-xs uppercase tracking-wide text-slate-500">
              <tr>
                {["When", "Who", "What", "About", "Details", "From"].map((h) => (
                  <th key={h} scope="col" className="p-3 font-medium">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800 align-top">
              {shown.map((e) => (
                <tr key={e.id}>
                  <td className="p-3 whitespace-nowrap text-slate-400">{when(e.at)}</td>
                  <td className="p-3">{e.actor || "—"}</td>
                  <td className="p-3">
                    {actionLabel(e.action)}
                    {actionLabel(e.action) !== e.action && (
                      <span className="block text-xs text-slate-500">{e.action}</span>
                    )}
                  </td>
                  <td className="p-3 break-all">{e.target || "—"}</td>
                  <td className="p-3 break-words text-slate-400">{describeDetail(e.detail)}</td>
                  <td className="p-3 whitespace-nowrap text-slate-500">{e.address}</td>
                </tr>
              ))}
              {shown.length === 0 && (
                <tr>
                  <td colSpan={6} className="p-4 text-slate-500">
                    {entries.length === 0 ? "Nothing recorded yet." : "No loaded entry matches."}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
      {loadError && <ErrorNote>{loadError}</ErrorNote>}
      {cursor !== null && (
        <button type="button" onClick={() => void loadMore()} disabled={loading} className={BTN}>
          {loading ? "Loading…" : "Load older entries"}
        </button>
      )}
    </div>
  );
}
