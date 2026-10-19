import { useState } from "react";
import useSWR from "swr";
import { ConfirmModal } from "@/components/ConfirmModal";
import { BTN_DANGER, BTN_QUIET, CARD, errorText } from "@/components/admin/styles";
import { Badge, ErrorNote, LiveResult, PageTitle } from "@/components/admin/ui";
import { when, type AdminSession, type AdminUser } from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { apiJson } from "@/lib/http";
import { useSession } from "@/lib/session-context";

type Ask =
  { kind: "one"; user: AdminUser; session: AdminSession } | { kind: "all"; user: AdminUser };

function UserSessions({
  user,
  onResult,
}: {
  user: AdminUser;
  onResult: (m: { text: string; error?: boolean }) => void;
}) {
  const session = useSession();
  const { data, error, mutate } = useSWR<{ sessions: AdminSession[] }>(
    `/api/users/${user.id}/sessions`,
    fetcher
  );
  const [ask, setAsk] = useState<Ask | null>(null);
  const [busy, setBusy] = useState(false);

  async function confirm() {
    if (!ask) return;
    setBusy(true);
    try {
      if (ask.kind === "one") {
        await apiJson(`/api/users/${user.id}/sessions/${ask.session.id}`, { method: "DELETE" });
        onResult({ text: `One session of ${user.username} was ended.` });
        // Ending the session this page uses is signing out.
        if (ask.session.current) session.endSession();
      } else {
        const r = await apiJson<{ revoked: number }>(`/api/users/${user.id}/sessions`, {
          method: "DELETE",
        });
        onResult({
          text: `${r.revoked} session${r.revoked === 1 ? "" : "s"} of ${user.username} ended.`,
        });
        if (user.id === session.user?.id) session.endSession();
      }
      await mutate();
    } catch (err) {
      onResult({ text: errorText(err), error: true });
    } finally {
      setBusy(false);
      setAsk(null);
    }
  }

  const list = data?.sessions ?? [];
  return (
    <section aria-label={`Sessions of ${user.username}`} className={`${CARD} space-y-2 p-4`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-medium">
          {user.username} {user.disabled && <Badge tone="bad">Disabled</Badge>}{" "}
          <span className="text-xs font-normal text-slate-500">
            {data ? `${list.length} active` : ""}
          </span>
        </h2>
        {list.length > 0 && (
          <button
            type="button"
            className={BTN_DANGER}
            disabled={busy}
            onClick={() => setAsk({ kind: "all", user })}
          >
            End all sessions <span className="sr-only">of {user.username}</span>
          </button>
        )}
      </div>
      {error && <p className="text-sm text-red-400">Could not load these sessions.</p>}
      {data && list.length === 0 && (
        <p className="text-sm text-slate-500">Not signed in anywhere.</p>
      )}
      <ul className="divide-y divide-slate-800">
        {list.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-3 py-2 text-sm">
            <div className="min-w-0">
              <p className="truncate text-slate-200">
                {s.userAgent || "Unknown browser"}
                {s.current && (
                  <span className="ml-2">
                    <Badge tone="info">This session</Badge>
                  </span>
                )}
              </p>
              <p className="text-xs text-slate-500">
                {s.ip} · signed in {when(s.createdAt)} · last active {when(s.lastSeenAt)} · ends{" "}
                {when(s.expiresAt)}
              </p>
            </div>
            <button
              type="button"
              disabled={busy}
              className={BTN_QUIET}
              onClick={() => setAsk({ kind: "one", user, session: s })}
            >
              End session
              <span className="sr-only">
                {" "}
                of {user.username} from {s.ip}, started {when(s.createdAt)}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {ask && (
        <ConfirmModal
          title={
            ask.kind === "all"
              ? `End all sessions of ${user.username}?`
              : `End this session of ${user.username}?`
          }
          body={
            ask.kind === "all"
              ? `${user.username} is signed out everywhere and needs to sign in again.${
                  user.id === session.user?.id ? " That includes you, right now." : ""
                }`
              : ask.session.current
                ? "This is the session you are using now. You will be signed out."
                : `That device is signed out and ${user.username} needs to sign in again there.`
          }
          confirmLabel="End session"
          busyLabel="Ending…"
          busy={busy}
          onConfirm={() => void confirm()}
          onCancel={() => setAsk(null)}
        />
      )}
    </section>
  );
}

export default function SessionsPage() {
  const { data, error } = useSWR<{ users: AdminUser[] }>("/api/users", fetcher);
  const [result, setResult] = useState<{ text: string; error?: boolean } | null>(null);
  return (
    <div className="space-y-4">
      <PageTitle title="Sessions">
        <p>
          Everywhere each person is signed in right now. End a session to sign that device out; the
          person can sign in again with their password. To lock someone out, disable the account on
          the Users page.
        </p>
      </PageTitle>
      {result?.error ? (
        <ErrorNote>{result.text}</ErrorNote>
      ) : (
        <LiveResult>{result?.text}</LiveResult>
      )}
      {error && <ErrorNote>Could not load the users. {errorText(error, "")}</ErrorNote>}
      {data?.users.map((u) => (
        <UserSessions key={u.id} user={u} onResult={setResult} />
      ))}
    </div>
  );
}
