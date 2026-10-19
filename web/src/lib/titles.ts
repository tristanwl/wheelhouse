/** The tab and screen-reader title of each page, so a browser history or a tab list is readable. */
const SECTIONS: [prefix: string, title: string][] = [
  ["/admin/services", "Services"],
  ["/admin/features", "Features"],
  ["/admin/users", "Users"],
  ["/admin/sessions", "Sessions"],
  ["/admin/network", "Network"],
  ["/admin/converter", "Media and converter"],
  ["/admin/audit", "Audit log"],
  ["/admin", "Administration"],
  ["/login", "Sign in"],
  ["/setup", "Set up"],
  ["/change-password", "Change password"],
  ["/account", "Account"],
  ["/library", "Library"],
  ["/explore", "Explore"],
  ["/codecs", "Codecs"],
];

/** `Users · wheelhouse` for /admin/users, `Dashboard · wheelhouse` for /; the name alone is the fallback. */
export function titleFor(pathname: string): string {
  if (pathname === "/") return "Dashboard · wheelhouse";
  for (const [prefix, title] of SECTIONS) {
    if (pathname === prefix || pathname.startsWith(prefix + "/")) return `${title} · wheelhouse`;
  }
  return "Page not found · wheelhouse";
}
