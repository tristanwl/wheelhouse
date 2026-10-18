export async function fetcher<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`request to ${url} failed: ${res.status}`);
  }
  return res.json() as Promise<T>;
}
