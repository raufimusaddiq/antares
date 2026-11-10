// @vitest-environment jsdom
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { afterEach, describe, expect, it } from "vitest";
import { makeBridge } from "./draftBridge";
import type { EditSuggestion } from "./types";

let ed: Editor | null = null;
afterEach(() => {
  ed?.destroy();
  ed = null;
});

const p = (...c: object[]) => ({ type: "paragraph", content: c });
const t = (text: string, marks?: string[]) => ({ type: "text", text, ...(marks ? { marks: marks.map((type) => ({ type })) } : {}) });
const make = (content: object[]) => (ed = new Editor({ element: document.createElement("div"), extensions: [StarterKit.configure({ heading: { levels: [2, 3, 4] } })], content: { type: "doc", content } }));
const text = (e: Editor) => e.getText({ blockSeparator: "|" });
const edit = (over: Partial<EditSuggestion>): EditSuggestion => ({ id: "e1", kind: "replace", part: 0, anchor: "", original: "", proposed: "", reason: "r", decision: "open", ...over });

describe("applying a suggestion in the editor", () => {
  it("replaces only the span it names, in one transaction undone by one undo", () => {
    const e = make([p(t("Running your own assistant sounds cheap until the bill arrives.")), p(t("A closing paragraph."))]);
    const b = makeBridge(e);
    let transactions = 0;
    e.on("transaction", () => transactions++);
    const s = edit({ anchor: "Running your own assistant sounds cheap until the bill arrives.", original: "sounds cheap until the bill arrives", proposed: "looks cheap until the first bill" });
    expect(b.applicable(s)).toBe(true);
    expect(b.apply(s)).toBe("applied");
    expect(transactions).toBe(1);
    expect(text(e)).toBe("Running your own assistant looks cheap until the first bill.|A closing paragraph.");
    expect(b.canUndo("e1")).toBe(true);
    expect(b.undo("e1")).toBe(true);
    expect(text(e)).toBe("Running your own assistant sounds cheap until the bill arrives.|A closing paragraph.");
  });

  it("finds the part again after paragraphs were added above it", () => {
    const e = make([p(t("New first.")), p(t("Old first.")), p(t("The part, with a claim."))]);
    const s = edit({ part: 1, anchor: "The part, with a claim.", original: "with a claim", proposed: "with proof" });
    expect(makeBridge(e).apply(s)).toBe("applied");
    expect(text(e)).toBe("New first.|Old first.|The part, with proof.");
  });

  it("keeps the marks around the replaced span", () => {
    const e = make([p(t("plain "), t("bold word", ["bold"]), t(" tail"))]);
    const s = edit({ anchor: "plain bold word tail", original: "plain bold", proposed: "X" });
    expect(makeBridge(e).apply(s)).toBe("applied");
    expect(text(e)).toBe("X word tail");
    expect(e.getHTML()).toContain("<strong>");
    expect(e.getHTML()).toContain(" tail");
  });

  it("inserts paragraphs after the part, and undoes them in one step", () => {
    const e = make([{ type: "heading", attrs: { level: 2 }, content: [t("Hardware")] }, p(t("Closing."))]);
    const b = makeBridge(e);
    const s = edit({ id: "e2", kind: "insert", part: 0, anchor: "Hardware", proposed: "First new paragraph.\n\nSecond new paragraph." });
    expect(b.apply(s)).toBe("applied");
    expect(text(e)).toBe("Hardware|First new paragraph.|Second new paragraph.|Closing.");
    expect(b.undo("e2")).toBe(true);
    expect(text(e)).toBe("Hardware|Closing.");
  });

  it("is not applicable when the passage changed, and touches nothing", () => {
    const e = make([p(t("The part, with a claim."))]);
    const b = makeBridge(e);
    e.commands.setContent({ type: "doc", content: [p(t("The part, now with a different claim."))] });
    const s = edit({ anchor: "The part, with a claim.", original: "with a claim", proposed: "with proof" });
    const before = e.getHTML();
    expect(b.applicable(s)).toBe(false);
    expect(b.apply(s)).toBe("not_applicable");
    expect(e.getHTML()).toBe(before);
  });

  it("is not applicable when the span was edited inside the part, or the part is gone", () => {
    const e = make([p(t("Keep this anchor. The span is here."))]);
    const b = makeBridge(e);
    const s = edit({ anchor: "Keep this anchor. The span is here.", original: "The span is here", proposed: "Changed" });
    e.commands.setTextSelection({ from: 24, to: 28 });
    e.commands.insertContent("XX");
    expect(b.applicable(s)).toBe(false); // the anchor no longer matches either
    const gone = make([p(t("Something else."))]);
    expect(makeBridge(gone).applicable(s)).toBe(false);
  });

  it("two suggestions about different passages both apply; one about a changed passage does not", () => {
    const e = make([p(t("Alpha part one.")), p(t("Beta part two."))]);
    const b = makeBridge(e);
    expect(b.apply(edit({ id: "a", anchor: "Alpha part one.", original: "part one", proposed: "first" }))).toBe("applied");
    expect(b.apply(edit({ id: "b", part: 1, anchor: "Beta part two.", original: "part two", proposed: "second" }))).toBe("applied");
    expect(text(e)).toBe("Alpha first.|Beta second.");
    expect(b.apply(edit({ id: "c", anchor: "Alpha part one.", original: "part one", proposed: "again" }))).toBe("not_applicable");
  });

  it("an undo is only offered while nothing else has changed", () => {
    const e = make([p(t("Alpha part one."))]);
    const b = makeBridge(e);
    b.apply(edit({ anchor: "Alpha part one.", original: "part one", proposed: "first" }));
    e.commands.insertContent("!");
    expect(b.canUndo("e1")).toBe(false);
    expect(b.undo("e1")).toBe(false);
  });
});
