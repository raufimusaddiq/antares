import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
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
});
