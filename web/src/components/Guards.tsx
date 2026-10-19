import { Link, Navigate, Outlet, useLocation } from "react-router";
import { loginUrl, passwordChangeUrl } from "@/lib/redirect";
import { useSession } from "@/lib/session-context";

/** Shown while the session is read, so a protected page never flashes for the wrong person. */
export function SessionLoading() {
  return (
    <p role="status" className="p-8 text-center text-sm text-slate-400">
      Loading…
    </p>
  );
}

export function SessionFailed() {
  const { error, refresh } = useSession();
  return (
    <div role="alert" className="mx-auto mt-16 max-w-md rounded-lg border border-red-900 p-4">
      <h1 className="mb-2 text-lg font-medium">Wheelhouse could not check your session</h1>
      <p className="mb-3 text-sm text-slate-300">{error ?? "The server did not answer."}</p>
      <button
        onClick={() => void refresh()}
        className="rounded-md bg-slate-800 px-3 py-1.5 text-sm hover:bg-slate-700"
      >
        Try again
      </button>
    </div>
  );
}

/**
 * Everything but the login and setup pages. Without a session the person goes
 * to the login page (remembering this address) or, on a fresh install, to the
 * setup wizard; an account that must change its password sees nothing else.
 */
export function RequireAuth() {
  const session = useSession();
  const location = useLocation();
  switch (session.status) {
    case "loading":
      return <SessionLoading />;
    case "error":
      return <SessionFailed />;
    case "setup-required":
      return <Navigate to="/setup" replace />;
    case "anonymous":
      return <Navigate to={loginUrl(location.pathname + location.search)} replace />;
  }
  if (session.mustChangePassword && location.pathname !== "/change-password") {
    return <Navigate to={passwordChangeUrl(location.pathname + location.search)} replace />;
  }
  return <Outlet />;
}

export function NotAuthorised() {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
      <h1 className="mb-2 text-lg font-medium">Not authorised</h1>
      <p>
        Your account does not have access to this page. It is for administrators. Go back to the{" "}
        <Link to="/" className="underline hover:text-white">
          Dashboard
        </Link>
        .
      </p>
    </div>
  );
}

/**
 * The `/admin/*` namespace. A `user` sees the not-authorised page. Someone let in
 * by the local-network bypass is never an administrator, so they are asked for an
 * admin login instead (and come back here after it).
 */
export function RequireAdmin() {
  const session = useSession();
  const location = useLocation();
  if (session.bypass) {
    return <Navigate to={loginUrl(location.pathname + location.search)} replace />;
  }
  if (!session.isAdmin) return <NotAuthorised />;
  return <Outlet />;
}
