/**
 * The URL of a library delete. The server refuses a delete without
 * `confirm=true` (issue #41), so this is only to be called after the user has
 * said yes in a confirm dialog; nothing else in the UI builds a delete URL.
 * With a season number only that season's files go; without, the whole item.
 */
export function confirmedDeleteUrl(instanceId: string, id: number, season?: number): string {
  const base = `/api/library/${encodeURIComponent(instanceId)}/${id}`;
  return `${season === undefined ? base : `${base}/seasons/${season}`}?confirm=true`;
}

/**
 * A YouTube key from the server becomes a link that opens in a new tab. It is
 * not embedded: the page's CSP allows no frames (and YouTube refuses an embed
 * without a referrer, which the server never sends).
 */
export function youtubeWatchUrl(key: string | null): string | null {
  return key && /^[\w-]{3,32}$/.test(key) ? `https://www.youtube.com/watch?v=${key}` : null;
}
