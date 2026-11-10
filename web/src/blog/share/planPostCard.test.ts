import { describe, expect, it } from "vitest";
import { breakLines, isRtl, PANEL_BOTTOM, planPostCard, type Measure, type PostCardInput } from "./planPostCard";

// A stand-in for the canvas: every character is half as wide as the font is high.
const measure: Measure = (font, text, spacing = 0) => {
  const size = Number(/(\d+)px/.exec(font)?.[1] ?? 20);
  return text.length * (size * 0.5 + spacing);
};
const base: PostCardInput = { title: "Self-hosting an assistant: what it really costs", summary: "A small team asked whether running its own assistant beats paying per seat. Here is what we found, with the numbers and where they come from.", highlights: ["The three options", "What drives the difference"], authorName: "Maya R.", hasCover: true };
const long = "word ".repeat(2000);

describe("the story picture's layout", () => {
  // FR-005 / SC-003: the picture is exactly 9:16 for any post. Nothing the plan places may pass the card's bottom edge.
  it.each([
    ["an ordinary post", base],
    ["no cover", { ...base, hasCover: false }],
    ["no summary", { ...base, summary: "" }],
    ["no headings", { ...base, highlights: [] }],
    ["twelve headings", { ...base, highlights: Array.from({ length: 12 }, (_, i) => `Heading number ${i + 1} of the post`).slice(0, 4) }],
    ["a 20,000 word post", { ...base, summary: long, highlights: ["a", "b", "c", "d"] }],
    ["a very long title", { ...base, title: "Self-hosting an assistant for a small team: what it really costs once you count the hours, the tokens and the patching nobody planned for" }],
    ["a title with no spaces", { ...base, title: "x".repeat(300) }],
    ["right to left", { ...base, title: "استضافة مساعد بنفسك", summary: "سألنا فريقاً" }],
  ] as [string, PostCardInput][])("keeps everything inside the card: %s", (_n, input) => {
    const p = planPostCard(input, measure);
    expect(p.contentBottom).toBeLessThanOrEqual(PANEL_BOTTOM - 56);
    expect(p.panel.y + p.panel.h).toBe(PANEL_BOTTOM);
    expect(p.title.lines.length).toBeGreaterThan(0);
    expect(p.title.lines.length).toBeLessThanOrEqual(4);
  });

  it("drops parts in the order of FR-022: the last highlights, then the cover, then the end of the summary", () => {
    const four = ["One heading", "Two heading", "Three heading", "Four heading"];
    const fits = planPostCard({ ...base, highlights: four, summary: "Short summary." }, measure);
    expect(fits.highlights.items).toHaveLength(4);
    expect(fits.cover).toBe(true);
    // a longer summary: the highlights go from the last first, the cover stays while it can
    let seen: number[] = [];
    for (const n of [200, 400, 600, 800, 1000, 1400, 2000]) {
      const p = planPostCard({ ...base, highlights: four, summary: "w".repeat(n).replace(/(.{7})/g, "$1 ") }, measure);
      seen.push(p.highlights.items.length * 10 + (p.cover ? 1 : 0));
      expect(p.highlights.items.map((i) => i.lines.join(" "))).toEqual(four.slice(0, p.highlights.items.length)); // the first ones stay
    }
    // as the summary grows: never gets more highlights back while the cover stays
    const withCover = seen.filter((v) => v % 10 === 1).map((v) => Math.floor(v / 10));
    expect([...withCover].sort((a, b) => b - a)).toEqual(withCover);
    // the title, author and address always remain; a post too long even with nothing else is cut with an ellipsis
    const huge = planPostCard({ ...base, summary: long, highlights: four }, measure);
    expect(huge.cover).toBe(false);
    expect(huge.highlights.items).toHaveLength(0);
    expect(huge.summary.cut).toBe(true);
    expect(huge.summary.lines.at(-1)!.endsWith("…")).toBe(true);
    expect(huge.title.lines.join(" ")).toContain("Self-hosting");
  });

  it("steps the type down before dropping anything", () => {
    const roomy = planPostCard({ ...base, summary: "A short summary." }, measure);
    const tight = planPostCard({ ...base, summary: "word ".repeat(70) }, measure);
    expect(tight.summary.size).toBeLessThan(roomy.summary.size);
    expect(tight.cover).toBe(true);
    expect(tight.highlights.items).toHaveLength(2);
  });

  it("the title steps down and is clamped to four lines with an ellipsis", () => {
    const p = planPostCard({ ...base, title: "word ".repeat(300) }, measure);
    expect(p.title.size).toBe(46);
    expect(p.title.lines).toHaveLength(4);
    expect(p.title.lines[3].endsWith("…")).toBe(true);
  });

  it("detects right-to-left text by its first letter", () => {
    expect(isRtl("استضافة")).toBe(true);
    expect(isRtl("1. שלום")).toBe(true);
    expect(isRtl("Hello اس")).toBe(false);
    expect(planPostCard({ ...base, title: "استضافة" }, measure).rtl).toBe(true);
  });

  it("breaks between words, a language without spaces between words, and inside a word wider than a line", () => {
    const fit = (s: string) => s.length * 10;
    expect(breakLines("alpha beta gamma delta", 100, fit, 9).lines).toEqual(["alpha beta", "gamma", "delta"]);
    expect(breakLines("x".repeat(25), 100, fit, 9).lines).toEqual(["x".repeat(10), "x".repeat(10), "x".repeat(5)]);
    const zh = breakLines("我们研究了服务器上运行助手的成本", 60, fit, 9);
    expect(zh.lines.every((l) => l.length <= 6)).toBe(true);
    expect(zh.lines.join("")).toBe("我们研究了服务器上运行助手的成本");
    const cut = breakLines("one two three four five six", 80, fit, 2);
    expect(cut.cut).toBe(true);
    expect(cut.lines[1].endsWith("…")).toBe(true);
  });
});
