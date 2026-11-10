// @vitest-environment jsdom
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { afterEach, describe, expect, it } from "vitest";
import { computeFormat } from "./formatState";

let ed: Editor | null = null;
afterEach(() => {
  ed?.destroy();
  ed = null;
});

const text = (t: string, marks?: string[]) => ({ type: "text", text: t, ...(marks ? { marks: marks.map((type) => ({ type })) } : {}) });
const make = (content: object[]) => (ed = new Editor({ element: document.createElement("div"), extensions: [StarterKit.configure({ heading: { levels: [2, 3, 4] } })], content: { type: "doc", content } }));

// positions: a paragraph at 0 holds its text from 1.
describe("coverage of the selection", () => {
  it("a cursor in plain text: everything off; in a bold word: bold on", () => {
    const e = make([{ type: "paragraph", content: [text("plain "), text("bold", ["bold"]), text(" tail")] }]);
    e.commands.setTextSelection(3);
    expect(computeFormat(e).bold).toBe("off");
    e.commands.setTextSelection(9); // inside "bold"
    expect(computeFormat(e)).toMatchObject({ bold: "on", italic: "off", heading2: "off" });
  });

  it("a range inside the mark is on, a range partly inside is mixed, a range outside is off", () => {
    const e = make([{ type: "paragraph", content: [text("plain "), text("bold", ["bold"]), text(" tail")] }]);
    e.commands.setTextSelection({ from: 8, to: 10 });
    expect(computeFormat(e).bold).toBe("on");
    e.commands.setTextSelection({ from: 3, to: 10 });
    expect(computeFormat(e).bold).toBe("mixed");
    e.commands.setTextSelection({ from: 12, to: 15 });
    expect(computeFormat(e).bold).toBe("off");
  });

  it("stored marks: pressing Bold with an empty selection shows on, and again shows off", () => {
    const e = make([{ type: "paragraph", content: [text("abc")] }]);
    e.commands.setTextSelection(2);
    e.commands.toggleBold();
    expect(computeFormat(e).bold).toBe("on");
    e.commands.toggleBold();
    expect(computeFormat(e).bold).toBe("off");
  });

  it("block types: a heading level, lists, a quote and a code block", () => {
    const e = make([
      { type: "heading", attrs: { level: 2 }, content: [text("Head")] },
      { type: "heading", attrs: { level: 3 }, content: [text("Sub")] },
      { type: "bulletList", content: [{ type: "listItem", content: [{ type: "paragraph", content: [text("one")] }] }] },
      { type: "orderedList", content: [{ type: "listItem", content: [{ type: "paragraph", content: [text("first")] }] }] },
      { type: "blockquote", content: [{ type: "paragraph", content: [text("said")] }] },
      { type: "codeBlock", content: [text("x = 1")] },
      { type: "paragraph", content: [text("plain")] },
    ]);
    const at = (needle: string) => {
      let pos = 0;
      e.state.doc.descendants((n, p) => {
        if (n.isText && n.text === needle) pos = p + 1;
      });
      e.commands.setTextSelection(pos);
      return computeFormat(e);
    };
    expect(at("Head")).toMatchObject({ heading2: "on", heading3: "off" });
    expect(at("Sub")).toMatchObject({ heading2: "off", heading3: "on" });
    expect(at("one")).toMatchObject({ bullets: "on", numbers: "off" });
    expect(at("first")).toMatchObject({ numbers: "on", bullets: "off" });
    expect(at("said")).toMatchObject({ quote: "on" });
    expect(at("x = 1")).toMatchObject({ code: "on" });
    expect(at("plain")).toEqual({ bold: "off", italic: "off", heading2: "off", heading3: "off", bullets: "off", numbers: "off", quote: "off", code: "off", link: "off" });
  });

  it("a range over a heading and a paragraph: heading mixed; over two headings: on", () => {
    const e = make([
      { type: "heading", attrs: { level: 2 }, content: [text("Head")] },
      { type: "paragraph", content: [text("body")] },
      { type: "heading", attrs: { level: 2 }, content: [text("More")] },
    ]);
    e.commands.selectAll();
    expect(computeFormat(e).heading2).toBe("mixed");
    e.commands.setTextSelection({ from: 2, to: 3 });
    expect(computeFormat(e).heading2).toBe("on");
  });

  it("is a fast read on a long document (select all in about 2,000 paragraphs)", () => {
    const e = make(Array.from({ length: 2000 }, (_, i) => ({ type: "paragraph", content: [text(`Paragraph number ${i} with some words in it.`, i % 2 ? ["bold"] : undefined)] })));
    e.commands.selectAll();
    const t0 = performance.now();
    expect(computeFormat(e).bold).toBe("mixed");
    expect(performance.now() - t0).toBeLessThan(250);
  });
});
