import { useEffect, useId, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useSession, type Role } from "@/lib/session-context";

export function RoleBadge({ role }: { role: Role }) {
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-xs ${
        role === "admin" ? "bg-amber-900 text-amber-200" : "bg-slate-800 text-slate-300"
      }`}
    >
      {role}
    </span>
  );
}

/**
 * The header's account menu: who you are and your role, a link to the account
 * page and Sign out. Someone using the local-network bypass has not signed in,
 * so there is no Sign out, only a way to sign in as an administrator.
 */
export function UserMenu() {
  const { user, bypass, logout } = useSession();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) return;
    const onPointer = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!user) return null;
  const name = bypass ? "Local network" : user.username;

  async function signOut() {
    setOpen(false);
    await logout();
    void navigate("/login", { replace: true });
  }

  return (
    <div ref={rootRef} className="relative text-sm">
      <button
        ref={buttonRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 rounded-md px-2 py-1 text-slate-300 hover:bg-slate-800 hover:text-white"
      >
        <span className="max-w-[10rem] truncate">{name}</span>
        <RoleBadge role={user.role} />
      </button>
      {open && (
        <div
          id={menuId}
          role="menu"
          className="absolute right-0 z-40 mt-1 w-56 rounded-md border border-slate-700 bg-slate-900 py-1 shadow-lg"
        >
          <p className="px-3 py-2 text-xs text-slate-500">
            {bypass ? "Signed in from the local network" : `Signed in as ${user.username}`}
          </p>
          <Link
            role="menuitem"
            to="/account"
            onClick={() => setOpen(false)}
            className="block px-3 py-2 text-slate-200 hover:bg-slate-800"
          >
            Account
          </Link>
          {bypass ? (
            <Link
              role="menuitem"
              to="/login?redirect=%2Fadmin"
              onClick={() => setOpen(false)}
              className="block px-3 py-2 text-slate-200 hover:bg-slate-800"
            >
              Sign in as administrator
            </Link>
          ) : (
            <button
              role="menuitem"
              type="button"
              onClick={() => void signOut()}
              className="block w-full px-3 py-2 text-left text-slate-200 hover:bg-slate-800"
            >
              Sign out
            </button>
          )}
        </div>
      )}
    </div>
  );
}
