/**
 * Where to go after signing in. Only a path inside this app is accepted, so a
 * crafted `?redirect=` cannot send someone to another site (`//example.com`,
 * `/\example.com`, `https://...`, `javascript:`), and the login and setup pages
 * are never a destination.
 */
export function safeRedirect(raw: string | null | undefined, fallback = "/"): string {
  if (!raw || !raw.startsWith("/")) return fallback;
  // eslint-disable-next-line no-control-regex
  if (/[\\\u0000-\u001f\u007f]/.test(raw)) return fallback; // backslash, control characters
  let url: URL;
  try {
    url = new URL(raw, "http://wheelhouse.invalid");
  } catch {
    return fallback;
  }
  if (url.origin !== "http://wheelhouse.invalid") return fallback;
  if (/^\/(login|setup)(\/|$)/.test(url.pathname)) return fallback;
  const result = `${url.pathname}${url.search}${url.hash}`;
  // URL normalisation can turn "/.//evil.example/x" into "//evil.example/x",
  // which a browser reads as another site. Check the result, not only the input.
  // eslint-disable-next-line no-control-regex
  if (result.startsWith("//") || /[\\\u0000-\u001f\u007f]/.test(result)) return fallback;
  return result;
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
