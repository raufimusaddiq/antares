import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Plain Ctrl+P (no export in flight, so no .print-root in the page) must print the page as it
// is. Every selector in the print block is therefore tied to the print copy; a bare `body`,
// `#root` or `body.chat` there would blank printing on every route.
const css = readFileSync(join(__dirname, "styles.css"), "utf8");
const start = css.indexOf("@media print {");
const block = css.slice(start, css.indexOf("\n}\n", start)).replace(/\/\*[\s\S]*?\*\//g, "");

describe("print stylesheet", () => {
  it("has a print block", () => expect(start).toBeGreaterThan(0));
  it("only styles the print copy, so ordinary printing is untouched", () => {
    const selectors = [...block.matchAll(/^\s*([^{}\n/][^{}]*)\{/gm)].map((m) => m[1].trim()).filter((s) => !s.startsWith("@media"));
    expect(selectors.length).toBeGreaterThan(5);
    for (const sel of selectors.flatMap((s) => s.split(","))) {
      expect(sel.trim(), sel).toMatch(/^(body(\.chat)?:has\(\.print-root\)|\.print-root|\.print-turn)/);
    }
  });
});
