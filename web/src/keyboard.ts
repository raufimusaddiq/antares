// iOS browsers (Safari, and Chrome on iOS) do not resize the page for the on-screen keyboard: the layout viewport keeps
// its height, the keyboard covers the bottom of it, and the browser scrolls the page so the focused field shows. In the
// chat that left the header and the composer on screen and almost none of the conversation, so a reader had to close the
// keyboard to read. Android honours interactive-widget=resizes-content (index.html) and needs none of this.
//
// While the visible area (visualViewport) is much shorter than the layout viewport, the keyboard is up: the chat is
// sized to the visible area (--keyboard-h) and marked data-keyboard="open" on <html>, where styles.css steps the header
// and conversation strip aside on a phone so the conversation keeps the room. The page is kept at the top so the chat's
// own top lines up with the visible area.

/** The visible area is this much shorter than the layout viewport: a keyboard, not a browser bar sliding in. */
export const KEYBOARD_MIN_PX = 150;

export function watchKeyboard(win: Window = window): () => void {
  const vv = win.visualViewport;
  if (!vv) return () => {};
  const root = win.document.documentElement;
  const update = () => {
    const open = win.innerHeight - vv.height > KEYBOARD_MIN_PX;
    if (open) {
      root.dataset.keyboard = "open";
      root.style.setProperty("--keyboard-h", `${Math.round(vv.height)}px`);
      if (win.scrollY !== 0) win.scrollTo(0, 0);
    } else if (root.dataset.keyboard) {
      delete root.dataset.keyboard;
      root.style.removeProperty("--keyboard-h");
    }
  };
  vv.addEventListener("resize", update);
  vv.addEventListener("scroll", update);
  update();
  return () => {
    vv.removeEventListener("resize", update);
    vv.removeEventListener("scroll", update);
    delete root.dataset.keyboard;
    root.style.removeProperty("--keyboard-h");
  };
}
