import { useEffect, useId, useRef } from "react";
import { ModalCloseButton } from "./ModalCloseButton";

interface ConfirmModalProps {
  title: string;
  body: React.ReactNode;
  confirmLabel?: string;
  /** What the confirm button says while the action runs. */
  busyLabel?: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/**
 * The confirm dialog every destructive action goes through. Focus starts on
 * Cancel (so Enter never destroys anything by accident), Escape cancels, and
 * focus returns to where it was when the dialog closes.
 */
export function ConfirmModal({
  title,
  body,
  confirmLabel = "Delete",
  busyLabel = "Deleting…",
  busy,
  onConfirm,
  onCancel,
}: ConfirmModalProps) {
  const titleId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    cancelRef.current?.focus();
    return () => before?.focus?.();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [busy, onCancel]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-sm rounded-lg border border-slate-800 bg-slate-900 p-5"
      >
        <div className="mb-2 flex items-start justify-between gap-4">
          <h3 id={titleId} className="font-medium">
            {title}
          </h3>
          <ModalCloseButton onClose={onCancel} className="-mr-1 -mt-1" />
        </div>
        <div className="mb-4 text-sm text-slate-300">{body}</div>
        <div className="flex justify-end gap-2">
          <button
            ref={cancelRef}
            onClick={onCancel}
            disabled={busy}
            className="rounded-md px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={busy}
            className="rounded-md bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500 disabled:opacity-60"
          >
            {busy ? busyLabel : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
