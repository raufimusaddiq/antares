// Generated from docs/design/logo/b-rival.svg, the source of truth. Edit the SVG first, then copy the paths here.
interface Props {
  size?: number;
  /** Set when the mark stands alone; leave unset when a visible wordmark sits beside it. */
  title?: string;
}

export function AntaresMark({ size = 24, title }: Props) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 64 64"
      width={size}
      height={size}
      fill="currentColor"
      focusable="false"
      {...(title ? { role: "img", "aria-label": title } : { "aria-hidden": true })}
    >
      {title && <title>{title}</title>}
      <path d="M28 5h8l24 54H49L32 20 15 59H4z" />
      <circle cx="32" cy="45" r="6.5" />
    </svg>
  );
}
