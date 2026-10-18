import { formatRelativeAge } from "@/lib/format";
import { visibleWarnings, type CardWarning, type InstanceState } from "@/lib/status";

interface StatusCardProps {
  name: string;
  state: InstanceState;
  lines: string[];
  warnings?: CardWarning[];
  error?: string;
  /** Current time in ms; only tests pass it. */
  now?: number;
}

const DOT: Record<InstanceState, string> = {
  ok: "bg-emerald-500",
  degraded: "bg-amber-500",
  "needs-setup": "bg-sky-500",
  down: "bg-red-500",
};

const STATE_LABEL: Record<InstanceState, string> = {
  ok: "up",
  degraded: "up, with problems",
  "needs-setup": "needs setup",
  down: "down",
};

// Paths and URLs have no spaces to break at, so every piece of text from a
// service wraps anywhere; min-w-0 lets a flex or grid child shrink to fit.
const WRAP = "min-w-0 break-words [overflow-wrap:anywhere]";

export function StatusCard({
  name,
  state,
  lines,
  warnings,
  error,
  now = Date.now(),
}: StatusCardProps) {
  const shown = visibleWarnings(warnings ?? [], now);
  return (
    <div className="min-w-0 rounded-lg border border-slate-800 bg-slate-900 p-4">
      <div className="mb-2 flex min-w-0 items-center justify-between gap-2">
        <h2 className={`font-medium ${WRAP}`}>{name}</h2>
        <span
          className={`h-2.5 w-2.5 shrink-0 rounded-full ${DOT[state]}`}
          title={STATE_LABEL[state]}
        />
      </div>
      {state === "down" ? (
        <p className={`text-sm text-red-400 ${WRAP}`}>{error ?? "unreachable"}</p>
      ) : (
        <ul className="space-y-1 text-sm text-slate-300">
          {lines.map((line) => (
            <li key={line} className={WRAP}>
              {line}
            </li>
          ))}
        </ul>
      )}
      {state !== "down" && shown.length > 0 && (
        <ul className="mt-2 space-y-1 border-t border-slate-800 pt-2 text-xs">
          {shown.map((w, i) => (
            <li
              key={`${i}:${w.message}`}
              className={`${WRAP} ${w.severity === "error" ? "text-red-400" : "text-amber-400"}`}
              title={w.message}
            >
              <span className="font-medium uppercase">
                {w.severity === "error" ? "Error" : "Warning"}
              </span>
              {w.at && <span className="text-slate-500"> · {formatRelativeAge(w.at, now)}</span>}
              {": "}
              {w.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
