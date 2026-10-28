import type { ReactNode } from "react";

/**
 * A panel is a disclosure (its summary opens it), or a page of the Settings dialog, which is mounted only while shown
 * and so is always open. `onToggle` reports the disclosure's state; a page never calls it.
 */
export function Panel(props: { page: boolean; className: string; title: string; onToggle?: (open: boolean) => void; children: ReactNode }) {
  if (props.page) {
    return (
      <section className={`${props.className}`} aria-label={props.title}>
        {props.children}
      </section>
    );
  }
  return (
    <details className={`${props.className}`} onToggle={(e) => e.target === e.currentTarget && props.onToggle?.(e.currentTarget.open)}>
      <summary>{props.title}</summary>
      {props.children}
    </details>
  );
}
