import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// The visual contract (web/README.md "Visual contract") as checks on the stylesheet: flat surfaces, a CSP-safe asset policy,
// the same tokens in both themes, and the contrast the contract promises. jsdom applies no CSS, so layout and occlusion are
// measured in a real browser (scripts/browser-check.mjs); this file pins what a text file can prove.
const css = readFileSync(join(__dirname, "styles.css"), "utf8");
const pub = join(__dirname, "..", "public");

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
const tokens = (body: string) => Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
const light = tokens(block(":root {"));
const dark = tokens(block("@media (prefers-color-scheme: dark)"));

describe("flat surfaces, no glass", () => {
  it("has no blur, no radial or ambient layer, no translucent tiers", () => {
    expect(css).not.toMatch(/backdrop-filter/);
    expect(css).not.toMatch(/radial-gradient/);
    expect(css).not.toMatch(/body::(before|after)/);
    expect(css).not.toMatch(/--(chrome|raised|rest|solid|amb)-/);
  });
  it("uses a gradient only as the flat two-stop marker stroke", () => {
    for (const m of css.matchAll(/linear-gradient\(([^;]*)\)/g)) expect(m[1], m[0]).toMatch(/^transparent \d+%, (var\(--mark\)|color-mix\(in srgb, var\(--mark\) \d+%, transparent\)) \d+% \d+%, transparent \d+%$/);
  });
  it("keeps the note above the history in the flow (it must never cover text)", () => {
    const rule = css.match(/\.vstatus\s*\{[^}]*\}/)?.[0] ?? "";
    expect(rule).not.toMatch(/sticky|fixed|absolute|height:\s*0/);
  });
});

describe("assets obey img-src 'self'", () => {
  it("references only same-origin files that exist, never a data: URI", () => {
    expect(css).not.toMatch(/url\(\s*["']?data:/);
    const urls = [...css.matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/g)].map((m) => m[1]);
    expect(urls.length).toBeGreaterThan(10);
    for (const u of urls) {
      expect(u, u).toMatch(/^\/(icons|fonts)\//);
      expect(existsSync(join(pub, u)), `${u} is missing from web/public`).toBe(true);
    }
  });
  it("gives every data-icon used by the markup a mask file", () => {
    const names = new Set([...css.matchAll(/\[data-icon="([\w-]+)"\]\s*\{\s*--icon/g)].map((m) => m[1]));
    for (const used of ["plus", "search", "edit", "regenerate", "rewind", "copy", "clip", "camera", "mic", "bookmark", "bell", "download", "logout", "shield", "arrow-down"]) expect(names.has(used), used).toBe(true);
  });
});

describe("one set of tokens in both themes", () => {
  const colour = ["--canvas", "--rail", "--card", "--ink", "--ink-2", "--hair", "--cobalt", "--cobalt-ink", "--peri", "--mint", "--blush", "--code", "--mark-ink", "--focus"];
  it("the dark theme reassigns every colour the contract names", () => {
    for (const t of colour) {
      expect(light[t], `${t} (light)`).toMatch(/^#[0-9a-f]{6}$/);
      expect(dark[t] ?? light[t], `${t} (dark)`).toMatch(/^#[0-9a-f]{6}$/);
    }
    for (const t of ["--canvas", "--rail", "--card", "--ink", "--ink-2", "--cobalt", "--cobalt-ink", "--peri", "--mint", "--blush"]) expect(dark[t], `${t} must be redefined for dark`).toBeDefined();
  });
});

function lum(hex: string): number {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
const ratio = (a: string, b: string) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

describe("contrast the contract promises (WCAG AA 4.5:1 for text)", () => {
  const pairs: [string, string][] = [
    ["--ink", "--canvas"], ["--ink", "--card"], ["--ink", "--rail"], ["--ink-2", "--canvas"], ["--ink-2", "--card"], ["--ink-2", "--rail"],
    ["--cobalt-ink", "--cobalt"], ["--mark-ink", "--mark"], ["--ink", "--peri"], ["--ink", "--mint"], ["--ink", "--blush"], ["--code-ink", "--code"], ["--cobalt", "--canvas"],
  ];
  for (const [name, theme] of [["light", light], ["dark", { ...light, ...dark }]] as const) {
    for (const [fg, bg] of pairs) {
      it(`${name}: ${fg} on ${bg}`, () => {
        expect(ratio(theme[fg], theme[bg]), `${theme[fg]} on ${theme[bg]}`).toBeGreaterThanOrEqual(4.5);
      });
    }
  }
  it("dark text on the marker is not needed: the dark marker is a translucent block behind --ink", () => {
    expect(dark["--marker-ink"]).toBe("var(--ink)");
  });
});

describe("accessibility fallbacks", () => {
  it("has the contrast and reduced-motion blocks", () => {
    expect(block("@media (prefers-contrast: more)")).toMatch(/--hair:\s*var\(--ink\)/);
    expect(block("@media (prefers-reduced-motion: reduce)")).toMatch(/animation: none/);
  });
});
