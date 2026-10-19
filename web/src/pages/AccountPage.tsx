import { useState } from "react";
import useSWR from "swr";
import { PasswordChangeForm } from "@/components/PasswordChangeForm";
import { RoleBadge } from "@/components/UserMenu";
import { fetcher } from "@/lib/fetcher";
import { apiFetch, toApiError } from "@/lib/http";
import { useSession } from "@/lib/session-context";

interface SessionRow {
  id: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  ip: string;
  userAgent: string;
  current: boolean;
}

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function Sessions() {
  const session = useSession();
  const { data, error, mutate } = useSWR<{ sessions: SessionRow[] }>("/api/auth/sessions", fetcher);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  async function revoke(row: SessionRow) {
    setBusyId(row.id);
    setRevokeError(null);
    try {
      const res = await apiFetch(`/api/auth/sessions/${encodeURIComponent(row.id)}`, {
        method: "DELETE",
      });
      if (!res.ok) throw await toApiError(res, "revoke");
      // Revoking the session this page uses is signing out.
      if (row.current) session.endSession();
      else await mutate();
    } catch (err) {
      setRevokeError(err instanceof Error ? err.message : "Could not end that session.");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <section aria-labelledby="sessions-title" className="space-y-3">
      <h2
        id="sessions-title"
        className="text-sm font-medium uppercase tracking-wide text-slate-500"
      >
        Your sessions
      </h2>
      {error && <p className="text-sm text-red-400">Could not load your sessions.</p>}
      {revokeError && (
        <p role="alert" className="text-sm text-red-400">
          {revokeError}
        </p>
      )}
      {data && (
        <ul className="divide-y divide-slate-800 rounded-lg border border-slate-800 bg-slate-900">
          {data.sessions.map((s) => (
            <li
              key={s.id}
              className="flex flex-wrap items-center justify-between gap-3 p-3 text-sm"
            >
              <div className="min-w-0">
                <p className="truncate text-slate-200">
                  {s.userAgent || "Unknown browser"}
                  {s.current && (
                    <span className="ml-2 rounded bg-sky-900 px-1.5 py-0.5 text-xs text-sky-200">
                      This session
                    </span>
                  )}
                </p>
                <p className="text-xs text-slate-500">
                  {s.ip} · signed in {when(s.createdAt)} · last active {when(s.lastSeenAt)}
                </p>
              </div>
              <button
                onClick={() => void revoke(s)}
                disabled={busyId === s.id}
                className="rounded-md px-3 py-1.5 text-sm text-red-300 hover:bg-slate-800 disabled:opacity-60"
              >
                {s.current ? "Revoke and sign out" : "Revoke"}
                <span className="sr-only">
                  {" "}
                  started {when(s.createdAt)} from {s.ip}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export default function AccountPage() {
  const { user, bypass } = useSession();
  if (!user) return null;

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <section aria-labelledby="account-title" className="space-y-2">
        <h1 id="account-title" className="text-lg font-medium">
          Account
        </h1>
        <p className="text-sm text-slate-300">
          {bypass ? "Local network" : user.username} <RoleBadge role={user.role} />
        </p>
      </section>

      {bypass ? (
        <p className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
          Signed in from the local network. Devices on this network use Wheelhouse without a login,
          so there is no password or session to manage here. Administration needs an administrator
          account: sign in from the menu.
        </p>
      ) : (
        <>
          <section aria-labelledby="password-title" className="space-y-3">
            <h2
              id="password-title"
              className="text-sm font-medium uppercase tracking-wide text-slate-500"
            >
              Change password
            </h2>
            <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
              <PasswordChangeForm />
            </div>
          </section>
          <Sessions />
        </>
      )}
    </div>
  );
}
