// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { useMemo, useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { makeBridge } from "./draftBridge";
import { Suggestions } from "./Suggestions";
import type { EditSuggestion } from "./types";

let ed: Editor | null = null;
afterEach(() => {
  cleanup();
  ed?.destroy();
  ed = null;
  vi.unstubAllGlobals();
});

const p = (t: string) => ({ type: "paragraph", content: [{ type: "text", text: t }] });
const make = (...paras: string[]) => (ed = new Editor({ element: document.createElement("div"), extensions: [StarterKit], content: { type: "doc", content: paras.map(p) } }));
const sg = (over: Partial<EditSuggestion>): EditSuggestion => ({ id: "e1", kind: "replace", part: 0, anchor: "Running your own assistant sounds cheap.", original: "sounds cheap", proposed: "looks cheap", reason: "Shorter.", decision: "open", ...over });
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { "Content-Type": "application/json" } });
const calls: { url: string; body: unknown }[] = [];
const server = (fail = false) =>
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    const body = JSON.parse(String(init.body));
    calls.push({ url: String(url), body });
    if (fail) return json({ error: { code: "INTERNAL" } }, 500);
    const id = /edits\/(\w+)$/.exec(String(url))![1];
    return json({ id, kind: "replace", part: 0, anchor: "x", original: "o", proposed: "p", reason: "r", decision: body.decision });
  });

function Harness({ edits, editor }: { edits: EditSuggestion[]; editor: Editor }) {
  const [list, setList] = useState(edits);
  const [, setTick] = useState(0);
  editor.on("transaction", () => setTick((n) => n + 1));
  const bridge = useMemo(() => makeBridge(editor), [editor]);
  return <Suggestions postId="p1" edits={list} bridge={bridge} doc={editor.state.doc} onDecided={(e) => setList((l) => l.map((x) => (x.id === e.id ? { ...x, decision: e.decision } : x)))} />;
}

describe("suggestions", () => {
  it("a replace card shows the passage, the proposal and the reason; an insert card says it was written by the assistant", () => {
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({}), sg({ id: "e2", kind: "insert", anchor: "Running your own assistant sounds cheap.", original: "", proposed: "First.\n\nSecond.", reason: "Fills it." })]} />);
    expect(screen.getByText("Suggested edit")).toBeTruthy();
    expect(screen.getByText("sounds cheap").tagName).toBe("S");
    expect(screen.getByText("looks cheap")).toBeTruthy();
    expect(screen.getByText(/Why: Shorter\./)).toBeTruthy();
    expect(screen.getByText("Written by the assistant")).toBeTruthy();
    expect(screen.getByText("First.")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Suggestions (2)" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Accept suggestion 1" })).toBeTruthy();
  });

  it("accepting applies one change to the draft, records the choice, and offers Undo", async () => {
    server();
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({})]} />);
    fireEvent.click(screen.getByRole("button", { name: "Accept suggestion 1" }));
    await waitFor(() => expect(screen.getByText("Accepted")).toBeTruthy());
    expect(e.getText()).toBe("Running your own assistant looks cheap.");
    expect(calls.at(-1)).toMatchObject({ url: "/api/blog/posts/p1/edits/e1", body: { decision: "accepted" } });
    fireEvent.click(screen.getByRole("button", { name: "Undo suggestion 1" }));
    await waitFor(() => expect(screen.getByText("Undone")).toBeTruthy());
    expect(e.getText()).toBe("Running your own assistant sounds cheap.");
  });

  it("rejecting records the choice and changes nothing in the draft", async () => {
    server();
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({})]} />);
    const before = e.getHTML();
    fireEvent.click(screen.getByRole("button", { name: "Reject suggestion 1" }));
    await waitFor(() => expect(screen.getByText("Rejected")).toBeTruthy());
    expect(e.getHTML()).toBe(before);
    expect(calls.at(-1)?.body).toEqual({ decision: "rejected" });
  });

  it("a passage that changed shows as no longer applicable, with no way to accept it", () => {
    const e = make("Running your own assistant is different now.");
    render(<Harness editor={e} edits={[sg({})]} />);
    expect(screen.getByText("No longer applicable")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Accept/ })).toBeNull();
    expect(screen.getByText(/Nothing was applied/)).toBeTruthy();
  });

  it("becomes no longer applicable when the author edits the passage while it is on screen", async () => {
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({})]} />);
    expect(screen.getByRole("button", { name: "Accept suggestion 1" })).toBeTruthy();
    await act(async () => {
      e.commands.setContent({ type: "doc", content: [p("Something else entirely.")] });
    });
    await waitFor(() => expect(screen.getByText("No longer applicable")).toBeTruthy());
  });

  it("moves focus to the next suggestion after a decision, and to the heading after the last", async () => {
    server();
    const e = make("Alpha sounds cheap.", "Beta is fine here.");
    render(<Harness editor={e} edits={[sg({ id: "a", anchor: "Alpha sounds cheap.", original: "sounds cheap", proposed: "is cheap" }), sg({ id: "b", part: 1, anchor: "Beta is fine here.", original: "is fine", proposed: "is good" })]} />);
    fireEvent.click(screen.getByRole("button", { name: "Reject suggestion 1" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Accept suggestion 2" })));
    fireEvent.click(screen.getByRole("button", { name: "Reject suggestion 2" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { name: /Suggestions/ })));
  });

  it("when the choice cannot be saved the text is left as it was and the author is told", async () => {
    server(true);
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({})]} />);
    fireEvent.click(screen.getByRole("button", { name: "Accept suggestion 1" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/Nothing was changed/));
    expect(e.getText()).toBe("Running your own assistant sounds cheap.");
  });

  it("an accepted card shows what was done and Undo, not the old and new text again", async () => {
    server();
    const e = make("Running your own assistant sounds cheap.");
    render(<Harness editor={e} edits={[sg({})]} />);
    fireEvent.click(screen.getByRole("button", { name: "Accept suggestion 1" }));
    await waitFor(() => expect(screen.getByText("Accepted")).toBeTruthy());
    expect(screen.queryByText("You wrote")).toBeNull();
    expect(screen.getByText(/Undo works until you change something else/)).toBeTruthy();
  });

  it("the compact list (the review screen) shows each suggestion as one line with its buttons", () => {
    const e = make("Running your own assistant sounds cheap.");
    render(<Suggestions postId="p1" edits={[sg({})]} bridge={makeBridge(e)} doc={e.state.doc} onDecided={() => {}} compact />);
    expect(screen.getByText("“sounds cheap” → “looks cheap”")).toBeTruthy();
    expect(screen.queryByText("You wrote")).toBeNull();
    expect(screen.getByRole("button", { name: "Reject suggestion 1" })).toBeTruthy();
  });
});
