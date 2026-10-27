// @vitest-environment jsdom
// #173: the reply renderer measured against GitHub Flavored Markdown, case by case. The GFM side is not written by hand:
// it is computed by micromark with its GFM extension (a CommonMark + GFM reference implementation, dev dependency only).
// `ours` is the shape this renderer must produce. Where the two differ the case must say why in `divergence`, and
// docs/runbooks/markdown.md lists every divergence by name. A change in either shape fails here, and so does a divergence
// nobody wrote down or one that is no longer real.
import { readFileSync } from "node:fs";
import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Markdown } from "./Markdown";

interface Case {
  name: string;
  md: string;
  ours: string;
  divergence?: string;
}

// Shape: tags and text, attributes dropped except href; whitespace-only text dropped. The renderer's chrome is folded
// away so that only the document remains: the copy header of a code block and the scroll wrapper of a table.
function shape(html: string): string {
  const host = document.createElement("div");
  host.innerHTML = html;
  const walk = (n: Node): string => {
    // Text as written, without the trailing newlines a block ends with (GFM keeps one inside <code>, for instance).
    if (n.nodeType === Node.TEXT_NODE) return n.textContent!.trim() === "" ? "" : JSON.stringify(n.textContent!.replace(/\n+$/, ""));
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
const gfmOf = (md: string) => shape(micromark(md, { allowDangerousHtml: true, extensions: [gfm()], htmlExtensions: [gfmHtml()] }));

export const CASES: Case[] = [
  { name: "paragraph", md: "Hello there.", ours: 'p("Hello there.")' },
  { name: "emphasis-strong-code", md: "*a* **b** `c`", ours: 'p(em("a") strong("b") code("c"))' },
  { name: "safe-link", md: "[docs](https://example.test/a)", ours: 'p(a[https://example.test/a]("docs"))' },
  { name: "ordered-list", md: "1. one\n2. two", ours: 'ol(li("one") li("two"))' },
  { name: "bullet-list", md: "- one\n- two", ours: 'ul(li("one") li("two"))' },
  {
    name: "table",
    md: "| a | b |\n|---|---|\n| 1 | 2 |",
    ours: 'table(thead(tr(th("a") th("b"))) tbody(tr(td("1") td("2"))))',
  },
  { name: "fenced-code-language", md: "```js\nlet x = 1;\n```", ours: 'pre(code("let x = 1;"))' },
  {
    name: "soft-line-break",
    md: "line one\nline two",
    ours: 'p("line one" br() "line two")',
    divergence: "A single newline is a visible line break. Model replies use newlines as breaks; GFM would join the lines.",
  },
  {
    name: "heading-levels",
    md: "# Title",
    ours: 'h3("Title")',
    divergence: "Headings are demoted by two levels (h1 becomes h3) so a reply never outranks the page's own headings.",
  },
  {
    name: "unsafe-link",
    md: "[x](javascript:alert(1))",
    ours: 'p("[x](javascript:alert(1))")',
    divergence: "Only absolute http(s) and mailto links become links; anything else is shown as its literal text.",
  },
  {
    name: "raw-html",
    md: "<b>bold</b>",
    ours: 'p("<b>bold</b>")',
    divergence: "Raw HTML is never parsed: it is shown as text (GFM passes most inline HTML through).",
  },
  {
    name: "nested-list",
    md: "- a\n  - b",
    ours: 'ul(li("a") li("b"))',
    divergence: "Nested lists are flattened into one level.",
  },
  {
    name: "blockquote-paragraph",
    md: "> quoted",
    ours: 'blockquote("quoted")',
    divergence: "A blockquote holds its text directly, not paragraphs inside it.",
  },
  {
    name: "strikethrough",
    md: "~~gone~~",
    ours: 'p("~~gone~~")',
    divergence: "Strikethrough is not rendered.",
  },
  {
    name: "autolink",
    md: "see https://example.test",
    ours: 'p("see https://example.test")',
    divergence: "Bare URLs are not turned into links; only explicit [text](url) links are.",
  },
  {
    name: "task-list",
    md: "- [ ] todo",
    ours: 'ul(li("[ ] todo"))',
    divergence: "Task-list checkboxes are shown as their literal [ ] text, never as inputs.",
  },
  {
    name: "setext-heading",
    md: "Title\n=====",
    ours: 'p("Title" br() "=====")',
    divergence: "Setext (underlined) headings are not recognised; only # headings are.",
  },
  { name: "math-is-literal", md: "$x^2$", ours: 'p("$x^2$")' },
  {
    name: "table-alignment",
    md: "| a |\n|:-:|\n| 1 |",
    ours: 'table(thead(tr(th("a"))) tbody(tr(td("1"))))',
  },
];

describe("the reply renderer against GFM (#173)", () => {
  for (const c of CASES) {
    it(c.name, () => {
      expect(ours(c.md)).toBe(c.ours);
      const reference = gfmOf(c.md);
      if (c.ours === reference) expect(c.divergence, `no divergence when the shapes agree (GFM: ${reference})`).toBeUndefined();
      else expect(c.divergence, `a difference from GFM must be explained (GFM: ${reference})`).toBeTruthy();
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
