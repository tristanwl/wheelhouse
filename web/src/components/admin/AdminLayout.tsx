import { NavLink, Outlet } from "react-router";

const SECTIONS = [
  { to: "/admin/services", label: "Services" },
  { to: "/admin/features", label: "Features" },
  { to: "/admin/users", label: "Users" },
  { to: "/admin/sessions", label: "Sessions" },
  { to: "/admin/network", label: "Network" },
  { to: "/admin/converter", label: "Media and converter" },
  { to: "/admin/audit", label: "Audit log" },
];

/** The frame of every administration page: a sub-navigation and the page. */
export function AdminLayout() {
  return (
    <div className="md:flex md:gap-8">
      <nav aria-label="Administration" className="mb-6 shrink-0 md:mb-0 md:w-48">
        <p className="mb-2 hidden text-xs font-medium uppercase tracking-wide text-slate-500 md:block">
          Administration
        </p>
        <ul className="flex gap-1 overflow-x-auto md:flex-col">
          {SECTIONS.map((s) => (
            <li key={s.to} className="shrink-0">
              <NavLink
                to={s.to}
                className={({ isActive }) =>
                  `block rounded-md px-3 py-1.5 text-sm whitespace-nowrap focus-visible:outline-2 focus-visible:outline-sky-500 ${
                    isActive
                      ? "bg-slate-800 text-white"
                      : "text-slate-400 hover:bg-slate-900 hover:text-white"
                  }`
                }
              >
                {s.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
      <div className="min-w-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}
