import { formatBytes } from "@/lib/format";

interface StorageBarProps {
  label: string;
  path: string;
  freeBytes: number;
  libraryUsedBytes: number;
}

export function StorageBar({
  label,
  path,
  freeBytes,
  libraryUsedBytes,
}: StorageBarProps) {
  const accountedFor = freeBytes + libraryUsedBytes;
  const usedPct = accountedFor > 0 ? (libraryUsedBytes / accountedFor) * 100 : 0;
  const lowSpace = freeBytes < 50 * 1024 * 1024 * 1024; // < 50GB

  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <div className="mb-1 flex items-baseline justify-between">
        <h3 className="font-medium">
          {label} <span className="text-xs text-slate-500">{path}</span>
        </h3>
        <span className={`text-sm ${lowSpace ? "text-red-400" : "text-slate-300"}`}>
          {formatBytes(freeBytes)} free
        </span>
      </div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-slate-800">
        <div
          className={`h-full ${lowSpace ? "bg-red-500" : "bg-sky-500"}`}
          style={{ width: `${Math.min(usedPct, 100)}%` }}
        />
      </div>
      <p className="mt-1 text-xs text-slate-500">
        {formatBytes(libraryUsedBytes)} in library
      </p>
    </div>
  );
}
