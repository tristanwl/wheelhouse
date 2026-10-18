export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

export function formatAge(iso: string): string {
  const addedAt = new Date(iso).getTime();
  if (!Number.isFinite(addedAt)) return "-";
  const days = Math.floor((Date.now() - addedAt) / (1000 * 60 * 60 * 24));
  if (days < 1) return "today";
  if (days < 31) return `${days}d`;
  const months = Math.floor(days / 30);
  if (months < 24) return `${months}mo`;
  const years = Math.floor(days / 365);
  return `${years}y`;
}
