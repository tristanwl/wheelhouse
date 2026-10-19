import { useCallback, useEffect, useMemo, useState } from "react";
import { Outlet, useLocation } from "react-router";
import { useSWRConfig } from "swr";
import {
  apiFetch,
  jsonInit,
  setApiHandlers,
  setCsrfToken,
  SESSION_URL,
  toApiError,
} from "@/lib/http";
import { titleFor } from "@/lib/titles";
import {
  parseSession,
  SessionContext,
  type SessionBody,
  type SessionStatus,
  type SessionUser,
  type SessionValue,
} from "@/lib/session-context";

interface State {
  status: SessionStatus;
  user: SessionUser | null;
  bypass: boolean;
  error?: string;
}

const LOADING: State = { status: "loading", user: null, bypass: false };
const ANONYMOUS: State = { status: "anonymous", user: null, bypass: false };

/**
 * Holds who is signed in, for the whole app. It reads `GET /api/auth/session`
 * once at start and registers the API client's central handlers: a 401 anywhere
 * makes the state anonymous (the route guards then send the person to the login
 * page, remembering where they were) and a 503 "setup required" makes it
 * setup-required (the guards send them to the wizard). Nothing here is written
 * to browser storage; the cookie is HttpOnly and the CSRF token stays in memory.
 */
export function SessionProvider({ children }: { children: React.ReactNode }) {
  const { mutate } = useSWRConfig();
  const [state, setState] = useState<State>(LOADING);

  const forgetCaches = useCallback(() => {
    // Nothing one person loaded may show for the next: drop every cached answer.
    void mutate(() => true, undefined, { revalidate: false });
  }, [mutate]);

  const apply = useCallback((body: SessionBody) => {
    setCsrfToken(body.csrfToken);
    const { user, bypass } = parseSession(body);
    setState(
      user
        ? { status: "authenticated", user, bypass }
        : { status: "error", user: null, bypass: false, error: "The server sent no account." }
    );
  }, []);

  const endSession = useCallback(() => {
    setCsrfToken(null);
    forgetCaches();
    setState((s) => (s.status === "anonymous" ? s : ANONYMOUS));
  }, [forgetCaches]);

  const refresh = useCallback(async () => {
    try {
      const res = await apiFetch(SESSION_URL, { anonymous: true });
      if (res.ok) {
        apply((await res.json()) as SessionBody);
        return;
      }
      const err = await toApiError(res, SESSION_URL);
      if (res.status === 401) {
        setCsrfToken(null);
        setState(ANONYMOUS);
      } else if (res.status === 503 && err.message === "setup required") {
        setCsrfToken(null);
        setState({ status: "setup-required", user: null, bypass: false });
      } else {
        setState({ status: "error", user: null, bypass: false, error: err.message });
      }
    } catch {
      setState({
        status: "error",
        user: null,
        bypass: false,
        error: "Could not reach the server.",
      });
    }
  }, [apply]);

  useEffect(() => {
    setApiHandlers({
      onUnauthorized: endSession,
      onSetupRequired: () => {
        setCsrfToken(null);
        forgetCaches();
        setState({ status: "setup-required", user: null, bypass: false });
      },
      onPasswordChangeRequired: () =>
        setState((s) =>
          s.user && !s.user.mustChangePassword
            ? { ...s, user: { ...s.user, mustChangePassword: true } }
            : s
        ),
    });
    return () => setApiHandlers(null);
  }, [endSession, forgetCaches]);

  useEffect(() => {
    // The first read of the session; async, so the state it sets is not set during render.
    void refresh();
  }, [refresh]);

  const login = useCallback(
    async (username: string, password: string) => {
      const res = await apiFetch("/api/auth/login", {
        ...jsonInit("POST", { username, password }),
        anonymous: true,
      });
      if (!res.ok) throw await toApiError(res, "/api/auth/login");
      forgetCaches();
      apply((await res.json()) as SessionBody);
    },
    [apply, forgetCaches]
  );

  const logout = useCallback(async () => {
    try {
      await apiFetch("/api/auth/logout", { method: "POST" });
    } finally {
      // Even if the request failed, this browser must stop acting as the user.
      endSession();
    }
  }, [endSession]);

  const value = useMemo<SessionValue>(
    () => ({
      status: state.status,
      user: state.user,
      bypass: state.bypass,
      isAdmin: state.user?.role === "admin" && !state.bypass,
      mustChangePassword: state.user?.mustChangePassword === true,
      error: state.error,
      login,
      logout,
      refresh,
      applySession: apply,
      endSession,
    }),
    [state, login, logout, refresh, apply, endSession]
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

/** The route element that provides the session to every page below it. */
export function SessionRoot() {
  const { pathname } = useLocation();
  useEffect(() => {
    document.title = titleFor(pathname);
  }, [pathname]);
  return (
    <SessionProvider>
      <Outlet />
    </SessionProvider>
  );
}
