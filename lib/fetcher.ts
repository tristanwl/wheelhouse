export async function fetcher<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    // Our routes explain themselves in an `error` field; prefer that to a bare status.
    const body = await res.json().catch(() => null);
    throw new Error(
      typeof body?.error === "string" ? body.error : `request to ${url} failed: ${res.status}`
    );
  }
  return res.json() as Promise<T>;
}
