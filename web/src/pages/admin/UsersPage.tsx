import { useId, useState } from "react";
import useSWR from "swr";
import { ConfirmModal } from "@/components/ConfirmModal";
import {
  BTN,
  BTN_DANGER,
  BTN_PRIMARY,
  BTN_QUIET,
  CARD,
  INPUT,
  errorText,
} from "@/components/admin/styles";
import { Badge, ErrorNote, LiveResult, OneTimePassword, PageTitle } from "@/components/admin/ui";
import { when, type AdminUser } from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { apiJson, jsonInit } from "@/lib/http";
import { MIN_PASSWORD_LENGTH, generatePassword } from "@/lib/password";
import { useSession } from "@/lib/session-context";

type Confirm =
  | { kind: "delete"; user: AdminUser }
  | { kind: "reset"; user: AdminUser }
  | { kind: "signout"; user: AdminUser }
  | { kind: "disable"; user: AdminUser }
  | { kind: "demote"; user: AdminUser };

interface Shown {
  heading: string;
  username: string;
  password: string;
}

function CreateUserForm({
  onCreated,
  onCancel,
}: {
  onCreated: (u: AdminUser, shown: Shown | null) => void;
  onCancel: () => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [generated, setGenerated] = useState(false);
  const [role, setRole] = useState<"user" | "admin">("user");
  const [mustChange, setMustChange] = useState(true);
  const [reveal, setReveal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ username?: string; password?: string }>({});
  const ids = { u: useId(), p: useId(), r: useId() };

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const problems: typeof fieldErrors = {};
    if (username.trim() === "") problems.username = "Enter a username.";
    if ([...password].length < MIN_PASSWORD_LENGTH) {
      problems.password = `The password must be at least ${MIN_PASSWORD_LENGTH} characters. Use Generate for a random one.`;
    }
    setFieldErrors(problems);
    setError(null);
    if (Object.keys(problems).length > 0) return;
    setBusy(true);
    try {
      const r = await apiJson<{ user: AdminUser }>(
        "/api/users",
        jsonInit("POST", {
          username: username.trim(),
          password,
          role,
          mustChangePassword: mustChange,
        })
      );
      onCreated(
        r.user,
        generated ? { heading: "Account created", username: r.user.username, password } : null
      );
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} noValidate className={`${CARD} space-y-4 p-4`}>
      <h2 className="font-medium">Add a user</h2>
      <div className="space-y-1">
        <label htmlFor={ids.u} className="block text-sm text-slate-300">
          Username
        </label>
        <input
          id={ids.u}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="off"
          aria-invalid={fieldErrors.username ? true : undefined}
          className={INPUT}
        />
        {fieldErrors.username && <p className="text-xs text-red-400">{fieldErrors.username}</p>}
      </div>
      <div className="space-y-1">
        <label htmlFor={ids.p} className="block text-sm text-slate-300">
          Password
        </label>
        <div className="flex flex-wrap gap-2">
          <input
            id={ids.p}
            type={reveal ? "text" : "password"}
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
              setGenerated(false);
            }}
            autoComplete="new-password"
            spellCheck={false}
            aria-invalid={fieldErrors.password ? true : undefined}
            className={`${INPUT} flex-1`}
          />
          <button
            type="button"
            aria-pressed={reveal}
            onClick={() => setReveal((r) => !r)}
            className={BTN_QUIET}
          >
            {reveal ? "Hide" : "Show"} <span className="sr-only">password</span>
          </button>
          <button
            type="button"
            className={BTN}
            onClick={() => {
              setPassword(generatePassword());
              setGenerated(true);
              setReveal(true);
            }}
          >
            Generate
          </button>
        </div>
        {fieldErrors.password && <p className="text-xs text-red-400">{fieldErrors.password}</p>}
        <p className="text-xs text-slate-500">
          At least {MIN_PASSWORD_LENGTH} characters. You will pass it on to the person yourself.
        </p>
      </div>
      <div className="space-y-1">
        <label htmlFor={ids.r} className="block text-sm text-slate-300">
          Role
        </label>
        <select
          id={ids.r}
          value={role}
          onChange={(e) => setRole(e.target.value as "user" | "admin")}
          className={INPUT}
        >
          <option value="user">User: uses the app, cannot change settings</option>
          <option value="admin">Administrator: can change everything</option>
        </select>
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input
          type="checkbox"
          checked={mustChange}
          onChange={(e) => setMustChange(e.target.checked)}
        />
        Must choose a new password at first sign-in
      </label>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex gap-3">
        <button type="submit" disabled={busy} className={BTN_PRIMARY}>
          {busy ? "Creating…" : "Create user"}
        </button>
        <button type="button" onClick={onCancel} className={BTN_QUIET}>
          Cancel
        </button>
      </div>
    </form>
  );
}

const CONFIRM_TEXT: Record<
  Confirm["kind"],
  (name: string) => { title: string; body: string; label: string }
> = {
  delete: (n) => ({
    title: `Delete ${n}?`,
    body: `${n} is signed out everywhere and the account is removed for good. Library changes they made stay as they are.`,
    label: "Delete user",
  }),
  reset: (n) => ({
    title: `Reset the password of ${n}?`,
    body: `A new one-time password is created and shown to you once. ${n} is signed out everywhere and must choose a new password at the next sign-in.`,
    label: "Reset password",
  }),
  signout: (n) => ({
    title: `Sign ${n} out everywhere?`,
    body: `Every session of ${n} ends now. They can sign in again with their password.`,
    label: "Sign out everywhere",
  }),
  disable: (n) => ({
    title: `Disable ${n}?`,
    body: `${n} is signed out everywhere and cannot sign in until you enable the account again.`,
    label: "Disable account",
  }),
  demote: (n) => ({
    title: `Make ${n} a regular user?`,
    body: `${n} loses access to the administration pages. The last administrator cannot be demoted.`,
    label: "Remove administrator role",
  }),
};

export default function UsersPage() {
  const { user: me } = useSession();
  const { data, error, mutate } = useSWR<{ users: AdminUser[] }>("/api/users", fetcher);
  const [adding, setAdding] = useState(false);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [shown, setShown] = useState<Shown | null>(null);

  function begin() {
    setFailure(null);
    setMessage(null);
  }

  async function patch(u: AdminUser, body: { role?: string; disabled?: boolean }, done: string) {
    begin();
    setBusy(true);
    try {
      await apiJson(`/api/users/${u.id}`, jsonInit("PATCH", body));
      await mutate();
      setMessage(done);
    } catch (err) {
      setFailure(errorText(err));
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }

  async function run(c: Confirm) {
    const u = c.user;
    switch (c.kind) {
      case "disable":
        return patch(u, { disabled: true }, `${u.username} is disabled and signed out.`);
      case "demote":
        return patch(u, { role: "user" }, `${u.username} is now a regular user.`);
      case "delete":
        begin();
        setBusy(true);
        try {
          await apiJson(`/api/users/${u.id}`, { method: "DELETE" });
          await mutate();
          setMessage(`${u.username} was deleted.`);
        } catch (err) {
          setFailure(errorText(err));
        } finally {
          setBusy(false);
          setConfirm(null);
        }
        return;
      case "signout":
        begin();
        setBusy(true);
        try {
          const r = await apiJson<{ revoked: number }>(`/api/users/${u.id}/sessions`, {
            method: "DELETE",
          });
          setMessage(
            `${u.username} was signed out of ${r.revoked} session${r.revoked === 1 ? "" : "s"}.`
          );
        } catch (err) {
          setFailure(errorText(err));
        } finally {
          setBusy(false);
          setConfirm(null);
        }
        return;
      case "reset": {
        begin();
        setBusy(true);
        const password = generatePassword();
        try {
          await apiJson(`/api/users/${u.id}/password`, jsonInit("POST", { password }));
          await mutate();
          setShown({ heading: `New password for ${u.username}`, username: u.username, password });
        } catch (err) {
          setFailure(errorText(err));
        } finally {
          setBusy(false);
          setConfirm(null);
        }
        return;
      }
    }
  }

  async function changeRole(u: AdminUser, role: "admin" | "user") {
    if (role === u.role) return;
    if (role === "user") {
      setConfirm({ kind: "demote", user: u });
      return;
    }
    await patch(u, { role }, `${u.username} is now an administrator.`);
  }

  const text = confirm && CONFIRM_TEXT[confirm.kind](confirm.user.username);

  return (
    <div className="space-y-4">
      <PageTitle
        title="Users"
        actions={
          !adding && (
            <button type="button" onClick={() => setAdding(true)} className={BTN_PRIMARY}>
              Add user
            </button>
          )
        }
      >
        <p>
          People who can sign in. An administrator can change settings and manage accounts; a user
          can use the app but not change how it is set up. There must always be at least one enabled
          administrator.
        </p>
      </PageTitle>

      {shown && (
        <OneTimePassword
          heading={shown.heading}
          username={shown.username}
          password={shown.password}
          onDismiss={() => setShown(null)}
        />
      )}
      {adding && (
        <CreateUserForm
          onCancel={() => setAdding(false)}
          onCreated={(u, one) => {
            setAdding(false);
            setFailure(null);
            setMessage(`${u.username} was added.`);
            if (one) setShown(one);
            void mutate();
          }}
        />
      )}
      {failure && <ErrorNote>{failure}</ErrorNote>}
      <LiveResult>{message}</LiveResult>
      {error && <ErrorNote>Could not load the users. {errorText(error, "")}</ErrorNote>}

      {data && (
        <div className={`${CARD} overflow-x-auto`}>
          <table className="w-full min-w-[40rem] text-left text-sm">
            <caption className="sr-only">Users</caption>
            <thead className="text-xs uppercase tracking-wide text-slate-500">
              <tr>
                <th scope="col" className="p-3 font-medium">
                  Username
                </th>
                <th scope="col" className="p-3 font-medium">
                  Role
                </th>
                <th scope="col" className="p-3 font-medium">
                  Created
                </th>
                <th scope="col" className="p-3 font-medium">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800">
              {data.users.map((u) => {
                const self = u.id === me?.id;
                return (
                  <tr key={u.id}>
                    <th scope="row" className="p-3 font-normal">
                      <span className="mr-2">{u.username}</span>
                      {self && <Badge tone="info">You</Badge>}{" "}
                      {u.disabled && <Badge tone="bad">Disabled</Badge>}{" "}
                      {u.mustChangePassword && <Badge tone="warn">Must change password</Badge>}
                    </th>
                    <td className="p-3">
                      <label className="sr-only" htmlFor={`role-${u.id}`}>
                        Role of {u.username}
                      </label>
                      <select
                        id={`role-${u.id}`}
                        value={u.role}
                        disabled={busy}
                        onChange={(e) => void changeRole(u, e.target.value as "admin" | "user")}
                        className="rounded-md border border-slate-700 bg-slate-950 px-2 py-1 text-sm"
                      >
                        <option value="user">user</option>
                        <option value="admin">admin</option>
                      </select>
                    </td>
                    <td className="p-3 text-slate-400">{when(u.createdAt)}</td>
                    <td className="p-3">
                      <div className="flex flex-wrap gap-1">
                        {u.disabled ? (
                          <button
                            type="button"
                            disabled={busy}
                            className={BTN_QUIET}
                            onClick={() =>
                              void patch(u, { disabled: false }, `${u.username} is enabled.`)
                            }
                          >
                            Enable <span className="sr-only">{u.username}</span>
                          </button>
                        ) : (
                          <button
                            type="button"
                            disabled={busy}
                            className={BTN_QUIET}
                            onClick={() => setConfirm({ kind: "disable", user: u })}
                          >
                            Disable <span className="sr-only">{u.username}</span>
                          </button>
                        )}
                        <button
                          type="button"
                          disabled={busy}
                          className={BTN_QUIET}
                          onClick={() => setConfirm({ kind: "reset", user: u })}
                        >
                          Reset password <span className="sr-only">of {u.username}</span>
                        </button>
                        <button
                          type="button"
                          disabled={busy}
                          className={BTN_QUIET}
                          onClick={() => setConfirm({ kind: "signout", user: u })}
                        >
                          Sign out everywhere <span className="sr-only">for {u.username}</span>
                        </button>
                        <button
                          type="button"
                          disabled={busy}
                          className={BTN_DANGER}
                          onClick={() => setConfirm({ kind: "delete", user: u })}
                        >
                          Delete <span className="sr-only">{u.username}</span>
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {confirm && text && (
        <ConfirmModal
          title={text.title}
          body={text.body}
          confirmLabel={text.label}
          busyLabel="Working…"
          busy={busy}
          onConfirm={() => void run(confirm)}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  );
}
