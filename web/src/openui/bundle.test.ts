import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

// What a page load costs, read from the built bundle (internal/webui/dist, committed and embedded in
// the Go binary). A plain-text chat must not fetch the OpenUI runtime, and a rich reply without math
// must not fetch KaTeX. Both are build-time properties (vite.config.ts), so they are checked here.

const dist = join(__dirname, "..", "..", "..", "internal", "webui", "dist");
const assets = join(dist, "assets");

function entryChunk(): string {
  const html = readFileSync(join(dist, "index.html"), "utf8");
  const entry = /src="\/assets\/([^"]+\.js)"/.exec(html)?.[1];
  expect(entry, "no entry script in the built index.html (run `npm run build` in web/)").toBeTruthy();
  return entry!;
}

/** Every chunk the entry fetches before any lazy import: its static import closure. */
function staticClosure(entry: string): string[] {
  const seen = new Set<string>();
  const walk = (file: string) => {
    if (seen.has(file)) return;
    seen.add(file);
    const src = readFileSync(join(assets, file), "utf8");
    for (const m of src.matchAll(/from"\.\/([^"]+)"/g)) walk(m[1]);
  };
  walk(entry);
  return [...seen];
}

describe("the built bundle", () => {
  it("loads no OpenUI runtime for a plain-text chat", () => {
    for (const file of staticClosure(entryChunk())) {
      const src = readFileSync(join(assets, file), "utf8");
      expect(src, `${file} is fetched by every page load and carries the OpenUI runtime`).not.toContain("rehype-katex");
      expect(src, `${file} is fetched by every page load and carries the OpenUI runtime`).not.toContain("openui-code-block");
    }
  });

  it("fetches KaTeX only for a reply that holds math", () => {
    const chunk = readdirSync(assets).find((f) => f.startsWith("validate-") && f.endsWith(".js"));
    expect(chunk, "no validate chunk in the built bundle (run `npm run build` in web/)").toBeTruthy();
    const src = readFileSync(join(assets, chunk!), "utf8");
    const at = src.lastIndexOf("rehype-katex-");
    expect(at, "the library no longer imports rehype-katex dynamically").toBeGreaterThan(-1);
    expect(src.slice(Math.max(0, at - 300), at), "the KaTeX import is not guarded by a math check").toContain("textMarkdown");
  });

  it("fetches the code highlighter only for a reply that holds code", () => {
    const files = readdirSync(assets);
    const validate = files.find((f) => f.startsWith("validate-") && f.endsWith(".js"))!;
    const prism = files.find((f) => f.startsWith("prism-") && f.endsWith(".js"));
    expect(prism, "no prism chunk: the highlighter is no longer split out").toBeTruthy();
    expect(staticClosure(validate), "a rich reply without code would fetch the highlighter").not.toContain(prism!);
    expect(readFileSync(join(assets, validate), "utf8"), "nothing imports the highlighter lazily").toContain(`assets/${prism}`);
  });

  it("packs every lucide icon into one chunk that no page load fetches", async () => {
    const files = readdirSync(assets).filter((f) => f.endsWith(".js"));
    const icons = files.filter((f) => f.includes("antares-lucide-all"));
    expect(icons, "the icons are no longer one chunk").toHaveLength(1);
    expect(files.length, "per-icon chunks are back").toBeLessThan(100);
    expect(staticClosure(entryChunk()), "every page load would fetch the icon chunk").not.toContain(icons[0]);
    const validate = files.find((f) => f.startsWith("validate-"))!;
    expect(staticClosure(validate), "a rich reply would fetch the icon chunk up front").not.toContain(icons[0]);
    // Hermes can name any lucide icon, and legacy aliases still resolve to the real icon file.
    const map = readFileSync(join(assets, validate), "utf8");
    for (const [name, file] of [["home", "house.js"], ["check-circle", "circle-check-big.js"], ["alert-triangle", "triangle-alert.js"]]) {
      const at = map.search(new RegExp(String.raw`[{,]"?${name}"?:\(\)=>`));
      expect(at, `${name} is not in the icon map`).toBeGreaterThan(-1);
      expect(map.slice(at, at + 200), name).toContain("default[`" + file + "`]");
    }
  });

  it("the icon chunk renders the real icons for home and check-circle", async () => {
    const chunk = readdirSync(assets).find((f) => f.includes("antares-lucide-all"))!;
    const { default: all } = await import(/* @vite-ignore */ pathToFileURL(join(assets, chunk)).href);
    for (const file of ["house.js", "circle-check-big.js", "triangle-alert.js"]) {
      const html = renderToStaticMarkup(createElement(all[file]));
      expect(html, file).toContain("<svg");
      expect(html, file).toContain("lucide-" + file.replace(".js", ""));
    }
    expect(Object.keys(all).length, "the catalogue is not whole").toBeGreaterThan(1500);
  });
});
