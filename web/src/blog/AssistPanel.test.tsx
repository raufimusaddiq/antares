// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AssistPanel } from "./AssistPanel";
import { makeBridge } from "./draftBridge";
import type { Check } from "./types";

let ed: Editor | null = null;
afterEach(() => {
  cleanup();
  ed?.destroy();
  ed = null;
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
const editor = () => (ed = new Editor({ element: document.createElement("div"), extensions: [StarterKit], content: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Running your own assistant sounds cheap." }] }] } }));
const prog = { stage: "searching", stageSince: "2026-11-11T10:00:00Z", lastEventAt: "2026-11-11T10:00:05Z", lookups: 2, startedAt: "2026-11-11T10:00:00Z", now: "2026-11-11T10:00:10Z" };
const running = { id: "c1", state: "running", reason: "", notes: [], notices: [], blocks: [], edits: [], progress: prog };
const ready = (edits: unknown[] = []): Check => ({ id: "c1", state: "ready", reason: "", notes: [], notices: [], blocks: [], edits: edits as Check["edits"], progress: { ...prog, stage: "writing" } });
const edit = { id: "e1", kind: "replace", part: 0, anchor: "Running your own assistant sounds cheap.", original: "sounds cheap", proposed: "looks cheap", reason: "Shorter.", decision: "open" };
const mount = (over: Partial<React.ComponentProps<typeof AssistPanel>> = {}) => {
  const e = editor();
  return render(<AssistPanel postId="p1" initial={null} editor={e} bridge={makeBridge(e)} flush={async () => true} paused={false} canCheck {...over} />);
};

describe("the editor panel", () => {
  it("starts a check after saving the draft, then shows the progress", async () => {
    const flush = vi.fn(async () => true);
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => (init.method === "POST" && String(url).endsWith("/check") ? json(running, 202) : json({}, 500)));
    mount({ flush });
    fireEvent.click(screen.getByRole("button", { name: "Check my post" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "Checking with the editor" })).toBeTruthy());
    expect(flush).toHaveBeenCalled();
    expect(document.querySelector(".step-current")?.textContent).toContain("Looking things up · 2 lookups");
  });

  it("does not start when the draft is not saved yet, and says why", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    mount({ flush: async () => false });
    fireEvent.click(screen.getByRole("button", { name: "Check my post" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/not saved yet/));
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("says when the limit is reached, when to try again, and that Publish original still works", async () => {
    vi.stubGlobal("fetch", async () => json({ error: { code: "LIMIT_REACHED", message: "x" }, retryAt: "2026-11-11T14:20:00Z" }, 422));
    mount();
    fireEvent.click(screen.getByRole("button", { name: "Check my post" }));
    await waitFor(() => expect(screen.getByText("No more checks right now")).toBeTruthy());
    expect(document.body.textContent).toMatch(/check again after \d/);
    expect(document.body.textContent).toContain("publish your original");
    vi.unstubAllGlobals();
    vi.stubGlobal("fetch", async () => json({ error: { code: "LIMIT_REACHED", message: "x" }, running: true }, 422));
  });

  it("a check with nothing to suggest says so, and a very short draft says there is too little to edit", () => {
    mount({ initial: ready([]) });
    expect(screen.getByRole("heading", { name: "Too little to edit yet" })).toBeTruthy(); // the test draft has seven words
    cleanup();
    ed?.destroy();
    const long = "word ".repeat(60);
    const e = (ed = new Editor({ element: document.createElement("div"), extensions: [StarterKit], content: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: long }] }] } }));
    render(<AssistPanel postId="p1" initial={ready([])} editor={e} bridge={makeBridge(e)} flush={async () => true} paused={false} canCheck />);
    expect(screen.getByRole("heading", { name: "Nothing to suggest" })).toBeTruthy();
    expect(document.body.textContent).toContain("Your draft is as you wrote it");
  });

  it("two checks running now says that instead of a time", async () => {
    vi.stubGlobal("fetch", async () => json({ error: { code: "LIMIT_REACHED", message: "x" }, running: true }, 422));
    mount();
    fireEvent.click(screen.getByRole("button", { name: "Check my post" }));
    await waitFor(() => expect(document.body.textContent).toContain("Two checks are already running"));
  });

  it("shows the suggestions of a finished check, and a decision does not leave the panel", async () => {
    vi.stubGlobal("fetch", async (_u: string, init: RequestInit) => json({ ...edit, decision: JSON.parse(String(init.body)).decision }));
    mount({ initial: ready([edit]) });
    expect(screen.getByRole("heading", { name: "Suggestions (1)" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reject suggestion 1" }));
    await waitFor(() => expect(screen.getByText("Rejected")).toBeTruthy());
    expect(screen.getByRole("button", { name: "Check again" })).toBeTruthy();
  });

  it("does not poll while the Publish screen is open over the editor", async () => {
    vi.useFakeTimers();
    const fetchSpy = vi.fn(async () => json(running));
    vi.stubGlobal("fetch", fetchSpy);
    mount({ initial: running as unknown as Check, paused: true });
    await act(async () => void vi.advanceTimersByTime(6000));
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("polls a running check and moves to the suggestions when it finishes", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", async () => json(ready([edit])));
    mount({ initial: running as unknown as Check });
    await act(async () => void vi.advanceTimersByTime(1600));
    await act(async () => void vi.advanceTimersByTime(50));
    expect(screen.getByRole("heading", { name: "Suggestions (1)" })).toBeTruthy();
  });

  it("moves focus to its heading when it opens", () => {
    mount();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Check with the editor" }));
  });
});
