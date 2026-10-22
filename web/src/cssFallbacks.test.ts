import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Each accessibility/support fallback works by reassigning tokens. A fill token left out of a block
// stays translucent in that mode (a hover fill with no blur behind it, say), so every block must cover
// every fill token the glass tiers use.
const css = readFileSync(join(__dirname, "styles.css"), "utf8");
const FILLS = ["--chrome-fill", "--raised-fill", "--raised-fill-hover", "--rest-fill", "--solid-fill"];
const AT_RULES = [
  "@media (prefers-reduced-transparency: reduce)",
  "@media (prefers-contrast: more)",
  "@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px)))",
];

function block(header: string): string {
  const start = css.indexOf(header);
  expect(start, `${header} is missing from styles.css`).toBeGreaterThan(-1);
  let depth = 0;
  for (let i = css.indexOf("{", start); i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(start, i + 1);
  }
  throw new Error(`${header} is not closed`);
}

describe("solid fallbacks reassign every fill token", () => {
  for (const header of AT_RULES) {
    it(header, () => {
      const body = block(header);
      for (const token of FILLS) expect(body, `${token} is not reassigned`).toMatch(new RegExp(`${token}:\\s*var\\(--solid`));
    });
  }
});
