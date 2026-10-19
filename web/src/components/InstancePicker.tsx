import type { InstanceRef } from "@/lib/types";

interface InstancePickerProps {
  instances: InstanceRef[];
  /** The instance being added to. */
  value: string | null;
  onChange: (instanceId: string) => void;
}

/** Choose which Radarr to add to. Shown only when there is a choice. */
export function InstancePicker({ instances, value, onChange }: InstancePickerProps) {
  if (instances.length < 2) return null;
  return (
    <label className="flex items-center gap-1.5 text-sm text-slate-300">
      Add to
      <select
        value={value ?? ""}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-md border border-slate-800 bg-slate-950 px-2 py-1.5 text-sm outline-hidden focus:border-slate-600"
      >
        {instances.map((i) => (
          <option key={i.instanceId} value={i.instanceId}>
            {i.name}
          </option>
        ))}
      </select>
    </label>
  );
}
