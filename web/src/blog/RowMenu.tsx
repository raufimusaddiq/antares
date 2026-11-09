import { useEffect, useRef, useState } from "react";

export interface RowAction {
  label: string;
  /** A link item (View post) goes to an address instead of running something. */
  href?: string;
  danger?: boolean;
  run?: () => void;
}

/**
 * The "⋯" menu of a post row: a button that opens a menu of actions (the design's row-actions frame). The keyboard works as in
 * a menu button pattern: Enter, Space or the arrow keys open it onto its first item, the arrows, Home and End move, Escape
 * closes it and returns to the button, Tab leaves it; a press outside closes it.
 */
export function RowMenu({ label, actions }: { label: string; actions: RowAction[] }) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const items = () => Array.from(list.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? []);

  useEffect(() => {
    if (!open) return;
    items()[0]?.focus();
    const away = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!list.current?.contains(t) && !button.current?.contains(t)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);

  const close = (refocus: boolean) => {
    setOpen(false);
    if (refocus) button.current?.focus();
  };
  const onMenuKey = (e: React.KeyboardEvent) => {
    const all = items();
    const at = all.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => (e.preventDefault(), all[(i + all.length) % all.length]?.focus());
    switch (e.key) {
      case "ArrowDown":
        return go(at + 1);
      case "ArrowUp":
        return go(at - 1);
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
        className="quiet blog-more"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) (e.preventDefault(), setOpen(true));
        }}
      >
        ⋯
      </button>
      {open && (
        <div ref={list} className="blog-menu-list" role="menu" aria-label={label} onKeyDown={onMenuKey}>
          {actions.map((a) =>
            a.href ? (
              <a key={a.label} role="menuitem" tabIndex={-1} href={a.href} className={a.danger ? "danger" : undefined} onClick={() => close(false)}>
                {a.label}
              </a>
            ) : (
              <button
                key={a.label}
                type="button"
                role="menuitem"
                tabIndex={-1}
                className={a.danger ? "danger" : undefined}
                onClick={() => {
                  close(false);
                  a.run?.();
                }}
              >
                {a.label}
              </button>
            ),
          )}
        </div>
      )}
    </div>
  );
}
