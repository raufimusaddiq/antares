// Real-browser proof that a hostile artifact preview cannot reach the app (PRD §21.8, #83).
//
//   CHROME=/path/to/chrome-headless-shell node scripts/sandbox-probe.mjs
//
// A tiny origin plays the app: it sets a readable cookie and serves a page with a
// marker element. The same hostile payload is framed three ways:
//   - "shipped":  sandbox="" + the CSP that internal/chat/artifacts.go sends (read from the source);
//   - "scripts":  sandbox="allow-scripts", no CSP: the payload RUNS, proving it is the sandbox
//                 (opaque origin) that stops it, not just the absence of scripting;
//   - "control":  sandbox="allow-scripts allow-same-origin": the forbidden combination. The
//                 payload must succeed here, which proves the probe can detect a breach.
// Exit code 1 if the shipped frame leaks anything or the controls do not behave as stated.

import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { readFileSync, mkdtempSync, readdirSync, existsSync } from "node:fs";
import http from "node:http";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";

const src = readFileSync(new URL("../../internal/chat/artifacts.go", import.meta.url), "utf8");
const csp = /const previewCSP = "([^"]+)"/.exec(src)?.[1];
if (!csp) throw new Error("previewCSP not found in artifacts.go");

function chrome() {
  if (process.env.CHROME) return process.env.CHROME;
  const base = join(homedir(), ".cache/ms-playwright");
  for (const d of existsSync(base) ? readdirSync(base).filter((n) => n.startsWith("chromium_headless_shell-")) : []) {
    for (const sub of ["chrome-headless-shell-linux64/chrome-headless-shell", "chrome-linux/headless_shell"]) if (existsSync(join(base, d, sub))) return join(base, d, sub);
  }
  throw new Error("set CHROME to a chrome-headless-shell (or Chromium) binary");
}

const payload = `<p>artifact</p><script>
const r = {};
const t = (k, f) => { try { r[k] = "ALLOWED:" + String(f()); } catch (e) { r[k] = "blocked:" + e.name; } };
t("cookie", () => { const c = document.cookie; if (!c.includes("sid=SECRET")) throw { name: "NoCookie" }; return c; });
t("localStorage", () => localStorage.getItem("k"));
t("parentDOM", () => { parent.document.getElementById("marker").textContent = "PWNED"; return "wrote"; });
t("topNav", () => { top.location.href = "/pwned"; return "navigated"; });
parent.postMessage(JSON.stringify(r), "*");
fetch("/beacon").catch(() => {});
</script>`;

// A second origin the preview must never be able to show: a <meta http-equiv=refresh> needs no script.
const evilHits = [];
const evil = http.createServer((req, res) => (evilHits.push(req.url), res.writeHead(200, { "Content-Type": "text/html" }), res.end("<p>spoofed login</p>")));
await new Promise((r) => evil.listen(0, "127.0.0.1", r));
const evilUrl = `http://127.0.0.1:${evil.address().port}`;
const refresh = (name) => `<meta http-equiv="refresh" content="0;url=${evilUrl}/${name}"><p>artifact</p>`;

const hits = [];
const server = http.createServer((req, res) => {
  hits.push(req.url);
  const send = (type, body, h = {}) => (res.writeHead(200, { "Content-Type": type, ...h }), res.end(body));
  if (req.url === "/") {
    const attr = { shipped: "", scripts: "allow-scripts", control: "allow-scripts allow-same-origin" };
    const frames = Object.entries(attr).map(([n, a]) => `<iframe id="${n}" sandbox="${a}" src="/p/${n}"></iframe>`).join("");
    // The parent carries the app's own frame-src: it, not the preview, is what stops a frame
    // navigating itself to another origin.
    return send("text/html", `<!doctype html><div id="marker">untouched</div><pre id="out"></pre>${frames}<iframe sandbox="" src="/p/refresh-guarded"></iframe>
<script>
const got = [];
addEventListener("message", (e) => { got.push(e.data); document.getElementById("out").textContent = JSON.stringify(got); });
</script>`, { "Set-Cookie": "sid=SECRET; Path=/", "Content-Security-Policy": "frame-src 'self'" });
  }
  // No frame-src on this page: the shipped frame is held by its own sandbox alone, and a frame
  // that is allowed scripts (no CSP) shows the meta refresh is something this probe can see.
  if (req.url === "/open") return send("text/html", `<!doctype html><iframe sandbox="" src="/p/refresh-shipped"></iframe><iframe sandbox="allow-scripts" src="/p/refresh-control"></iframe>`);
  if (req.url === "/p/refresh-guarded") return send("text/html; charset=utf-8", refresh("guarded"), { "Content-Security-Policy": csp });
  if (req.url === "/p/refresh-shipped") return send("text/html; charset=utf-8", refresh("shipped"), { "Content-Security-Policy": csp });
  if (req.url === "/p/refresh-control") return send("text/html; charset=utf-8", refresh("control"));
  if (req.url === "/p/shipped") return send("text/html; charset=utf-8", payload, { "Content-Security-Policy": csp });
  if (req.url.startsWith("/p/")) return send("text/html; charset=utf-8", payload);
  res.writeHead(404).end();
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;

const dir = mkdtempSync(join(tmpdir(), "probe-"));
const run = await promisify(execFile)(chrome(), ["--no-sandbox", "--disable-gpu", `--user-data-dir=${dir}`, "--virtual-time-budget=8000", "--dump-dom", base + "/"], { timeout: 60_000, maxBuffer: 1 << 24 });
const open = await promisify(execFile)(chrome(), ["--no-sandbox", "--disable-gpu", `--user-data-dir=${mkdtempSync(join(tmpdir(), "probe-"))}`, "--virtual-time-budget=8000", "--dump-dom", base + "/open"], { timeout: 60_000, maxBuffer: 1 << 24 });
server.close();
evil.close();
void open;
const dom = run.stdout;
if (process.env.DEBUG) console.log(dom);
const marker = /<div id="marker">([^<]*)<\/div>/.exec(dom)?.[1];
const out = /<pre id="out">([^<]*)<\/pre>/.exec(dom)?.[1] ?? "[]";
const msgs = JSON.parse(out.replace(/&quot;/g, '"').replace(/&amp;/g, "&")).map((m) => JSON.parse(m));
console.log("messages from frames:", msgs);
console.log("marker:", marker, "| requests seen:", hits.join(" "), "| evil saw:", evilHits.join(" "));

const fails = [];
const check = (ok, what) => ok || fails.push(what);
// The marker is rewritten only by the same-origin control; every other frame leaves it alone.
check(marker === "PWNED" && msgs.filter((m) => m.parentDOM === "ALLOWED:wrote").length === 1, "parent DOM written by a frame other than the control");
const scripts = msgs.find((m) => m.cookie === "blocked:SecurityError");
const control = msgs.find((m) => m.cookie?.startsWith("ALLOWED:"));
check(msgs.length === 2, `expected exactly 2 reporting frames (scripts, control), got ${msgs.length}: the shipped frame must be silent`);
check(!!scripts && scripts.parentDOM === "blocked:SecurityError" && scripts.topNav.startsWith("blocked:") && scripts.localStorage === "blocked:SecurityError", "allow-scripts frame did not show the opaque-origin blocks");
check(!!control && control.parentDOM === "ALLOWED:wrote", "control (same-origin) frame did not breach: the probe cannot detect a breach");
check(!hits.includes("/pwned"), "a frame navigated the top page");
// beacon: scripts frame (no CSP) may fetch; the shipped frame's CSP forbids it. Exactly 2 beacons expected (scripts+control).
check(hits.filter((h) => h === "/beacon").length === 2, `beacon count ${hits.filter((h) => h === "/beacon").length}, want 2 (shipped frame must not run or fetch)`);
// Meta refresh needs no script. The shipped frame stays put under the app's frame-src and also
// with no frame-src at all (Chromium holds a script-less sandbox in place; `navigate-to` is not
// implemented anywhere yet). The control, which is allowed scripts and has no CSP, does navigate:
// the probe can see a navigation, so the two silences above mean something.
check(!evilHits.includes("/guarded"), "a preview navigated itself to another origin under the app's frame-src");
check(!evilHits.includes("/shipped"), "the shipped preview navigated itself to another origin with no frame-src");
check(evilHits.includes("/control"), "control: an allow-scripts frame with no CSP did not navigate, so the probe cannot see a meta refresh");
if (fails.length) {
  console.error("FAIL:\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("meta refresh: shipped frame stayed put (with and without frame-src); the allow-scripts control navigated.");
console.log("OK: the shipped preview ran nothing, read nothing, and reached neither the parent DOM nor the top frame; the scripted frame is stopped by its opaque origin; the same-origin control breaches, so the probe can see a breach.");
