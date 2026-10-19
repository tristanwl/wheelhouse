import { forwardRef, useId } from "react";
import { Outlet } from "react-router";
import { MIN_PASSWORD_LENGTH, passwordStrength } from "@/lib/password";
import { Logo } from "./Logo";

const INPUT =
  "w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm outline-hidden focus:border-slate-500 focus-visible:ring-2 focus-visible:ring-sky-600 disabled:opacity-60";

interface FieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "id"> {
  label: string;
  /** Help text read with the label. */
  hint?: React.ReactNode;
}

/** A labelled input; the hint is tied to it with aria-describedby. */
export const Field = forwardRef<HTMLInputElement, FieldProps>(function Field(
  { label, hint, ...input },
  ref
) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm text-slate-300">
        {label}
      </label>
      <input
        ref={ref}
        id={id}
        aria-describedby={hint ? hintId : undefined}
        className={INPUT}
        {...input}
      />
      {hint && (
        <div id={hintId} className="text-xs text-slate-500">
          {hint}
        </div>
      )}
    </div>
  );
});

/** A form-level error; announced when it appears. */
export const FormError = forwardRef<HTMLParagraphElement, { children: React.ReactNode }>(
  function FormError({ children }, ref) {
    return (
      <p
        ref={ref}
        tabIndex={-1}
        role="alert"
        className="rounded-md border border-red-900 bg-red-950/40 px-3 py-2 text-sm text-red-300 outline-hidden"
      >
        {children}
      </p>
    );
  }
);

export function SubmitButton({
  busy,
  children,
  busyLabel,
}: {
  busy: boolean;
  children: React.ReactNode;
  busyLabel: string;
}) {
  return (
    <button
      type="submit"
      disabled={busy}
      className="w-full rounded-md bg-sky-700 px-3 py-2 text-sm font-medium text-white hover:bg-sky-600 disabled:opacity-60"
    >
      {busy ? busyLabel : children}
    </button>
  );
}

const BAR = ["w-0", "w-1/3 bg-red-500", "w-2/3 bg-amber-500", "w-full bg-emerald-500"] as const;

/** Length requirement and a strength hint for a new password. */
export function PasswordHint({ password }: { password: string }) {
  const s = passwordStrength(password);
  return (
    <div className="space-y-1">
      <div className="h-1 rounded bg-slate-800" aria-hidden="true">
        <div className={`h-1 rounded ${BAR[s.level]}`} />
      </div>
      <p aria-live="polite">
        At least {MIN_PASSWORD_LENGTH} characters. A few unrelated words make a good password.
        {s.label && <span className="ml-1 text-slate-300">{s.label}.</span>}
      </p>
    </div>
  );
}

/** The card the login, setup and forced-change pages sit in: the logo, then the content. */
export function AuthFrame({ children }: { children?: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-md px-4 py-10">
      <div className="mb-6 flex justify-center">
        <Logo />
      </div>
      <div className="rounded-lg border border-slate-800 bg-slate-900 p-5">
        {children ?? <Outlet />}
      </div>
    </div>
  );
}
