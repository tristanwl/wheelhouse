interface StatusCardProps {
  name: string;
  up: boolean;
  lines: string[];
  warnings?: string[];
  error?: string;
}

export function StatusCard({ name, up, lines, warnings, error }: StatusCardProps) {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="font-medium">{name}</h2>
        <span
          className={`h-2.5 w-2.5 rounded-full ${up ? "bg-emerald-500" : "bg-red-500"}`}
          title={up ? "up" : "down"}
        />
      </div>
      {up ? (
        <ul className="space-y-1 text-sm text-slate-300">
          {lines.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-red-400">{error ?? "unreachable"}</p>
      )}
      {warnings && warnings.length > 0 && (
        <ul className="mt-2 space-y-1 border-t border-slate-800 pt-2 text-xs text-amber-400">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
