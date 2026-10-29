// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { KEYBOARD_MIN_PX, watchKeyboard } from "./keyboard";

/** A window whose visual viewport can shrink like iOS's when the keyboard opens. */
function fakeWindow(innerHeight: number) {
  const vv = Object.assign(new EventTarget(), { height: innerHeight });
  const win = { innerHeight, visualViewport: vv, document, scrollY: 0, scrollTo: vi.fn() } as unknown as Window;
  const resize = (h: number) => {
    vv.height = h;
    vv.dispatchEvent(new Event("resize"));
  };
  return { win, resize };
}

const root = document.documentElement;
afterEach(() => {
  delete root.dataset.keyboard;
  root.style.removeProperty("--keyboard-h");
});

describe("the iOS keyboard (visual viewport shorter than the layout)", () => {
  it("marks the page and sizes the chat to the visible area while the keyboard is up, and undoes both when it closes", () => {
    const { win, resize } = fakeWindow(844);
    const stop = watchKeyboard(win);
    expect(root.dataset.keyboard).toBeUndefined();
    resize(420.4);
    expect(root.dataset.keyboard).toBe("open");
    expect(root.style.getPropertyValue("--keyboard-h")).toBe("420px");
    resize(844);
    expect(root.dataset.keyboard).toBeUndefined();
    expect(root.style.getPropertyValue("--keyboard-h")).toBe("");
    resize(400);
    stop();
    expect(root.dataset.keyboard).toBeUndefined();
  });
  it("a browser bar sliding in is not a keyboard", () => {
    const { win, resize } = fakeWindow(844);
    watchKeyboard(win)();
    const stop = watchKeyboard(win);
    resize(844 - KEYBOARD_MIN_PX);
    expect(root.dataset.keyboard).toBeUndefined();
    stop();
  });
  it("brings a page the browser scrolled for the keyboard back to the top", () => {
    const { win, resize } = fakeWindow(844);
    (win as unknown as { scrollY: number }).scrollY = 300;
    const stop = watchKeyboard(win);
    resize(400);
    expect(win.scrollTo).toHaveBeenCalledWith(0, 0);
    stop();
  });
});
