// @vitest-environment jsdom
// #173: the reply renderer measured against GitHub Flavored Markdown, case by case. `gfm` is the shape the GFM spec
// (https://github.github.com/gfm/) produces for the input; `ours` is the shape this renderer produces. Where they differ
// the case must say why in `divergence`, and docs/runbooks/markdown.md lists every divergence by name. A change in the
// renderer that alters a shape fails here, and so does a divergence nobody wrote down.
import { readFileSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Markdown } from "./Markdown";

interface Case {
  name: string;
  md: string;
  gfm: string;
  ours: string;
  divergence?: string;
}

// Shape: tags and text, attributes dropped except href; whitespace-only text dropped. The renderer's chrome is folded
// away so that only the document remains: the copy header of a code block and the scroll wrapper of a table.
function shape(html: string): string {
  const host = document.createElement("div");
  host.innerHTML = html;
  const walk = (n: Node): string => {
    if (n.nodeType === Node.TEXT_NODE) return n.textContent!.trim() === "" ? "" : JSON.stringify(n.textContent);
    const el = n as Element;
    if (el.classList.contains("codehead")) return "";
    const kids = Array.from(el.childNodes).map(walk).filter(Boolean).join(" ");
    if (el.classList.contains("md") || el.classList.contains("codeblock") || el.classList.contains("tablewrap")) return kids;
    const href = el.getAttribute("href");
    const tag = el.tagName.toLowerCase();
    return `${tag}${href ? `[${href}]` : ""}(${kids})`;
  };
  return Array.from(host.childNodes).map(walk).filter(Boolean).join(" ");
}

const ours = (md: string) => shape(renderToStaticMarkup(<Markdown text={md} />));

export const CASES: Case[] = [
  { name: "paragraph", md: "Hello there.", gfm: 'p("Hello there.")', ours: 'p("Hello there.")' },
  { name: "emphasis-strong-code", md: "*a* **b** `c`", gfm: 'p(em("a") strong("b") code("c"))', ours: 'p(em("a") strong("b") code("c"))' },
  { name: "safe-link", md: "[docs](https://example.test/a)", gfm: 'p(a[https://example.test/a]("docs"))', ours: 'p(a[https://example.test/a]("docs"))' },
  { name: "ordered-list", md: "1. one\n2. two", gfm: 'ol(li("one") li("two"))', ours: 'ol(li("one") li("two"))' },
  { name: "bullet-list", md: "- one\n- two", gfm: 'ul(li("one") li("two"))', ours: 'ul(li("one") li("two"))' },
  {
    name: "table",
    md: "| a | b |\n|---|---|\n| 1 | 2 |",
    gfm: 'table(thead(tr(th("a") th("b"))) tbody(tr(td("1") td("2"))))',
    ours: 'table(thead(tr(th("a") th("b"))) tbody(tr(td("1") td("2"))))',
  },
  { name: "fenced-code-language", md: "```js\nlet x = 1;\n```", gfm: 'pre(code("let x = 1;"))', ours: 'pre(code("let x = 1;"))' },
  {
    name: "soft-line-break",
    md: "line one\nline two",
    gfm: 'p("line one\\nline two")',
    ours: 'p("line one" br() "line two")',
    divergence: "A single newline is a visible line break. Model replies use newlines as breaks; GFM would join the lines.",
  },
  {
    name: "heading-levels",
    md: "# Title",
    gfm: 'h1("Title")',
    ours: 'h3("Title")',
    divergence: "Headings are demoted by two levels (h1 becomes h3) so a reply never outranks the page's own headings.",
  },
  {
    name: "unsafe-link",
    md: "[x](javascript:alert(1))",
    gfm: 'p(a[javascript:alert(1)]("x"))',
    ours: 'p("[x](javascript:alert(1))")',
    divergence: "Only absolute http(s) and mailto links become links; anything else is shown as its literal text.",
  },
  {
    name: "raw-html",
    md: "<b>bold</b>",
    gfm: 'p(b("bold"))',
    ours: 'p("<b>bold</b>")',
    divergence: "Raw HTML is never parsed: it is shown as text (GFM passes most inline HTML through).",
  },
  {
    name: "nested-list",
    md: "- a\n  - b",
    gfm: 'ul(li("a" ul(li("b"))))',
    ours: 'ul(li("a") li("b"))',
    divergence: "Nested lists are flattened into one level.",
  },
  {
    name: "blockquote-paragraph",
    md: "> quoted",
    gfm: 'blockquote(p("quoted"))',
    ours: 'blockquote("quoted")',
    divergence: "A blockquote holds its text directly, not paragraphs inside it.",
  },
  {
    name: "strikethrough",
    md: "~~gone~~",
    gfm: 'p(del("gone"))',
    ours: 'p("~~gone~~")',
    divergence: "Strikethrough is not rendered.",
  },
  {
    name: "autolink",
    md: "see https://example.test",
    gfm: 'p("see " a[https://example.test]("https://example.test"))',
    ours: 'p("see https://example.test")',
    divergence: "Bare URLs are not turned into links; only explicit [text](url) links are.",
  },
  {
    name: "task-list",
    md: "- [ ] todo",
    gfm: 'ul(li(input() " todo"))',
    ours: 'ul(li("[ ] todo"))',
    divergence: "Task-list checkboxes are shown as their literal [ ] text, never as inputs.",
  },
  {
    name: "setext-heading",
    md: "Title\n=====",
    gfm: 'h1("Title")',
    ours: 'p("Title" br() "=====")',
    divergence: "Setext (underlined) headings are not recognised; only # headings are.",
  },
  {
    name: "math",
    md: "$x^2$",
    gfm: 'p(math("x^2"))',
    ours: 'p("$x^2$")',
    divergence: "Math is not typeset; it is shown as written.",
  },
  {
    name: "table-alignment",
    md: "| a |\n|:-:|\n| 1 |",
    gfm: 'table(thead(tr(th("a"))) tbody(tr(td("1"))))',
    ours: 'table(thead(tr(th("a"))) tbody(tr(td("1"))))',
  },
];

describe("the reply renderer against GFM (#173)", () => {
  for (const c of CASES) {
    it(c.name, () => {
      expect(ours(c.md)).toBe(c.ours);
      if (c.ours === c.gfm) expect(c.divergence, "no divergence when the shapes agree").toBeUndefined();
      else expect(c.divergence, "a difference from GFM must be explained").toBeTruthy();
    });
  }

  it("every divergence is written down in docs/runbooks/markdown.md", () => {
    const doc = readFileSync("../docs/runbooks/markdown.md", "utf8");
    for (const c of CASES.filter((x) => x.divergence)) expect(doc, c.name).toContain(`\`${c.name}\``);
  });

  it("very large input is shown as plain text, not parsed", () => {
    const big = "# x\n".repeat(60_000);
    expect(shape(renderToStaticMarkup(<Markdown text={big} />)).startsWith("pre(")).toBe(true);
  });
});
