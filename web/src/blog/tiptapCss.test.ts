// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// The editor (Editor.tsx, injectCSS: false) runs on a copy of @tiptap/core's base rules in styles.css, because the page's
// Content-Security-Policy refuses the inline <style> TipTap would inject. A copy can rot: this fails when an upgrade changes
// the rules, so the copy is re-made instead of the editor silently losing `white-space: pre-wrap` again.
const squash = (s: string) =>
  s
    .replace(/\/\*[\s\S]*?\*\//g, "") // comments
    .replace(/\s+/g, " ")
    .replace(/\s*([{};:,])\s*/g, "$1")
    .trim();

describe("the copy of TipTap's base CSS", () => {
  it("matches the rules @tiptap/core would inject", () => {
    const dist = join(__dirname, "..", "..", "node_modules", "@tiptap", "core", "dist");
    const bundle = readFileSync(join(dist, "index.js"), "utf8");
    const start = bundle.indexOf("const style = `");
    expect(start, "TipTap no longer has a `style` string: check how it injects its CSS").toBeGreaterThan(-1);
    const theirs = squash(bundle.slice(start + "const style = `".length, bundle.indexOf("`;", start)));

    const css = readFileSync(join(__dirname, "..", "styles.css"), "utf8");
    const from = css.indexOf("The editor's own base rules");
    expect(from).toBeGreaterThan(-1);
    // Our copy, with the one deliberate change (the gap cursor takes the theme's ink instead of black).
    const ours = squash(css.slice(css.indexOf("*/", from) + 2)).replace("var(--ink)", "black");
    expect(ours.startsWith(theirs)).toBe(true);
  });
});
