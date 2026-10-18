interface ModalCloseButtonProps {
  onClose: () => void;
  className?: string;
}

// A dedicated tap target in the corner of every modal — relying on
// "click outside to close" doesn't work well on mobile, where the modal
// often fills most or all of the viewport and there's no backdrop left to
// tap.
export function ModalCloseButton({ onClose, className = "" }: ModalCloseButtonProps) {
  return (
    <button
      onClick={onClose}
      aria-label="Close"
      className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-xl leading-none text-slate-400 hover:bg-slate-800 hover:text-white ${className}`}
    >
      ×
    </button>
  );
}
