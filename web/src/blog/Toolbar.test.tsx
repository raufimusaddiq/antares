// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Editor from "./Editor";
import type { Post } from "./types";

// jsdom has no layout: the editor scrolls the caret into view when it takes the focus.
if (!Element.prototype.getClientRects) Element.prototype.getClientRects = () => [] as unknown as DOMRectList;
if (typeof Range !== "undefined" && !Range.prototype.getClientRects) {
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList;
  Range.prototype.getBoundingClientRect = () => new DOMRect();
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.unstubAllGlobals();
});

const doc = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "plain " }, { type: "text", text: "bold", marks: [{ type: "bold" }] }, { type: "text", text: " tail" }] }, { type: "heading", attrs: { level: 2 }, content: [{ type: "text", text: "Head" }] }] };
const post: Post = { id: "p1abcdef", title: "T", summary: "", slug: "", coverUrl: "", status: "draft", latestVersion: 1, publishedVersion: 0, doc: doc as Post["doc"], check: null, updatedAt: "" };
const props = { post, email: "a@example.com", onPost: () => {}, onSettings: () => {}, onPublish: () => {}, onBack: () => {} };

const pressed = (name: string) => screen.getByRole("button", { name }).getAttribute("aria-pressed");

// The toolbar must follow the cursor with no other action (the editor is not re-rendered by a selection change unless the toolbar subscribes).
describe("the formatting toolbar", () => {
  it("every formatting button has a name and follows the selection", async () => {
    vi.stubGlobal("fetch", async () => new Response("{}", { status: 500 }));
    render(<Editor {...props} />);
    const bar = await screen.findByRole("toolbar", { name: "Formatting" });
    for (const b of bar.querySelectorAll("button")) expect(b.getAttribute("aria-label")).toBeTruthy();
    const view = document.querySelector(".ProseMirror") as HTMLElement & { editor?: { commands: { setTextSelection: (p: number | { from: number; to: number }) => void } } };
    expect(view).toBeTruthy();
    const ed = view.editor!;
    await act(async () => ed.commands.setTextSelection(3));
    await waitFor(() => expect(pressed("Bold")).toBe("false"));
    await act(async () => ed.commands.setTextSelection(9));
    await waitFor(() => expect(pressed("Bold")).toBe("true"));
    await act(async () => ed.commands.setTextSelection({ from: 3, to: 10 }));
    await waitFor(() => expect(pressed("Bold")).toBe("mixed"));
    await act(async () => ed.commands.setTextSelection(20));
    await waitFor(() => expect(pressed("Heading")).toBe("true"));
    expect(pressed("Bold")).toBe("false");
    // buttons that are not toggles carry no state
    expect(screen.getByRole("button", { name: "Image" }).hasAttribute("aria-pressed")).toBe(false);
  });
});

// The phone toolbar while typing (design "Phone / Editor, keyboard open").
describe("the phone toolbar while typing", () => {
  const mm = (phone: boolean) =>
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: phone && q.includes("40rem"), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false, onchange: null }));
  const vv = (height: number) => {
    const t = new EventTarget() as EventTarget & { height: number; offsetTop: number };
    t.height = height;
    t.offsetTop = 0;
    Object.defineProperty(window, "visualViewport", { value: t, configurable: true });
    return t;
  };
  afterEach(() => Object.defineProperty(window, "visualViewport", { value: null, configurable: true }));

  it("docks above the keyboard while the text has focus, follows it, and lets go when focus leaves", async () => {
    mm(true);
    const v = vv(window.innerHeight - 300);
    vi.stubGlobal("fetch", async () => new Response("{}", { status: 500 }));
    render(<Editor {...props} />);
    const bar = await screen.findByRole("toolbar", { name: "Formatting" });
    expect(bar.classList.contains("blog-toolbar-docked")).toBe(false);
    const text = document.querySelector(".ProseMirror") as HTMLElement;
    await act(async () => text.focus());
    await waitFor(() => expect(bar.classList.contains("blog-toolbar-docked")).toBe(true));
    expect(bar.style.getPropertyValue("--kb")).toBe("300px");
    await act(async () => {
      v.height = window.innerHeight - 280;
      v.dispatchEvent(new Event("resize"));
    });
    expect(bar.style.getPropertyValue("--kb")).toBe("280px");
    await act(async () => text.blur());
    await waitFor(() => expect(bar.classList.contains("blog-toolbar-docked")).toBe(false));
    expect(bar.style.getPropertyValue("--kb")).toBe("");
  });

  it("a tap on a toolbar button does not take the focus away from the text", async () => {
    mm(true);
    vv(window.innerHeight - 300);
    vi.stubGlobal("fetch", async () => new Response("{}", { status: 500 }));
    render(<Editor {...props} />);
    const bar = await screen.findByRole("toolbar", { name: "Formatting" });
    const ev = new Event("pointerdown", { bubbles: true, cancelable: true });
    screen.getByRole("button", { name: "Bold" }).dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(bar).toBeTruthy();
  });

  it("a wide screen never docks", async () => {
    mm(false);
    vv(window.innerHeight - 300);
    vi.stubGlobal("fetch", async () => new Response("{}", { status: 500 }));
    render(<Editor {...props} />);
    const bar = await screen.findByRole("toolbar", { name: "Formatting" });
    await act(async () => (document.querySelector(".ProseMirror") as HTMLElement).focus());
    expect(bar.classList.contains("blog-toolbar-docked")).toBe(false);
  });
});
