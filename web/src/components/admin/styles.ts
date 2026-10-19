/** Shared look of the administration pages. */
export const BTN =
  "rounded-md bg-slate-800 px-3 py-1.5 text-sm text-slate-100 hover:bg-slate-700 disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-sky-500";
export const BTN_PRIMARY =
  "rounded-md bg-sky-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-sky-600 disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-sky-300";
export const BTN_QUIET =
  "rounded-md px-2.5 py-1 text-sm text-slate-300 hover:bg-slate-800 hover:text-white disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-sky-500";
export const BTN_DANGER =
  "rounded-md px-2.5 py-1 text-sm text-red-300 hover:bg-slate-800 disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-sky-500";
export const INPUT =
  "w-full rounded-md border border-slate-700 bg-slate-950 px-3 py-2 text-sm outline-hidden focus:border-slate-500 focus-visible:ring-2 focus-visible:ring-sky-600 disabled:opacity-60";
export const CARD = "rounded-lg border border-slate-800 bg-slate-900";

/** The words for something that went wrong, from the server when it explained. */
export function errorText(err: unknown, fallback = "Something went wrong. Try again."): string {
  return err instanceof Error && err.message ? err.message : fallback;
}
