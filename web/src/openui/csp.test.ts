import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// The Go server's CSP allows one inline <style>, by hash: the spinner keyframes
// @openuidev/react-lang injects when its Renderer mounts. If a library upgrade
// changes that text this fails, and the hash in internal/server/server.go must be
// updated deliberately (never replaced by 'unsafe-inline').
describe("CSP style hash matches the pinned OpenUI runtime", () => {
  it("is the hash of the stylesheet the Renderer injects", () => {
    const dir = join(__dirname, "..", "..", "node_modules", "@openuidev", "react-lang", "dist");
    const src = readdirSync(dir)
      .filter((f) => f.endsWith(".mjs") && !f.includes("native"))
      .map((f) => readFileSync(join(dir, f), "utf8"))
      .join("\n");
    // `const style = document.createElement("style"); style.textContent = `...`;` (a template without ${} holes)
    const injected = [...src.matchAll(/createElement\("style"\);\s*\w+\.textContent = `([^`$]*)`/g)].map((m) => m[1]);
    expect(injected.length, "the runtime no longer injects a <style>: remove openUIStyleHash from server.go").toBeGreaterThan(0);
    const goSrc = readFileSync(join(__dirname, "..", "..", "..", "internal", "server", "server.go"), "utf8");
    const allowed = /const openUIStyleHash = "'sha256-([^']+)'"/.exec(goSrc)?.[1];
    expect(allowed).toBeTruthy();
    const hashes = injected.map((t) => createHash("sha256").update(t).digest("base64"));
    expect(hashes, "every inline style the runtime injects must be allowed, and nothing else").toEqual([allowed]);
  });
});
