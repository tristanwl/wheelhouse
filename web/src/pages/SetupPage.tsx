import { useEffect, useRef, useState } from "react";
import { Navigate, useNavigate } from "react-router";
import { Field, FormError, PasswordHint, SubmitButton } from "@/components/Form";
import { SessionFailed, SessionLoading } from "@/components/Guards";
import { ApiError, apiFetch, jsonInit, toApiError } from "@/lib/http";
import { MIN_PASSWORD_LENGTH, passwordStrength } from "@/lib/password";
import { useSession } from "@/lib/session-context";

function setupErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403) {
      return "That setup token is not right. Copy it again from the server log or from the setup-token file.";
    }
    if (err.status === 429) return "Too many attempts. Wait a few minutes and try again.";
    if (err.status === 400 || err.status === 409) return err.message;
  }
  return "Could not create the administrator. Check your connection and try again.";
}

/**
 * First-run wizard, step 1 of 3: the setup token proves you can read the
 * server's log or volume, then the administrator account is created and signed
 * in. Adding services and the local-network option are administrator pages
 * (under /admin); the dashboard offers them right after this.
 */
export default function SetupPage() {
  const session = useSession();
  const navigate = useNavigate();
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState(false);
  const tokenRef = useRef<HTMLInputElement>(null);
  const errorRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    tokenRef.current?.focus();
  }, [session.status]);
  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  if (session.status === "loading") return <SessionLoading />;
  if (session.status === "error") return <SessionFailed />;
  if (session.status === "authenticated") {
    return <Navigate to="/" replace state={created ? { welcome: true } : null} />;
  }
  // Setup is over (or never was needed): the login page is the way in.
  if (session.status === "anonymous" && !created) return <Navigate to="/login" replace />;

  const mismatch = confirm.length > 0 && confirm !== password;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setError(null);
    if (!passwordStrength(password).meetsMinimum) {
      setError(`The password must be at least ${MIN_PASSWORD_LENGTH} characters.`);
      return;
    }
    if (password !== confirm) {
      setError("The two passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      const res = await apiFetch("/api/auth/setup", {
        ...jsonInit("POST", { token: token.trim(), username: username.trim(), password }),
        anonymous: true,
      });
      if (!res.ok) throw await toApiError(res, "/api/auth/setup");
      setCreated(true);
      try {
        await session.login(username.trim(), password);
        void navigate("/", { replace: true, state: { welcome: true } });
      } catch {
        // The account exists; only the automatic sign-in failed. Learn that setup is over.
        await session.refresh();
        void navigate("/login", { replace: true });
      }
    } catch (err) {
      setError(setupErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      onSubmit={(e) => void submit(e)}
      className="space-y-4"
      aria-labelledby="setup-title"
      noValidate
    >
      <div>
        <p className="text-xs uppercase tracking-wide text-slate-500">Step 1 of 3</p>
        <h1 id="setup-title" className="text-lg font-medium">
          Set up Wheelhouse
        </h1>
        <p className="mt-1 text-sm text-slate-400">
          Create the administrator account. Next you will add your services and, if you like, let
          devices on your home network in without a login; both are under Administration.
        </p>
      </div>
      {error && <FormError ref={errorRef}>{error}</FormError>}
      <Field
        ref={tokenRef}
        label="Setup token"
        name="setup-token"
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        required
        value={token}
        disabled={busy}
        onChange={(e) => setToken(e.target.value)}
        hint={
          <>
            Wheelhouse printed a one-time token in its log when it started, and saved it in{" "}
            <code className="text-slate-400">/config/setup-token</code> inside the container (
            <code className="text-slate-400">docker logs</code> shows the log). It proves you are
            the person who runs this server.
          </>
        }
      />
      <Field
        label="Administrator username"
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
        label="Password"
        name="new-password"
        type="password"
        autoComplete="new-password"
        required
        minLength={MIN_PASSWORD_LENGTH}
        value={password}
        disabled={busy}
        onChange={(e) => setPassword(e.target.value)}
        hint={<PasswordHint password={password} />}
      />
      <Field
        label="Confirm password"
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
      <SubmitButton busy={busy} busyLabel="Creating…">
        Create administrator
      </SubmitButton>
    </form>
  );
}
