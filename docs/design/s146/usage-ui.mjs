// Browser evidence for /admin/usage (#146). Serves the built bundle with the server's CSP against a
// disposable in-script mock of GET /api/admin/kenari-usage. Every figure is fabricated; no Kenari, no key.
// Run: OUT=docs/design/s146/shots CHROME=<chromium> PLAYWRIGHT_CORE=<path> node docs/design/s146/usage-ui.mjs
import http from "node:http"; import fs from "node:fs"; import path from "node:path"; import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { chromium } = require("" + (process.env.PLAYWRIGHT_CORE || "playwright-core") + "");
const root = new URL("../../..", import.meta.url).pathname;
const dist = root + "/internal/webui/dist";
const serverGo = fs.readFileSync(root + "/internal/server/server.go", "utf8");
const styleHash = /const openUIStyleHash = "([^"]+)"/.exec(serverGo)[1];
const CSP = /h\.Set\("Content-Security-Policy", (.*)\)\n/.exec(serverGo)[1].replace('"+openUIStyleHash+"', styleHash).slice(1, -1);
const row = (o) => ({ requests: 120, promptTokens: 480000, completionTokens: 96000, cachedPromptTokens: 0, cacheWritePromptTokens: 0, costMicroIdr: 15400000, catalogMicroIdr: 15400000, ...o });
const ok = (over = {}) => ({ state: "OK", source: "kenari.id: GET /v1/account/balance, /v1/account/quota, /v1/usage (30 days)", scope: "shared-account", fetchedAt: "2026-10-25T10:00:00Z", stale: false,
  balance: { state: "OK", data: { balanceMicroIdr: 25000000000, reservedMicroIdr: 1500000, availableMicroIdr: 24998500000, currency: "IDR" } },
  quota: { state: "OK", data: { alertPct: 80, coupon: null, plan: { name: "Indie", windows: [{ key: "week", budgetRp: 75000, usedRp: 12000, remainingRp: 63000, usedFrac: 0.16, resetsAt: "2026-10-30T07:12:00Z" }, { key: "month", budgetRp: 300000, usedRp: 12000, remainingRp: 288000, usedFrac: 0.04, resetsAt: "2026-11-30T07:12:00Z" }] } } },
  usage: { state: "OK", data: { start: 1788300000, end: 1790892000, rows: [row({ model: "model-one", billingKind: "payg" }), row({ model: "model-two", billingKind: "plan", costMicroIdr: 0 })], total: row({ requests: 240, costMicroIdr: 15400000 }) } }, ...over });
const modes = {
  ok: () => [200, ok()],
  stale: () => [200, ok({ stale: true })],
  refused: () => [200, ok({ balance: { state: "REFUSED", code: "restricted_key_not_allowed", data: null }, quota: { state: "REFUSED", code: "restricted_key_not_allowed", data: null } })],
  notconfigured: () => [200, { state: "NOT_CONFIGURED", source: "x", scope: "shared-account", stale: false }],
  unavailable: () => [200, { state: "UNAVAILABLE", source: "x", scope: "shared-account", stale: false }],
  error: () => [503, { error: { code: "INTERNAL", message: "x", requestId: "r" } }],
  forbidden: () => [403, { error: { code: "FORBIDDEN", message: "x", requestId: "r" } }],
};
let mode = "ok"; const log = [];
const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  const send = (s, b) => { res.writeHead(s, { "content-type": "application/json" }); res.end(JSON.stringify(b)); };
  if (url.pathname === "/__mode") { mode = url.searchParams.get("m"); return send(200, {}); }
  if (url.pathname === "/api/admin/kenari-usage") { log.push(req.method + " " + url.pathname + url.search + " auth=" + (req.headers.authorization ? "PRESENT" : "none")); const [s, b] = modes[mode](); return send(s, b); }
  if (url.pathname === "/api/me") return send(200, { email: "admin@example.test", displayName: "Admin", requestedAt: "", state: "ACTIVE", canAdmin: true });
  if (url.pathname.startsWith("/api/")) return send(404, { error: { code: "NOT_FOUND", message: "x", requestId: "r" } });
  let f = path.join(dist, url.pathname === "/" ? "index.html" : url.pathname); if (!fs.existsSync(f)) f = path.join(dist, "index.html");
  const t = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html" }[path.extname(f)] || "application/octet-stream";
  const h = { "content-type": t }; if (f.endsWith("index.html")) h["content-security-policy"] = CSP; res.writeHead(200, h); fs.createReadStream(f).pipe(res);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROME });
const out = process.env.OUT || "."; fs.mkdirSync(out, { recursive: true });
try {
  for (const [name, vp] of [["desktop", { width: 1280, height: 900 }], ["mobile", { width: 390, height: 780 }]]) {
    const ctx = await browser.newContext({ viewport: vp, hasTouch: name === "mobile", isMobile: name === "mobile" });
    const page = await ctx.newPage();
    const errors = []; page.on("pageerror", (e) => errors.push(String(e))); page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
    for (const m of Object.keys(modes)) {
      await fetch(`${base}/__mode?m=${m}`);
      await page.goto(base + "/admin/usage"); await page.waitForSelector("main.admin-main :is(h1,p,[role=alert])", { timeout: 8000 });
      await page.waitForFunction(() => !document.querySelector("[aria-busy=true]"), null, { timeout: 8000 });
      const text = (await page.locator("main.admin-main").innerText()).replace(/\s+/g, " ").trim();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      console.log(`${name} ${m}: hOverflow=${overflow} :: ${text.slice(0, 260)}`);
      await page.screenshot({ path: `${out}/${name}-${m}.png`, fullPage: true });
    }
    // refresh asks the server for ?refresh=1 and a later failure keeps what was on screen
    await fetch(`${base}/__mode?m=ok`); await page.goto(base + "/admin/usage"); await page.getByRole("button", { name: "Refresh" }).waitFor();
    await page.getByRole("button", { name: "Refresh" }).click(); await page.waitForTimeout(400);
    console.log(`${name} refresh requests: ${JSON.stringify(log.slice(-2))}`);
    console.log(`${name} page errors: ${errors.length ? errors.join(" | ") : "none"}`);
    await ctx.close();
  }
} finally { await browser.close(); server.close(); }
