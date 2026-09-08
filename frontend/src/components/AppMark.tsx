// AppMark is the OpenCraft brand glyph: the open O ring with the
// terminal-cursor block, wrapped at the bottom with three stitches
// ("craft"/tie-off). It is drawn from the --color-fg/--color-accent
// theme tokens so it flips with .theme-light automatically; the same
// mark (with a tile behind it) is the app icon in build/appicon.svg.
export function AppMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      className={className}
      fill="none"
      aria-hidden="true"
    >
      <rect
        x="3"
        y="3"
        width="26"
        height="26"
        rx="7.8"
        stroke="var(--color-fg)"
        strokeWidth="4.4"
      />
      <rect
        x="12.5"
        y="20.3"
        width="7"
        height="3"
        rx="1.5"
        fill="var(--color-accent)"
      />
      <line
        x1="12.95"
        y1="26.6"
        x2="12.95"
        y2="31.4"
        stroke="var(--color-accent)"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
      <line
        x1="16"
        y1="26.6"
        x2="16"
        y2="31.4"
        stroke="var(--color-accent)"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
      <line
        x1="19.05"
        y1="26.6"
        x2="19.05"
        y2="31.4"
        stroke="var(--color-accent)"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </svg>
  );
}
