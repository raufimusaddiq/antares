import { useEffect, useState, type RefObject } from "react";

// The phone toolbar while typing (specs/006 follow-up, design "Phone / Editor, keyboard open"): on a phone, with the text or the toolbar focused, the toolbar
// is one scrolling row docked right above the on-screen keyboard. It follows the keyboard through the visual viewport, so it is never hidden behind it or
// scrolled off. Wide screens keep the wrapped toolbar at the top of the draft.

const PHONE = "(max-width: 40rem)";

export function useIsPhone(): boolean {
  const query = () => (typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(PHONE) : null);
  const [phone, setPhone] = useState(() => query()?.matches ?? false);
  useEffect(() => {
    const m = query();
    if (!m) return;
    const on = () => setPhone(m.matches);
    on();
    m.addEventListener?.("change", on);
    return () => m.removeEventListener?.("change", on);
  }, []);
  return phone;
}

/** True while the focus is in the text or in the toolbar (a tap on a toolbar button must not count as leaving the text). */
export function useTypingFocus(textSelector: string, barSelector: string): boolean {
  const [typing, setTyping] = useState(false);
  useEffect(() => {
    const read = () => {
      const a = document.activeElement;
      setTyping(!!a && a !== document.body && (a.closest(textSelector) !== null || a.closest(barSelector) !== null));
    };
    const out = () => setTimeout(read, 0); // focus moves in two steps: read where it landed
    document.addEventListener("focusin", read);
    document.addEventListener("focusout", out);
    read();
    return () => {
      document.removeEventListener("focusin", read);
      document.removeEventListener("focusout", out);
    };
  }, [textSelector, barSelector]);
  return typing;
}

/** While `active`, keeps `--kb` on the element at the height of what covers the bottom of the screen (the keyboard), from the visual viewport. */
export function useKeyboardOffset(el: RefObject<HTMLElement | null>, active: boolean): void {
  useEffect(() => {
    const node = el.current;
    if (!node) return;
    if (!active) {
      node.style.removeProperty("--kb");
      return;
    }
    const vv = typeof window !== "undefined" ? window.visualViewport : null;
    const update = () => {
      const covered = vv ? Math.max(0, Math.round(window.innerHeight - vv.height - vv.offsetTop)) : 0;
      node.style.setProperty("--kb", `${covered}px`);
    };
    update();
    vv?.addEventListener("resize", update);
    vv?.addEventListener("scroll", update);
    return () => {
      vv?.removeEventListener("resize", update);
      vv?.removeEventListener("scroll", update);
      node.style.removeProperty("--kb");
    };
  }, [el, active]);
}
