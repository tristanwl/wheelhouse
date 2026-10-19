import { useId, useState } from "react";
import type { FormField, FormValue, FormValues, SecretState } from "@/lib/admin";
import { BTN_QUIET, INPUT } from "./styles";

interface SchemaFormProps {
  fields: FormField[];
  values: FormValues;
  onChange: (name: string, value: FormValue) => void;
  /** Per field: does the server hold a value, and is the administrator replacing it. Absent on a new service. */
  secrets?: Record<string, SecretState>;
  onReplaceSecret?: (name: string, replacing: boolean) => void;
  /** Messages by field name (our own checks and the server's). */
  errors: Record<string, string>;
  disabled?: boolean;
}

/**
 * The settings of one service, generated from its provider's JSON Schema (see
 * `fieldsOf`). Text and address fields are plain inputs; a secret is a masked
 * input with a Show/Hide button and is never filled in with the stored value:
 * on a saved service it reads "set" with a Replace button, and is only sent
 * when replaced.
 */
export function SchemaForm({
  fields,
  values,
  onChange,
  secrets,
  onReplaceSecret,
  errors,
  disabled,
}: SchemaFormProps) {
  return (
    <div className="space-y-4">
      {fields.map((f) => (
        <FieldRow
          key={f.name}
          field={f}
          value={values[f.name]}
          onChange={(v) => onChange(f.name, v)}
          secret={secrets?.[f.name]}
          onReplace={(r) => onReplaceSecret?.(f.name, r)}
          error={errors[f.name]}
          disabled={disabled}
        />
      ))}
    </div>
  );
}

function FieldRow({
  field,
  value,
  onChange,
  secret,
  onReplace,
  error,
  disabled,
}: {
  field: FormField;
  value: FormValue | undefined;
  onChange: (v: FormValue) => void;
  secret?: SecretState;
  onReplace: (replacing: boolean) => void;
  error?: string;
  disabled?: boolean;
}) {
  const id = useId();
  const errId = `${id}-err`;
  const [shown, setShown] = useState(false);
  const label = (
    <label htmlFor={id} className="block text-sm text-slate-300">
      {field.label}
      {field.required ? (
        <span className="ml-1 text-slate-500"> (required)</span>
      ) : (
        <span className="ml-1 text-slate-500"> (optional)</span>
      )}
    </label>
  );
  const err = error && (
    <p id={errId} className="text-xs text-red-400">
      {error}
    </p>
  );

  if (field.kind === "boolean") {
    return (
      <div className="space-y-1">
        <label className="flex items-center gap-2 text-sm text-slate-300">
          <input
            id={id}
            type="checkbox"
            checked={value === true}
            disabled={disabled}
            onChange={(e) => onChange(e.target.checked)}
          />
          {field.label}
        </label>
        {err}
      </div>
    );
  }

  if (field.kind === "secret") {
    // A saved secret is not shown; the administrator can replace it.
    if (secret?.stored && !secret.replacing) {
      return (
        <div className="space-y-1">
          <p className="text-sm text-slate-300">{field.label}</p>
          <div className="flex flex-wrap items-center gap-3">
            <span
              className="rounded bg-slate-800 px-2 py-1 text-sm text-slate-300"
              data-testid={`secret-state-${field.name}`}
            >
              Set (hidden)
            </span>
            <button
              type="button"
              disabled={disabled}
              onClick={() => onReplace(true)}
              className={BTN_QUIET}
            >
              Replace <span className="sr-only">{field.label}</span>
            </button>
          </div>
          <p className="text-xs text-slate-500">
            The stored value is kept unless you replace it. It cannot be shown again.
          </p>
        </div>
      );
    }
    return (
      <div className="space-y-1">
        {label}
        <div className="flex gap-2">
          <input
            id={id}
            type={shown ? "text" : "password"}
            value={typeof value === "string" ? value : ""}
            disabled={disabled}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? errId : undefined}
            onChange={(e) => onChange(e.target.value)}
            className={INPUT}
          />
          <button
            type="button"
            aria-pressed={shown}
            onClick={() => setShown((s) => !s)}
            className={BTN_QUIET}
          >
            {shown ? "Hide" : "Show"} <span className="sr-only">{field.label}</span>
          </button>
          {secret?.stored && (
            <button
              type="button"
              onClick={() => {
                onChange("");
                setShown(false);
                onReplace(false);
              }}
              className={BTN_QUIET}
            >
              Keep the current one
            </button>
          )}
        </div>
        {err}
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {label}
      <input
        id={id}
        type={field.kind === "url" ? "url" : "text"}
        inputMode={field.kind === "integer" ? "numeric" : undefined}
        value={typeof value === "string" ? value : ""}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        placeholder={field.kind === "url" ? "http://host:port" : undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errId : undefined}
        onChange={(e) => onChange(e.target.value)}
        className={INPUT}
      />
      {err}
    </div>
  );
}
