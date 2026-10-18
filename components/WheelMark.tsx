/** The ship's wheel mark. Draws in `currentColor`; the hub dot is cut out in the page ground. */
export function WheelMark({
  className,
  style,
}: {
  className?: string;
  style?: React.CSSProperties;
}) {
  return (
    <svg
      className={className}
      style={style}
      viewBox="0 0 64 64"
      aria-hidden="true"
      focusable="false"
    >
      <g fill="none" stroke="currentColor" strokeLinecap="round">
        <circle cx="32" cy="32" r="17" strokeWidth="4.5" />
        <circle cx="32" cy="32" r="12.2" strokeWidth="1.2" opacity="0.55" />
        <g strokeWidth="3.4">
          <path d="M32 15V5M32 49v10M15 32H5M49 32h10" />
          <path d="M20 20l-7-7M44 44l7 7M44 20l7-7M20 44l-7 7" />
        </g>
      </g>
      <g fill="currentColor">
        <circle cx="32" cy="3.6" r="2.6" />
        <circle cx="32" cy="60.4" r="2.6" />
        <circle cx="3.6" cy="32" r="2.6" />
        <circle cx="60.4" cy="32" r="2.6" />
        <circle cx="11.9" cy="11.9" r="2.6" />
        <circle cx="52.1" cy="52.1" r="2.6" />
        <circle cx="52.1" cy="11.9" r="2.6" />
        <circle cx="11.9" cy="52.1" r="2.6" />
        <circle cx="32" cy="32" r="5.2" />
      </g>
      <circle cx="32" cy="32" r="2" className="fill-slate-950" />
    </svg>
  );
}
