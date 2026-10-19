import { useEffect, useRef, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { Field, FormError, SubmitButton } from "@/components/Form";
import { SessionLoading, SessionFailed } from "@/components/Guards";
import { ApiError } from "@/lib/http";
import { safeRedirect } from "@/lib/redirect";
import { useSession } from "@/lib/session-context";

/**
 * What to tell someone whose sign-in failed. The server answers a wrong
 * password, an unknown user and a locked account the same way (401), so this
 * cannot say which field was wrong, and does not try to.
 */
function loginErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 429) {
      const wait = err.retryAfter
        ? ` Try again in about ${Math.max(1, Math.ceil(err.retryAfter / 60))} minute(s).`
        : " Wait a few minutes and try again.";
      return `Too many sign-in attempts.${wait}`;
    }
    if (err.status === 401) {
      return "Invalid username or password. After several failed attempts sign-in is delayed or locked for a while, so wait a few minutes before trying again.";
    }
    if (err.status === 503) return "The server is not ready to sign you in. Try again shortly.";
  }
  return "Could not sign in. Check your connection and try again.";
}

export default function LoginPage() {
  const session = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const redirect = safeRedirect(params.get("redirect"));
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const userRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    userRef.current?.focus();
  }, [session.status]);

  useEffect(() => {
    // Inputs are disabled while submitting, so focus the password once they are back.
    if (error) passwordRef.current?.focus();
  }, [error]);

  if (session.status === "loading") return <SessionLoading />;
  if (session.status === "error") return <SessionFailed />;
  if (session.status === "setup-required") return <Navigate to="/setup" replace />;
  // Already signed in with a real session: nothing to do here. (The local-network
  // bypass is not a login, so that visitor may still sign in, e.g. as an admin.)
  if (session.status === "authenticated" && !session.bypass) {
    return <Navigate to={redirect} replace />;
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await session.login(username.trim(), password);
      setPassword("");
      void navigate(redirect, { replace: true });
    } catch (err) {
      setError(loginErrorMessage(err));
      setPassword("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4" aria-labelledby="login-title">
      <h1 id="login-title" className="text-lg font-medium">
        Sign in
      </h1>
      {session.bypass && (
        <p className="rounded-md border border-slate-700 px-3 py-2 text-sm text-slate-300">
          You are using Wheelhouse from the local network without signing in. Sign in to manage
          services and users.
        </p>
      )}
      {error && <FormError>{error}</FormError>}
      <Field
        ref={userRef}
        label="Username"
        name="username"
        autoComplete="username"
        autoCapitalize="none"
        spellCheck={false}
        required
        value={username}
        disabled={busy}
        onChange={(e) => setUsername(e.target.value)}
      />
      <Field
        ref={passwordRef}
        label="Password"
        name="password"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        disabled={busy}
        onChange={(e) => setPassword(e.target.value)}
      />
      <SubmitButton busy={busy} busyLabel="Signing in…">
        Sign in
      </SubmitButton>
      <p className="text-xs text-slate-500">
        Forgot the only administrator password? Run{" "}
        <code className="text-slate-400">wheelhouse reset-password</code> inside the container.
      </p>
    </form>
  );
}
