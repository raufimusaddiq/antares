// Writes the fingerprint of the web sources next to the built bundle. A Go test
// (internal/webui) recomputes it, so a source change without a rebuild fails
// `go test` on a machine with no Node. Keep the algorithm identical there.
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const web = join(fileURLToPath(import.meta.url), "..", "..");
const out = join(web, "..", "internal", "webui", "dist", "source-hash.txt");

const files = [];
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p);
    else if (!/\.test\.tsx?$/.test(name)) files.push(relative(web, p).split(sep).join("/"));
  }
};
walk(join(web, "src"));
files.push("index.html", "package.json", "package-lock.json", "vite.config.ts", "tsconfig.json");
files.sort();

const h = createHash("sha256");
for (const f of files) {
  h.update(f);
  h.update("\0");
  h.update(readFileSync(join(web, f)));
  h.update("\0");
}
writeFileSync(out, h.digest("hex") + "\n");
