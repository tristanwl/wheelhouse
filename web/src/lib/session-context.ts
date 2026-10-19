import { createContext, useContext } from "react";

export type Role = "admin" | "user";

export interface SessionUser {
  id?: string;
  username: string;
  role: Role;
  mustChangePassword: boolean;
}

/**
 * `GET /api/auth/session` (and the login and password-change answers):
 * `{ "user": {...}, "csrfToken": "...", "bypass": true|false, ... }`.
 *
 * `bypass` is set by the server when the request came from the configured local
 * network and was let in without a login (plan item 5). The UI treats a missing
 * `bypass` as false, a missing `user` on a bypass answer as a plain `user` called
 * "Local network", and a missing `csrfToken` as "nothing to send".
 */
export interface SessionBody {
  user?: Partial<SessionUser> | null;
  csrfToken?: string;
  bypass?: boolean;
  [key: string]: unknown;
}

export type SessionStatus = "loading" | "authenticated" | "anonymous" | "setup-required" | "error";

export interface SessionValue {
  status: SessionStatus;
  user: SessionUser | null;
  /** Signed in by the local-network bypass, not by a login: no sign-out, no password. */
  bypass: boolean;
  isAdmin: boolean;
  mustChangePassword: boolean;
  /** Why the session could not be read, when status is "error". */
  error?: string;
  /** Signs in; throws an `ApiError` (401 invalid, 429 throttled) for the form to show. */
  login(username: string, password: string): Promise<void>;
  /** Signs out on the server, then forgets the session and every cached answer. */
  logout(): Promise<void>;
  /** Reads the session again (after a retry). */
  refresh(): Promise<void>;
  /** Takes a session answer the caller already has (a password change returns one). */
  applySession(body: SessionBody): void;
  /** The session this page was using is gone (revoked from the account page). */
  endSession(): void;
}

export const SessionContext = createContext<SessionValue | null>(null);

export function useSession(): SessionValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession needs a SessionProvider");
  return value;
}

/** Turns a server answer into the UI's user, defensively. */
export function parseSession(body: SessionBody | null | undefined): {
  user: SessionUser | null;
  bypass: boolean;
} {
  const bypass = body?.bypass === true;
  const u = body?.user;
  if (u && typeof u === "object" && typeof u.username === "string") {
    return {
      bypass,
      user: {
        id: typeof u.id === "string" ? u.id : undefined,
        username: u.username,
        role: u.role === "admin" && !bypass ? "admin" : "user",
        mustChangePassword: u.mustChangePassword === true && !bypass,
      },
    };
  }
  if (bypass) {
    return { bypass, user: { username: "Local network", role: "user", mustChangePassword: false } };
  }
  return { bypass, user: null };
}
