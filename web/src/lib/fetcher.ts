import { apiJson } from "./http";

/** The SWR fetcher: a GET through the API client (session, 401/503 handling). */
export function fetcher<T>(url: string): Promise<T> {
  return apiJson<T>(url);
}
