/**
 * Where to go after signing in. Only a path inside this app is accepted, so a
 * crafted `?redirect=` cannot send someone to another site (`//example.com`,
 * `/\example.com`, `https://...`, `javascript:`), and the login and setup pages
 * are never a destination.
 */
export function safeRedirect(raw: string | null | undefined, fallback = "/"): string {
  if (!raw || !raw.startsWith("/")) return fallback;
  let url: URL;
  try {
    url = new URL(raw, "http://wheelhouse.invalid");
  } catch {
    return fallback;
  }
  if (url.origin !== "http://wheelhouse.invalid") return fallback;
  if (/^\/(login|setup)(\/|$)/.test(url.pathname)) return fallback;
  return `${url.pathname}${url.search}${url.hash}`;
}

/** The forced password-change screen, remembering where the person was headed. */
export function passwordChangeUrl(from: string): string {
  const target = safeRedirect(from);
  return target === "/"
    ? "/change-password"
    : `/change-password?redirect=${encodeURIComponent(target)}`;
}

/** `/login?redirect=<where you were>`, leaving the parameter out for the Dashboard. */
export function loginUrl(from: string): string {
  const target = safeRedirect(from);
  return target === "/" ? "/login" : `/login?redirect=${encodeURIComponent(target)}`;
}
