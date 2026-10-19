import { useEffect, useRef, useState } from "react";
import { apiFetch, jsonInit, toApiError } from "@/lib/http";
import { MIN_PASSWORD_LENGTH, passwordStrength } from "@/lib/password";
import { useSession, type SessionBody } from "@/lib/session-context";
import { Field, FormError, PasswordHint, SubmitButton } from "./Form";

/**
 * Change the signed-in account's own password. The server checks the current
 * password, ends every other session and answers with a fresh session, whose
 * CSRF token replaces the old one.
 */
export function PasswordChangeForm({
  onChanged,
  autoFocus = false,
}: {
  onChanged?: () => void;
  autoFocus?: boolean;
}) {
  const session = useSession();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const firstRef = useRef<HTMLInputElement>(null);
  const errorRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (autoFocus) firstRef.current?.focus();
  }, [autoFocus]);
  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  const mismatch = confirm.length > 0 && confirm !== next;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setError(null);
    setDone(false);
    if (!passwordStrength(next).meetsMinimum) {
      setError(`The new password must be at least ${MIN_PASSWORD_LENGTH} characters.`);
      return;
    }
    if (next !== confirm) {
      setError("The two new passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      const res = await apiFetch(
        "/api/auth/password",
        jsonInit("POST", { currentPassword: current, newPassword: next })
      );
      if (!res.ok) {
        const err = await toApiError(res, "/api/auth/password");
        // A 401 was already handled centrally (back to the login page).
        throw err.status === 429 ? new Error("Too many attempts. Wait a few minutes.") : err;
      }
      session.applySession((await res.json()) as SessionBody);
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
      onChanged?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not change the password.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4" noValidate>
      {error && <FormError ref={errorRef}>{error}</FormError>}
      {done && (
        <p
          role="status"
          className="rounded-md border border-emerald-900 px-3 py-2 text-sm text-emerald-300"
        >
          Password changed. Your other sessions were signed out.
        </p>
      )}
      {/* The username, for password managers; hidden from view and from the tab order. */}
      <input
        type="text"
        name="username"
        autoComplete="username"
        value={session.user?.username ?? ""}
        readOnly
        hidden
      />
      <Field
        ref={firstRef}
        label="Current password"
        name="current-password"
        type="password"
        autoComplete="current-password"
        required
        value={current}
        disabled={busy}
        onChange={(e) => setCurrent(e.target.value)}
      />
      <Field
        label="New password"
        name="new-password"
        type="password"
        autoComplete="new-password"
        required
        value={next}
        disabled={busy}
        onChange={(e) => setNext(e.target.value)}
        hint={<PasswordHint password={next} />}
      />
      <Field
        label="Confirm new password"
        name="confirm-password"
        type="password"
        autoComplete="new-password"
        required
        value={confirm}
        disabled={busy}
        aria-invalid={mismatch}
        onChange={(e) => setConfirm(e.target.value)}
        hint={mismatch ? <span className="text-red-400">The passwords do not match.</span> : null}
      />
      <SubmitButton busy={busy} busyLabel="Changing…">
        Change password
      </SubmitButton>
    </form>
  );
}
