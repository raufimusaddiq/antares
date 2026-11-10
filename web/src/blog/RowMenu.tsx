import { useEffect, useRef, useState } from "react";

export interface RowAction {
  label: string;
  /** A stable identity when the label changes while the menu is open (Copy link becomes "Link copied"), so focus stays on the item. */
  id?: string;
  /** A link item (View post) goes to an address instead of running something. */
  href?: string;
  /** A link that leaves the site opens in a new tab and tells the destination nothing about us. */
  external?: boolean;
  danger?: boolean;
  /** A rule is drawn above this item. */
  divider?: boolean;
  /** The menu stays open after this item runs (Copy link turns into "Link copied" in place). */
  keepOpen?: boolean;
  /** Shown as done (the item has just done what it says). */
  done?: boolean;
  run?: () => void;
}

/**
 * The "⋯" menu of a post row: a button that opens a menu of actions (the design's row-actions frame). The keyboard works as in
 * a menu button pattern: Enter, Space or the arrow keys open it onto its first item, the arrows, Home and End move, Escape
 * closes it and returns to the button, Tab leaves it; a press outside closes it.
 */
export function RowMenu({
  label,
  actions,
  trigger = "⋯",
  triggerClass = "quiet blog-more",
  triggerIcon,
}: {
  label: string;
  actions: RowAction[];
  /** What the button shows (default the dots), its class and an optional icon name (data-icon). */
  trigger?: React.ReactNode;
  triggerClass?: string;
  triggerIcon?: string;
}) {
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState<"first" | "last">("first"); // ArrowUp on the closed button opens onto the last item
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const items = () => Array.from(list.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? []);

  useEffect(() => {
    if (!open) return;
    const all = items();
    (at === "last" ? all[all.length - 1] : all[0])?.focus();
    const away = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!list.current?.contains(t) && !button.current?.contains(t)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open, at]);

  const close = (refocus: boolean) => {
    setOpen(false);
    if (refocus) button.current?.focus();
  };
  const onMenuKey = (e: React.KeyboardEvent) => {
    const all = items();
    const idx = all.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => (e.preventDefault(), all[(i + all.length) % all.length]?.focus());
    switch (e.key) {
      case "ArrowDown":
        return go(idx + 1);
      case "ArrowUp":
        return go(idx - 1);
      case "Home":
        return go(0);
      case "End":
        return go(all.length - 1);
      case "Escape":
        e.preventDefault();
        return close(true);
      case "Tab":
        return close(false);
    }
  };

  return (
    <div className="blog-menu">
      <button
        ref={button}
        type="button"
        className={triggerClass}
        data-icon={triggerIcon}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => (setAt("first"), setOpen((o) => !o))}
        onKeyDown={(e) => {
          if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) (e.preventDefault(), setAt(e.key === "ArrowUp" ? "last" : "first"), setOpen(true));
        }}
      >
        {trigger}
      </button>
      {open && (
        <div ref={list} className="blog-menu-list" role="menu" aria-label={label} onKeyDown={onMenuKey}>
          {actions.map((a) => {
            const cls = [a.danger ? "danger" : "", a.done ? "done" : "", a.divider ? "divided" : ""].filter(Boolean).join(" ") || undefined;
            return a.href ? (
              <a
                key={a.id ?? a.label}
                role="menuitem"
                tabIndex={-1}
                href={a.href}
                className={cls}
                {...(a.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
                onClick={() => close(false)}
              >
                {a.label}
              </a>
            ) : (
              <button
                key={a.id ?? a.label}
                type="button"
                role="menuitem"
                tabIndex={-1}
                className={cls}
                onClick={() => {
                  if (!a.keepOpen) close(true); // focus stays on the button (a dialog the action opens returns to it); only a link leaves the page
                  a.run?.();
                }}
              >
                {a.label}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
