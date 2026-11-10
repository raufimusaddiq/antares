import { describe, expect, it } from "vitest";
import type { DocNode } from "../types";
import { cutAt, highlights, plainOf, summaryText } from "./postContent";

const t = (text: string): DocNode => ({ type: "text", text });
const p = (text: string): DocNode => ({ type: "paragraph", content: [t(text)] });
const h = (text: string, level = 2): DocNode => ({ type: "heading", attrs: { level }, content: [t(text)] });
const doc = (...content: DocNode[]): DocNode => ({ type: "doc", content });

describe("what the story picture says", () => {
  it("uses the author's summary, and the opening text when there is none", () => {
    expect(summaryText("  The author's own summary. ", doc(p("The opening.")))).toBe("The author's own summary.");
    expect(summaryText("", doc(h("A heading"), p("The opening paragraph."), p("Second.")))).toBe("The opening paragraph.");
    expect(summaryText("", doc(h("Only a heading")))).toBe("");
  });

  it("cuts a long text at a sentence end when one falls late enough, otherwise at a word, never mid-word", () => {
    const s = "First sentence is here. Second sentence goes on and on for a while longer than the allowance lets it.";
    expect(cutAt(s, 40)).toBe("First sentence is here.");
    const noSentence = "alpha beta gamma delta epsilon zeta eta theta";
    const cut = cutAt(noSentence, 20);
    expect(cut.endsWith("…")).toBe(true);
    expect(noSentence.startsWith(cut.slice(0, -1))).toBe(true);
    expect(["alpha beta gamma", "alpha beta gamma delta"].some((x) => cut.slice(0, -1) === x)).toBe(true);
    expect(cutAt("short", 20)).toBe("short");
    expect(cutAt("a".repeat(50), 10)).toBe("a".repeat(9) + "…"); // one word longer than the allowance
  });

  it("breaks a language written without spaces between words, never inside one", () => {
    const zh = "我们研究了在自己的服务器上运行助手的真实成本以及每种方案的优缺点";
    const cut = cutAt(zh, 12);
    expect(cut.endsWith("…")).toBe(true);
    expect(zh.startsWith(cut.slice(0, -1))).toBe(true);
    expect(cut.length).toBeLessThanOrEqual(12);
  });

  it("highlights are the post's own headings in order, at most four, each shortened", () => {
    const d = doc(p("intro"), h("One"), p("x"), h("Two", 3), { type: "bulletList", content: [{ type: "listItem", content: [h("Nested")] }] }, h("Four"), h("Five"));
    expect(highlights(d)).toEqual(["One", "Two", "Nested", "Four"]);
    expect(highlights(doc(p("no headings at all")))).toEqual([]);
    const long = highlights(doc(h("word ".repeat(40))));
    expect(long).toHaveLength(1);
    expect(long[0].length).toBeLessThanOrEqual(70);
    expect(long[0].endsWith("…")).toBe(true);
  });

  it("every word drawn occurs in the post, and markup characters stay plain text", () => {
    const d = doc(p("A <script>alert(1)</script> & b"), h("Heading <b>x</b>"));
    const all = plainOf(d);
    for (const piece of [summaryText("", d), ...highlights(d)]) for (const w of piece.replace(/…/g, "").split(/\s+/).filter(Boolean)) expect(all.includes(w), w).toBe(true);
    expect(summaryText("", d)).toContain("<script>");
  });
});
