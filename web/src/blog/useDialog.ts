import { useEffect, useRef } from "react";

// A modal sheet's keyboard contract: focus moves in when it opens (to the element marked data-autofocus, so a destructive sheet starts on the safe choice), Tab stays inside it, Escape closes it, and focus
// returns to what had it before.
export function useDialog<T extends HTMLElement>(onClose: () => void) {
  const ref = useRef<T>(null);
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const before = document.activeElement as HTMLElement | null;
    const focusable = () => Array.from(el.querySelectorAll<HTMLElement>("button, input, select, textarea, a[href], [tabindex]:not([tabindex='-1'])")).filter((n) => !n.hasAttribute("disabled"));
    (el.querySelector<HTMLElement>("[data-autofocus]") ?? focusable()[0] ?? el).focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") return close.current();
      if (e.key !== "Tab") return;
      const f = focusable();
      if (f.length === 0) return e.preventDefault();
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) (e.preventDefault(), last.focus());
      else if (!e.shiftKey && document.activeElement === last) (e.preventDefault(), first.focus());
    };
    el.addEventListener("keydown", onKey);
    return () => {
      el.removeEventListener("keydown", onKey);
      before?.focus?.();
    };
  }, []);
  return ref;
}
