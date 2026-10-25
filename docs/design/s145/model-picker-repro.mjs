import http from "node:http"; import fs from "node:fs"; import path from "node:path"; import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { chromium } = require(""+(process.env.PLAYWRIGHT_CORE||"playwright-core")+"");
const root = new URL("../../..", import.meta.url).pathname;
const dist = root + "/internal/webui/dist";
const serverGo = fs.readFileSync(root + "/internal/server/server.go", "utf8");
const styleHash = /const openUIStyleHash = "([^"]+)"/.exec(serverGo)[1];
const CSP = /h\.Set\("Content-Security-Policy", (.*)\)\n/.exec(serverGo)[1].replace('"+openUIStyleHash+"', styleHash).slice(1, -1);
const CHAT = "a".repeat(32); const NEW = "c".repeat(32); const chatsMade = []; const models_by = { [CHAT]: null, [NEW]: null };
const st = { capsFail: 0, log: [], model: null, modelsFail: 0, modelsDelay: 0 };
const models = () => ({ source: "kenari", fetchedAt: "2026-10-25T10:00:00Z", stale: false, default: { provider: "p", model: "m-default" }, unsupported: { serviceTier: "none" },
  providers: [{ slug: "p", name: "Prov", available: true, quota: { state: "PROVIDER_USAGE_UNAVAILABLE" }, models: ["m-default", "m-alt", "m-third"].map((id) => ({ id, available: true, hidden: false, reasoning: null, reasoningEfforts: [], modalities: null, contextWindow: null, price: null })) }] });
function api(req, res, url) {
  const send = (s, b) => { res.writeHead(s, { "content-type": "application/json" }); res.end(JSON.stringify(b)); };
  st.log.push(`${req.method} ${url.pathname}${url.search}`);
  if (url.pathname === "/api/me") return send(200, { email: "r@example.test", displayName: "R", requestedAt: "", state: "ACTIVE" });
  if (url.pathname === "/api/capabilities") { if (st.capsFail > 0) { st.capsFail--; return send(503, { error: { code: "HERMES_UNAVAILABLE", message: "x", requestId: "r" } }); } } if (url.pathname === "/api/capabilities") return send(200, { capabilities: [{ id: "models", label: "Models", status: "available", reason: "" }], toolsets: [], providers: [], skills: [] });
  if (url.pathname === "/api/chats" && req.method === "POST") { chatsMade.push(NEW); return send(201, { id: NEW, title: "New chat", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", activeRunId: "" }); }
  if (url.pathname === "/api/chats") return send(200, { chats: [{ id: CHAT, title: "Chat", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", activeRunId: "" }], nextCursor: "" });
  if (url.pathname === "/api/models") { if (st.modelsFail > 0) { st.modelsFail--; return send(503, { error: { code: "HERMES_UNAVAILABLE", message: "x", requestId: "r" } }); } return setTimeout(() => send(200, models()), st.modelsDelay); }
  let mm = /^\/api\/chats\/([0-9a-f]{32})$/.exec(url.pathname); if (mm && req.method === "GET") return send(200, { id: mm[1], title: "Chat", model: models_by[mm[1]] ?? null });
  mm = /^\/api\/chats\/([0-9a-f]{32})\/model$/.exec(url.pathname); if (mm && req.method === "PUT") { let b = ""; req.on("data", (d) => (b += d)); req.on("end", () => { const j = JSON.parse(b); st.model = models_by[mm[1]] = j.reset ? null : { provider: "p", model: j.model, reasoningEffort: j.reasoningEffort || "", serviceTier: "" }; st.log.push("  body " + b + " chat " + mm[1].slice(0,1)); send(200, { model: st.model }); }); return; }
  if (/^\/api\/chats\/[0-9a-f]{32}\/messages$/.test(url.pathname)) return send(200, { messages: [], nextCursor: "", total: 0 });
  if (url.pathname === "/api/me/model-visibility") return send(200, { hidden: [] });
  if (url.pathname === "/api/me/templates") return send(200, { templates: [] });
  if (/draft$/.test(url.pathname)) return send(200, { body: "", revision: 0 });
  if (/usage$/.test(url.pathname)) return send(200, { runs: { finished: 0, reported: 0 }, cost: { amount: null }, context: { windowTokens: null }, budget: { limitTokens: null, usedTokens: null, enforced: false, reached: false, basis: "" }, tokens: null, turns: [], turnsTruncated: false });
  if (/input$/.test(url.pathname)) return send(200, { image: { available: false, reason: "x" } });
  send(404, { error: { code: "NOT_FOUND", message: "x", requestId: "r" } });
}
const server = http.createServer((req, res) => { const url = new URL(req.url, "http://x"); if (url.pathname.startsWith("/api/")) return api(req, res, url);
  let f = path.join(dist, url.pathname === "/" ? "index.html" : url.pathname); if (!fs.existsSync(f)) f = path.join(dist, "index.html");
  const t = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html" }[path.extname(f)] || "application/octet-stream";
  const h = { "content-type": t }; if (f.endsWith("index.html")) h["content-security-policy"] = CSP; res.writeHead(200, h); fs.createReadStream(f).pipe(res); });
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROME });
try {
  for (const [name, vp] of [["desktop", { width: 1280, height: 800 }], ["mobile", { width: 390, height: 780 }]]) {
    const mk = async () => { const ctx = await browser.newContext({ viewport: vp, hasTouch: name === "mobile", isMobile: name === "mobile" }); const page = await ctx.newPage(); return { ctx, page }; };
    const openOpts = async (page) => { const t = page.locator(".extras-toggle"); if (await t.isVisible()) await t.click(); };
    // A: capabilities fails once, then recovers via the Retry button.
    st.capsFail = 1; models_by[CHAT] = null; st.log.length = 0;
    let { ctx, page } = await mk(); await page.goto(base + "/"); await page.waitForTimeout(800);
    await page.getByText("Chat", { exact: true }).first().click(); await page.waitForTimeout(1500); await openOpts(page);
    console.log(name, "A capsFail: picker:", await page.locator("summary", { hasText: /^Model/ }).count(), "retry button:", await page.getByRole("button", { name: "Retry model check" }).count());
    await page.screenshot({ path: `${name}-A-fixed.png` });
    await page.getByRole("button", { name: "Retry model check" }).click(); await page.waitForTimeout(800);
    console.log(name, "A after Retry: picker:", await page.locator("summary", { hasText: /^Model/ }).count(), "caps calls:", st.log.filter(l=>/capabilities/.test(l)).length);
    await ctx.close();
    // B: models fails on first open
    st.capsFail = 0; st.modelsFail = 1; st.log.length = 0;
    ({ ctx, page } = await mk()); await page.goto(base + "/"); await page.waitForTimeout(800);
    await page.getByText("Chat", { exact: true }).first().click(); await page.waitForTimeout(600); await openOpts(page);
    const sum = page.locator("summary", { hasText: /^Model/ });
    await sum.click(); await page.waitForTimeout(600);
    console.log(name, "B first open: alert:", JSON.stringify(await page.locator('details.usage [role=alert]').allInnerTexts()), "retry button:", await page.getByRole("button", { name: /retry|try again/i }).count(), "radios:", await page.locator("input[name=model]").count());
    await page.screenshot({ path: `${name}-B.png` });
    await page.screenshot({ path: `${name}-B-fixed.png` });
    await page.getByRole("button", { name: "Retry" }).click(); await page.waitForTimeout(600);
    console.log(name, "B after Retry radios:", await page.locator("input[name=model]").count(), "alerts:", await page.locator('details.usage [role=alert]').count());
    // select, persist
    await page.getByRole("radio", { name: /m-alt/ }).click(); await page.waitForTimeout(400);
    console.log(name, "B select ->", JSON.stringify(models_by[CHAT]), "label:", await sum.innerText());
    // C: loaded, then refresh fails on reopen -> stale catalog wiped?
    st.modelsFail = 1; await sum.click(); await page.waitForTimeout(200); await sum.click(); await page.waitForTimeout(600);
    console.log(name, "C refresh fails after loaded: radios:", await page.locator("input[name=model]").count(), "alert:", JSON.stringify(await page.locator('details.usage [role=alert]').allInnerTexts()));
    await page.screenshot({ path: `${name}-C-fixed.png` });
    await page.reload(); await page.waitForTimeout(900); await openOpts(page);
    console.log(name, "reload -> label:", await page.locator("summary", { hasText: /^Model/ }).first().innerText());
    await ctx.close();
    // D: New chat, choose a model, it is that chat's own
    ({ ctx, page } = await mk()); await page.goto(base + "/"); await page.waitForTimeout(800);
    const nb = page.getByRole("button", { name: /new chat/i }).first(); console.log(name, "new chat button visible", await nb.isVisible());
    if (!(await nb.isVisible())) { await page.getByRole("button", { name: /menu|chats/i }).first().click().catch(()=>{}); }
    await nb.click().catch((e)=>console.log("NEWCHAT FAIL", e.message.split("\n")[0])); await page.waitForTimeout(700); await openOpts(page);
    const s2 = page.locator("summary", { hasText: /^Model/ }); console.log(name, "D new chat: picker", await s2.count());
    await s2.first().click(); await page.waitForTimeout(500);
    await page.getByRole("radio", { name: /m-third/ }).click(); await page.waitForTimeout(400);
    console.log(name, "D new-chat model:", JSON.stringify(models_by[NEW]), "other chat model:", JSON.stringify(models_by[CHAT]), "label", await s2.first().innerText());
    await page.screenshot({ path: `${name}-D-fixed.png` });
    await ctx.close(); models_by[NEW] = null;
  }
} finally { await browser.close(); server.close(); }
