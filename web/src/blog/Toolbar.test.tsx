// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Editor from "./Editor";
import type { Post } from "./types";

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
