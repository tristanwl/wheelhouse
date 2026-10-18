import { Link } from "react-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { WheelMark } from "./WheelMark";

/** How long the name stays out after page load, and after the pointer leaves. */
const INTRO_MS = 3500;
const LEAVE_MS = 600;

/**
 * The header logo: a ship's wheel with the name beside it. The name shows on
 * page load, then the wheel turns back and winds it in. Hover or keyboard focus
 * rolls it out again; on touch, a tap on the wheel toggles it (the link still
 * works when the name is showing).
 */
export function Logo() {
  const [open, setOpen] = useState(true);
  const [turns, setTurns] = useState(0);
  const openRef = useRef(true);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const set = useCallback((next: boolean) => {
    if (next === openRef.current) return;
    openRef.current = next;
    setOpen(next);
    setTurns((t) => t + (next ? 1 : -1));
  }, []);

  const later = useCallback(
    (ms: number, next: boolean) => {
      clearTimeout(timer.current);
      timer.current = setTimeout(() => set(next), ms);
    },
    [set]
  );

  useEffect(() => {
    later(INTRO_MS, false);
    return () => clearTimeout(timer.current);
  }, [later]);

  const reveal = () => {
    clearTimeout(timer.current);
    set(true);
  };

  return (
    <Link
      to="/"
      aria-label="wheelhouse home"
      data-open={open}
      className="logo"
      onPointerEnter={(e) => e.pointerType !== "touch" && reveal()}
      onPointerLeave={(e) => e.pointerType !== "touch" && later(LEAVE_MS, false)}
      onFocus={reveal}
      onBlur={() => later(LEAVE_MS, false)}
      onClick={(e) => {
        // First tap on a touch screen reveals the name instead of navigating.
        if ((e.nativeEvent as PointerEvent).pointerType === "touch" && !openRef.current) {
          e.preventDefault();
          reveal();
          later(INTRO_MS, false);
        }
      }}
    >
      <WheelMark className="logo-wheel" style={{ transform: `rotate(${turns * 360}deg)` }} />
      <span className="logo-name" aria-hidden="true">
        <span>wheelhouse</span>
      </span>
    </Link>
  );
}
