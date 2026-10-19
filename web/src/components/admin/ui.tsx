import { useId, useState } from "react";
import { BTN, BTN_QUIET, INPUT } from "./styles";

export function PageTitle({
  title,
  children,
  actions,
}: {
  title: string;
  children?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <header className="mb-5 space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-medium">{title}</h1>
        {actions}
      </div>
      {children && <div className="space-y-2 text-sm text-slate-400">{children}</div>}
    </header>
  );
}

/** A failure the administrator needs to see; announced when it appears. */
export function ErrorNote({ children }: { children: React.ReactNode }) {
  return (
    <p
      role="alert"
      className="rounded-md border border-red-900 bg-red-950/40 px-3 py-2 text-sm text-red-300"
    >
      {children}
    </p>
  );
}

/**
 * A polite live region for the result of the last action. The region itself is
 * always in the page, so a screen reader announces text that appears inside it.
 */
export function LiveResult({
  children,
  tone = "good",
}: {
  children?: React.ReactNode;
  tone?: "good" | "bad" | "neutral";
}) {
  const color =
    tone === "good" ? "text-emerald-400" : tone === "bad" ? "text-red-400" : "text-slate-300";
  return (
    <div role="status" aria-live="polite" className={`text-sm ${color}`}>
      {children}
    </div>
  );
}

export function Badge({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: "neutral" | "good" | "warn" | "bad" | "info";
}) {
  const tones = {
    neutral: "bg-slate-800 text-slate-300",
    good: "bg-emerald-950 text-emerald-300",
    warn: "bg-amber-950 text-amber-300",
    bad: "bg-red-950 text-red-300",
    info: "bg-sky-950 text-sky-300",
  } as const;
  return <span className={`rounded px-1.5 py-0.5 text-xs ${tones[tone]}`}>{children}</span>;
}

/** An on/off switch with a visible label; the state is in `aria-checked`. */
export function Switch({
  label,
  checked,
  onChange,
  disabled,
  description,
}: {
  label: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  description?: React.ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        aria-describedby={description ? `${id}-d` : undefined}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 h-5 w-9 shrink-0 rounded-full transition disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sky-500 ${
          checked ? "bg-sky-600" : "bg-slate-700"
        }`}
      >
        <span
          aria-hidden="true"
          className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all ${
            checked ? "left-[1.1rem]" : "left-0.5"
          }`}
        />
      </button>
      <div className="text-sm">
        <label htmlFor={id} className="text-slate-100">
          {label}
        </label>
        {description && (
          <div id={`${id}-d`} className="text-xs text-slate-500">
            {description}
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * A list of one-line values (address ranges, folders) with an input per row,
 * a way to remove a row and a way to add one. `validate` gives the message for
 * a row; messages from the server for the whole list go in `error`.
 */
export function StringListEditor({
  label,
  hint,
  values,
  onChange,
  validate,
  placeholder,
  addLabel,
  showErrors,
  disabled,
}: {
  label: string;
  hint?: React.ReactNode;
  values: string[];
  onChange: (next: string[]) => void;
  validate: (value: string) => string | undefined;
  placeholder?: string;
  addLabel: string;
  /** Show the row messages (after a first try to save). */
  showErrors: boolean;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <fieldset className="space-y-2" aria-describedby={hint ? `${id}-hint` : undefined}>
      <legend className="text-sm text-slate-200">{label}</legend>
      {hint && (
        <div id={`${id}-hint`} className="text-xs text-slate-500">
          {hint}
        </div>
      )}
      {values.length === 0 && <p className="text-sm text-slate-500">None yet.</p>}
      <ul className="space-y-2">
        {values.map((value, i) => {
          const problem = showErrors ? validate(value) : undefined;
          return (
            <li key={i} className="space-y-1">
              <div className="flex gap-2">
                <input
                  aria-label={`${label}, entry ${i + 1}`}
                  aria-invalid={problem ? true : undefined}
                  aria-describedby={problem ? `${id}-e${i}` : undefined}
                  value={value}
                  disabled={disabled}
                  placeholder={placeholder}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => onChange(values.map((v, j) => (j === i ? e.target.value : v)))}
                  className={INPUT}
                />
                <button
                  type="button"
                  disabled={disabled}
                  onClick={() => onChange(values.filter((_, j) => j !== i))}
                  className={BTN_QUIET}
                >
                  Remove <span className="sr-only">entry {i + 1}</span>
                </button>
              </div>
              {problem && (
                <p id={`${id}-e${i}`} className="text-xs text-red-400">
                  {problem}
                </p>
              )}
            </li>
          );
        })}
      </ul>
      <button
        type="button"
        disabled={disabled}
        onClick={() => onChange([...values, ""])}
        className={BTN}
      >
        {addLabel}
      </button>
    </fieldset>
  );
}

/** Copies text to the clipboard; says so, or says it could not. */
export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setState("copied");
    } catch {
      setState("failed");
    }
  }
  return (
    <span className="inline-flex items-center gap-2">
      <button type="button" onClick={() => void copy()} className={BTN}>
        {label}
      </button>
      <span role="status" aria-live="polite" className="text-xs text-slate-400">
        {state === "copied" && "Copied."}
        {state === "failed" && "Could not copy; select the text and copy it yourself."}
      </span>
    </span>
  );
}

/**
 * A password the server will never show again, for the administrator to pass on.
 * It lives only in the page's memory and is gone when this is dismissed.
 */
export function OneTimePassword({
  username,
  password,
  heading,
  onDismiss,
}: {
  username: string;
  password: string;
  heading: string;
  onDismiss: () => void;
}) {
  return (
    <section
      aria-label={heading}
      className="space-y-2 rounded-lg border border-amber-900 bg-amber-950/30 p-4 text-sm"
    >
      <h2 className="font-medium text-amber-200">{heading}</h2>
      <p className="text-slate-300">
        Give this password to <strong>{username}</strong> now. It is shown only once and cannot be
        looked up later. {username} will be asked to choose a new one at the next sign-in.
      </p>
      <p>
        <code
          data-testid="one-time-password"
          className="select-all rounded bg-slate-950 px-2 py-1 font-mono text-base tracking-wide"
        >
          {password}
        </code>
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <CopyButton text={password} label="Copy password" />
        <button type="button" onClick={onDismiss} className={BTN_QUIET}>
          I have passed it on
        </button>
      </div>
    </section>
  );
}
