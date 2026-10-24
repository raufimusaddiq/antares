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
walk(join(web, "public")); // fonts, icons and the OG card ship in the bundle too
files.push("index.html", "package.json", "package-lock.json", "vite.config.ts", "tsconfig.json", "scripts/stamp.mjs");
files.sort();

const h = createHash("sha256");
for (const f of files) {
  h.update(f);
  h.update("\0");
  h.update(readFileSync(join(web, f)));
  h.update("\0");
}
const version = h.digest("hex");
const dist = join(out, "..");
const css = readFileSync(join(dist, "offline.css"));
const cssPath = `/assets/offline-${createHash("sha256").update(css).digest("hex").slice(0, 16)}.css`;
writeFileSync(join(dist, cssPath), css);
writeFileSync(join(dist, "offline.html"), readFileSync(join(dist, "offline.html"), "utf8").replace("/offline.css", cssPath));
const hashed = readdirSync(join(dist, "assets")).filter(name => /-[\w-]+\.(js|css)$/.test(name)).sort().map(name => `/assets/${name}`);
// Install-time precache is what the shell loads (entry, runtime, CSS); every other chunk is on demand
// and cached by the worker on first use.
const shell = readFileSync(join(dist, "index.html"), "utf8");
const isShell = path => shell.includes(path) || path.startsWith("/assets/offline-");
const precache = ["/offline.html", ...hashed.filter(isShell)];
const optional = hashed.filter(path => !isShell(path));
writeFileSync(join(dist, "sw.js"), readFileSync(join(web, "public", "sw.js"), "utf8")
  .replace("__VERSION__", version).replace("__PRECACHE__", JSON.stringify(precache)).replace("__OPTIONAL__", JSON.stringify(optional)));
writeFileSync(out, version + "\n");
