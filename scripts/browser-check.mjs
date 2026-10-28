#!/usr/bin/env node
// Real-browser check of the chat UI (issue #24): a 500-turn conversation opens
// on the newest page, scrolls back without a stall, and an older page prepended
// above the viewport does not move the message being read; desktop and mobile
// layouts; OpenUI loads only when a message needs it and falls back safely.
//
// The browser talks to a small in-script server that serves the built bundle
// (internal/webui/dist) with the same CSP as the Go server and mocks /api with
// the documented contract. It exercises the UI, not the Go backend (which
// internal/chat tests and the Phase 1 gate cover). No Hermes, no credentials.
//
//   cd web && npm ci && npm run build && cd ..
//   PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core node scripts/browser-check.mjs [--out DIR]
//   (CHROME=/path/to/chromium selects the browser; ONLY=mobile-layout runs just the phone layout checks, which CI runs)
//
// playwright-core and a Chromium are deliberately not dependencies of web/.

import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { measure, verdicts, CURRENT, pointerCheck } from "../web/scripts/mobile-layout.mjs";
import { unexpectedMessages } from "../web/scripts/known-csp.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const dist = process.env.DIST ? path.resolve(process.env.DIST) : path.join(here, "..", "internal", "webui", "dist");
const outDir = process.argv.includes("--out") ? process.argv[process.argv.indexOf("--out") + 1] : fs.mkdtempSync("/tmp/antares-browser-");
fs.mkdirSync(outDir, { recursive: true });
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_CORE || "playwright-core");

// ---- the mock backend ---------------------------------------------------------------

// The Go server's own policy, read from its source so this check cannot drift from it.
const serverGo = fs.readFileSync(path.join(here, "..", "internal", "server", "server.go"), "utf8");
const styleHash = /const openUIStyleHash = "([^"]+)"/.exec(serverGo)[1];
const CSP = /h\.Set\("Content-Security-Policy", (.*)\)\n/.exec(serverGo)[1].replace('"+openUIStyleHash+"', styleHash).slice(1, -1);
const TURNS = 500;
const PAGE = 50;
const PLAIN = "a".repeat(32);
const RICH = "b".repeat(32);

const TABLE = 'root = Stack([Heading("Compare"), Table(["Option","Cost"], [["A","$1"],["B","$2"],["C","$3"]]), Card("Notes", [Text("A is cheapest"), Badge("recommended")])])';
const HOSTILE = 'root = Stack([Text("ok"), Button("go", @OpenUrl("https://evil.example"))])';

function transcript(rich) {
  const m = [];
  for (let i = 0; i < TURNS; i++) {
    const paras = 1 + ((i * 7) % 9); // 1..9 paragraphs: heights differ a lot between turns
    let body = Array.from({ length: paras }, (_, p) => `Paragraph ${p + 1} of answer ${i}. ` + "Words ".repeat(12 + ((i + p) % 20))).join("\n\n");
    if (rich && i === TURNS - 2) body += "\n\n```openui\n" + HOSTILE + "\n```";
    if (rich && i === TURNS - 1) body += "\n\n```openui\n" + TABLE + "\n```";
    // #173: Markdown mixed around the OpenUI fence, with a very long code line and a wide table, so the phone layouts
    // prove neither pushes the page sideways (they scroll inside their own box).
    if (rich && i === TURNS - 1) body += "\n\n```sh\n" + "echo " + "very-long-argument-".repeat(40) + "\n```\n\n| " + Array.from({ length: 12 }, (_, k) => `column ${k}`).join(" | ") + " |\n|" + "---|".repeat(12) + "\n| " + Array.from({ length: 12 }, (_, k) => `value ${k} ${"w".repeat(14)}`).join(" | ") + " |\n\nAfter the table.";
    m.push({ seq: 2 * i, role: "user", text: `question ${i}` }, { seq: 2 * i + 1, role: "assistant", text: `answer ${i}\n\n${body}` });
  }
  return m;
}
const DATA = { [PLAIN]: transcript(false), [RICH]: transcript(true) };
const b64 = (s) => Buffer.from(s).toString("base64url");

const state = { log: [], holdOlder: null, delayMs: 15, email: "reader@example.test", budgetReached: false, emptyList: false, assets: new Map(), deleted: [], sent: [], failNextUpload: false, runs: new Map() };
// A slow runner answers later than a fast one. MOCK_DELAY_MS adds that latency to the image endpoints of the mock (upload, delete, send) so
// the ordering a slow CI runner produces can be reproduced on demand. Unset (the default) the mock answers at once.
const MOCK_DELAY = Number(process.env.MOCK_DELAY_MS || 0);
const later = (ms, fn) => (ms > 0 ? void setTimeout(fn, ms) : fn());
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
const hex32 = () => Array.from({ length: 32 }, () => "0123456789abcdef"[Math.floor(Math.random() * 16)]).join("");

function api(req, res, url) {
  const send = (status, body) => {
    res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
    res.end(JSON.stringify(body));
  };
  state.log.push(`${req.method} ${url.pathname}${url.search}`);
  if (url.pathname === "/api/me") return send(200, { email: state.email, displayName: "Reader", requestedAt: "", state: "ACTIVE" });
  // Image upload, removal and send (the #138 gap): the same shapes the Go server documents, no model behind them.
  if (url.pathname === "/api/assets" && req.method === "POST") {
    const chunks = [];
    req.on("data", (d) => chunks.push(d));
    req.on("end", () => {
      if (state.failNextUpload) { state.failNextUpload = false; return send(415, { error: { code: "UNSUPPORTED_MEDIA", message: "x", requestId: "r" } }); }
      const body = Buffer.concat(chunks);
      const name = body.subarray(PNG.length).toString("latin1"); // see png() in images(): the file's name follows the image's last byte
      // "one" is the first file chosen, and the slower to arrive under MOCK_DELAY_MS: uploads of one message are concurrent and may register in either order.
      later(MOCK_DELAY * (name === "one" ? 3 : 1), () => {
        const id = hex32();
        state.assets.set(id, { type: req.headers["content-type"], size: body.length, name });
        send(201, { id });
      });
    });
    return;
  }
  let am = /^\/api\/assets\/([0-9a-f]{32})$/.exec(url.pathname);
  if (am && req.method === "DELETE") { later(MOCK_DELAY, () => { state.deleted.push(am[1]); state.assets.delete(am[1]); res.writeHead(204); res.end(); }); return; }
  if (am && req.method === "GET") { res.writeHead(200, { "content-type": "image/png", "cache-control": "no-store" }); return void res.end(PNG); }
  am = /^\/api\/chats\/([0-9a-f]{32})\/messages$/.exec(url.pathname);
  if (am && req.method === "POST") {
    let body = "";
    req.on("data", (d) => (body += d));
    req.on("end", () => {
      const b = JSON.parse(body || "{}");
      const runId = hex32();
      state.sent.push({ chat: am[1], input: b.input, imageAssetIds: b.imageAssetIds ?? [], key: req.headers["idempotency-key"] });
      state.runs.set(runId, { chat: am[1], input: b.input, ids: b.imageAssetIds ?? [] });
      later(MOCK_DELAY, () => send(202, { runId }));
    });
    return;
  }
  am = /^\/api\/runs\/([0-9a-f]{32})\/events$/.exec(url.pathname);
  if (am && state.runs.has(am[1])) {
    const r = state.runs.get(am[1]);
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-store" });
    const ev = (n, d) => res.write(`event: ${n}\ndata: ${JSON.stringify(d)}\n\n`);
    if (!r.recorded) {
      // What the server would now hold for this chat, so the refetch after completion shows the same turns.
      r.recorded = true;
      const all = DATA[r.chat];
      all.push({ seq: all.length, role: "user", text: r.input, ...(r.ids.length ? { attachments: r.ids.map((assetId) => ({ assetId, mediaType: "image/png", available: true })) } : {}) });
      all.push({ seq: all.length, role: "assistant", text: `Received ${r.ids.length} image(s) and: ${r.input}` });
    }
    ev("run.started", {});
    ev("assistant.delta", { text: `Received ${r.ids.length} image(s) and: ${r.input}` });
    ev("run.completed", { status: "COMPLETED", output: `Received ${r.ids.length} image(s) and: ${r.input}` });
    return void res.end();
  }
  am = /^\/api\/runs\/([0-9a-f]{32})$/.exec(url.pathname);
  if (am) return state.runs.has(am[1]) ? send(200, { runId: am[1], status: "COMPLETED" }) : send(404, { error: { code: "NOT_FOUND", message: "x", requestId: "r" } });
  if (url.pathname === "/api/chats") {
    if (state.emptyList) return send(200, { chats: [], nextCursor: "" });
    const c = (id, title) => ({ id, title, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", activeRunId: "" });
    return send(200, { chats: [c(PLAIN, "Plain conversation"), c(RICH, "Rich conversation")], nextCursor: "" });
  }
  const m = /^\/api\/chats\/([0-9a-f]{32})\/messages$/.exec(url.pathname);
  if (m && DATA[m[1]]) {
    const all = DATA[m[1]];
    const cur = url.searchParams.get("cursor");
    let end = all.length;
    if (cur) end = Number(Buffer.from(cur, "base64url").toString().split(".")[1]);
    const start = Math.max(0, end - PAGE);
    const reply = () => send(200, { messages: all.slice(start, end), nextCursor: start > 0 ? b64(`v1.${start}`) : "", total: all.length });
    const hold = cur && state.holdOlder;
    if (hold) return void hold.then(reply);
    return void setTimeout(reply, state.delayMs);
  }
  // The rest of what the chat screen reads on load, with the real response shapes, so no failure notice distorts the layout.
  if (url.pathname === "/api/capabilities") return send(200, { capabilities: [], toolsets: [], providers: [], skills: [] });
  if (url.pathname === "/api/me/templates") return send(200, { templates: [] });
  if (/^\/api\/(me\/new-chat-draft|chats\/[0-9a-f]{32}\/draft)$/.test(url.pathname)) {
    if (req.method === "DELETE") { res.writeHead(204); return void res.end(); }
    return send(200, { body: "", revision: 0 });
  }
  // After a reply completes the chat is read once more, to follow a run its queue started; this mock has no queue.
  const cm = /^\/api\/chats\/([0-9a-f]{32})$/.exec(url.pathname);
  if (cm && req.method === "GET") return send(200, { id: cm[1], title: "", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", activeRunId: "" });
  if (/^\/api\/chats\/[0-9a-f]{32}\/queue$/.test(url.pathname)) return send(200, { items: [], paused: "" });
  if (/^\/api\/chats\/[0-9a-f]{32}\/usage$/.test(url.pathname)) return send(200, { runs: { finished: 0, reported: 0 }, cost: { amount: null }, context: { windowTokens: null }, budget: state.budgetReached ? { limitTokens: 10000, usedTokens: 20000, enforced: true, reached: true, basis: "" } : { limitTokens: null, usedTokens: null, enforced: false, reached: false, basis: "" }, tokens: state.budgetReached ? { input: 1, output: 1, total: 2 } : null, turns: [], turnsTruncated: false });
  // Images on, as on the owner's account, so the composer is laid out with Attach image and Take photo.
  if (/^\/api\/chats\/[0-9a-f]{32}\/input$/.test(url.pathname)) return send(200, { image: { available: true, reason: null, model: "mock-vision", maxImages: 4, maxTotalBytes: 6291456 } });
  return send(404, { error: { code: "NOT_FOUND", message: "x", requestId: "r" } });
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  if (url.pathname.startsWith("/api/")) return api(req, res, url);
  state.log.push(`GET ${url.pathname}`);
  let file = path.join(dist, url.pathname === "/" ? "index.html" : url.pathname);
  if (!file.startsWith(dist) || !fs.existsSync(file)) file = path.join(dist, "index.html");
  const ext = path.extname(file);
  const type = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".webmanifest": "application/manifest+json", ".woff2": "font/woff2", ".svg": "image/svg+xml", ".png": "image/png" }[ext] || "application/octet-stream";
  const headers = { "content-type": type, "x-content-type-options": "nosniff" };
  if (file.endsWith("index.html")) headers["content-security-policy"] = CSP;
  res.writeHead(200, headers);
  fs.createReadStream(file).pipe(res);
});

// ---- checks -------------------------------------------------------------------------

const results = [];
let failed = false;
function check(name, ok, detail) {
  results.push({ name, ok, detail });
  if (!ok) failed = true;
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
}

const INSTRUMENT = () => {
  window.__long = [];
  window.__gaps = [];
  try {
    new PerformanceObserver((l) => l.getEntries().forEach((e) => window.__long.push(Math.round(e.duration)))).observe({ entryTypes: ["longtask"] });
  } catch {}
  let last = performance.now();
  const tick = (t) => {
    window.__gaps.push(t - last);
    last = t;
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
  window.__csp = [];
  document.addEventListener("securitypolicyviolation", (e) => window.__csp.push(e.violatedDirective + " " + e.blockedURI));
  // Every blob: thumbnail of an attached image, loaded or refused (#142). Sent images in the history share data-media="thumb" but
  // load from /api/assets, so only blob: sources are recorded.
  window.__thumbs = [];
  for (const kind of ["load", "error"]) {
    document.addEventListener(kind, (e) => {
      const t = e.target;
      if (t instanceof HTMLImageElement && t.dataset.media === "thumb" && t.src.startsWith("blob:")) window.__thumbs.push({ ok: kind === "load", w: t.naturalWidth });
    }, true);
  }
};

const scrollerInfo = () => {
  const el = document.querySelector(".log");
  const items = [...document.querySelectorAll(".vitem")];
  return { top: el.scrollTop, height: el.scrollHeight, client: el.clientHeight, mounted: items.length, first: items[0]?.dataset.vkey, last: items.at(-1)?.dataset.vkey };
};

async function openPage(browser, opts) {
  const ctx = await browser.newContext(opts);
  const page = await ctx.newPage();
  const consoleErrors = [];
  const external = [];
  page.on("console", (m) => m.type() === "error" && consoleErrors.push(m.text()));
  page.on("pageerror", (e) => consoleErrors.push(String(e)));
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.origin !== base) external.push(r.url());
  });
  await page.addInitScript(INSTRUMENT);
  return { ctx, page, consoleErrors, external };
}

let base = "";

async function desktop(browser) {
  const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
  state.log.length = 0;
  const t0 = Date.now();
  await page.goto(base + "/");
  await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
  await page.waitForFunction(() => {
    const el = document.querySelector(".log");
    const last = document.querySelector('.vitem[data-vkey="999"]');
    return !!last && last.getBoundingClientRect().bottom <= el.getBoundingClientRect().bottom + 2;
  });
  const openMs = Date.now() - t0;
  const msgReqs = state.log.filter((l) => l.includes("/messages"));
  const opened = await page.evaluate(scrollerInfo);
  check("500-turn conversation opens on the newest page only", msgReqs.length === 1 && !msgReqs[0].includes("cursor"), `${msgReqs.length} history request(s) at open; ${openMs} ms to the newest turn on screen`);
  check("opening mounts a bounded number of turns", opened.mounted <= 60 && opened.mounted >= 1, `${opened.mounted} of ${2 * TURNS} messages in the DOM`);
  check("no rich-content chunk is fetched when no message needs it", !state.log.some((l) => /assets\/Block-/.test(l)), "OpenUI code is lazy");
  await page.screenshot({ path: path.join(outDir, "desktop-open.png") });

  // Scroll back to the very start the way a reader does: repeated upward flicks.
  const t1 = Date.now();
  let maxMounted = opened.mounted;
  let guard = 0;
  while (guard++ < 400) {
    await page.evaluate(() => {
      const el = document.querySelector(".log");
      el.scrollTop = Math.max(0, el.scrollTop - 1400);
    });
    await page.waitForTimeout(40);
    // Read the position after the wait: a page that arrives while the reader is at
    // the top is prepended and the reader is kept on their message, so scrollTop moves.
    const info = await page.evaluate(scrollerInfo);
    maxMounted = Math.max(maxMounted, info.mounted);
    const done = await page.evaluate(() => document.body.innerText.includes("Start of the conversation"));
    if (info.top === 0 && done) break;
  }
  const scrollMs = Date.now() - t1;
  const first = await page.evaluate(() => document.querySelector(".vitem")?.innerText ?? "");
  const stats = await page.evaluate(() => ({ long: window.__long, gaps: window.__gaps.slice(1) }));
  const cursors = state.log.filter((l) => l.includes("/messages?cursor="));
  const uniq = new Set(cursors);
  check("scrolling back loads every older page exactly once", cursors.length === TURNS * 2 / PAGE - 1 && uniq.size === cursors.length, `${cursors.length} older-page requests, ${uniq.size} distinct (expected ${TURNS * 2 / PAGE - 1})`);
  check("the start of the conversation is reached", first.includes("question 0"), JSON.stringify(first.slice(0, 40)));
  check("the DOM stays bounded through the whole scroll-back", maxMounted <= 100, `max ${maxMounted} mounted of ${2 * TURNS} (all-mounted ceiling 100, then windowed)`);
  const maxGap = Math.round(Math.max(...stats.gaps));
  const over100 = stats.gaps.filter((g) => g > 100).length;
  const maxLong = stats.long.length ? Math.max(...stats.long) : 0;
  check("no stall while scrolling back through 500 turns", maxGap < 250 && maxLong < 200, `scroll-back ${scrollMs} ms; frames ${stats.gaps.length}; max frame gap ${maxGap} ms; frames >100 ms: ${over100}; long tasks: ${stats.long.length} (max ${maxLong} ms)`);
  await page.screenshot({ path: path.join(outDir, "desktop-top.png") });

  // Contiguity: walk down and read every mounted key; they must be consecutive.
  let contiguous = true;
  const seen = new Set();
  for (let y = 0; ; y += 600) {
    const r = await page.evaluate((yy) => {
      const el = document.querySelector(".log");
      el.scrollTop = yy;
      return [el.scrollTop, [...document.querySelectorAll(".vitem")].map((n) => Number(n.dataset.vkey))];
    }, y);
    await page.waitForTimeout(8);
    const keys = await page.evaluate(() => [...document.querySelectorAll(".vitem")].map((n) => Number(n.dataset.vkey)));
    for (let i = 1; i < keys.length; i++) if (keys[i] !== keys[i - 1] + 1) contiguous = false;
    keys.forEach((k) => seen.add(k));
    if (r[0] < y) break;
  }
  check("every turn is reachable, in order, with none missing or repeated", contiguous && seen.size === 2 * TURNS, `${seen.size} of ${2 * TURNS} distinct turns seen while walking down`);
  check("no console error, CSP violation or off-origin request on desktop", consoleErrors.length === 0 && external.length === 0 && (await page.evaluate(() => window.__csp.length)) === 0, `${consoleErrors.length} console errors; ${external.length} external requests; ${JSON.stringify(consoleErrors.slice(0, 2))}`);
  await ctx.close();
}

async function prepend(browser) {
  const { ctx, page } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
  await page.goto(base + "/");
  await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
  await page.waitForTimeout(150);
  let worst = 0;
  let pages = 0;
  for (let round = 0; round < 6; round++) {
    // Park the reader a little below the load trigger, then hold the older page.
    await page.evaluate(() => {
      const el = document.querySelector(".log");
      el.scrollTop = Math.min(el.scrollTop, 900);
    });
    let release;
    state.holdOlder = new Promise((r) => (release = r));
    await page.evaluate(() => (document.querySelector(".log").scrollTop = 200));
    try {
      await page.waitForFunction(() => document.querySelector(".log").getAttribute("aria-busy") === "true", null, { timeout: 5000 });
    } catch (e) {
      console.log("DEBUG round", round, JSON.stringify(await page.evaluate(scrollerInfo)), state.log.slice(-4), await page.evaluate(() => document.body.innerText.slice(0, 120)));
      throw e;
    }
    // The message at the top of the viewport and where it is on screen.
    const before = await page.evaluate(() => {
      const el = document.querySelector(".log");
      const box = el.getBoundingClientRect();
      const n = [...document.querySelectorAll(".vitem")].find((x) => x.getBoundingClientRect().bottom > box.top + 4);
      return { key: n.dataset.vkey, y: n.getBoundingClientRect().top - box.top, firstKey: Number(document.querySelector(".vitem").dataset.vkey) };
    });
    // Watch that message on every frame while the page is delivered and laid out.
    await page.evaluate((k) => {
      window.__dev = [];
      const el = document.querySelector(".log");
      const watch = () => {
        const n = document.querySelector(`.vitem[data-vkey="${k.key}"]`);
        if (n) window.__dev.push(n.getBoundingClientRect().top - el.getBoundingClientRect().top - k.y);
        window.__w = requestAnimationFrame(watch);
      };
      watch();
    }, before);
    release();
    state.holdOlder = null;
    await page.waitForFunction((fk) => Number(document.querySelector(".vitem").dataset.vkey) < fk || document.body.innerText.includes("Start of the conversation"), before.firstKey);
    await page.waitForTimeout(250);
    const dev = await page.evaluate(() => {
      cancelAnimationFrame(window.__w);
      return window.__dev;
    });
    const maxDev = Math.max(...dev.map(Math.abs));
    worst = Math.max(worst, maxDev);
    pages++;
    if (round === 0) await page.screenshot({ path: path.join(outDir, "desktop-prepend.png") });
  }
  check("prepending an older page does not move the message being read", worst <= 1, `${pages} pages prepended with variable-height turns; the top visible message moved at most ${worst.toFixed(2)} px on any frame`);
  await ctx.close();
}

async function rich(browser) {
  const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
  await page.goto(base + "/");
  await page.waitForSelector(".side button.item");
  await page.click(".side button.item:nth-of-type(1) >> text=Rich conversation");
  await page.waitForSelector("table.ou-table");
  const chunk = state.log.some((l) => /assets\/Block-/.test(l));
  const txt = await page.evaluate(() => document.querySelector(".log").innerText);
  check("a valid OpenUI table renders through the allowlist, and its chunk loads only now", chunk && (await page.locator("table.ou-table th").count()) === 2, "Table, Card and Badge rendered");
  check("an OpenUI block with an action falls back to inert source", txt.includes("could not be displayed") && (await page.locator(".ou-fallback a, .ou-fallback button").count()) === 0 && txt.includes("OpenUrl"), "no link or button was created");
  await page.evaluate(() => (document.querySelector(".ou-table-wrap").scrollIntoView(), 0));
  await page.screenshot({ path: path.join(outDir, "desktop-openui.png") });
  const csp = await page.evaluate(() => window.__csp);
  check("no console error, CSP violation or off-origin request with OpenUI", consoleErrors.length === 0 && external.length === 0 && csp.length === 0, `${consoleErrors.length} console errors ${JSON.stringify(consoleErrors.slice(0, 2))}; ${external.length} external ${JSON.stringify(external.slice(0, 2))}; CSP ${JSON.stringify(csp)}`);
  await ctx.close();
}

async function drafts(browser) {
  const { ctx, page } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
  const open = async (title) => {
    await page.click(`.side button.item >> text=${title}`);
    await page.waitForSelector(".vitem");
  };
  const box = () => page.inputValue("#composer-input");
  state.email = "reader@example.test";
  await page.goto(base + "/");
  await page.waitForSelector(".side button.item");
  await open("Plain conversation");
  await page.fill("#composer-input", "draft for plain");
  await open("Rich conversation");
  const richEmpty = (await box()) === "";
  await page.fill("#composer-input", "draft for rich");
  await open("Plain conversation");
  check("a draft stays with its chat and does not appear in another", richEmpty && (await box()) === "draft for plain", "chat 1 text absent in chat 2; back in chat 1 it is restored");
  await page.reload();
  await page.waitForSelector(".side button.item");
  await open("Rich conversation");
  const rich = await box();
  await open("Plain conversation");
  check("drafts survive a reload of the built bundle", rich === "draft for rich" && (await box()) === "draft for plain", `rich: ${JSON.stringify(rich)}`);
  await page.fill("#composer-input", "/sum");
  const menu = await page.locator(".templates button").allInnerTexts();
  await page.keyboard.press("Enter");
  const inserted = await box();
  check("a leading slash lists templates and Enter inserts instead of sending", menu.join() === "/summarize" && inserted.startsWith("Summarize") && !state.log.some((l) => l.startsWith("POST /api/chats/")), `${JSON.stringify(menu)} -> ${JSON.stringify(inserted.slice(0, 20))}`);
  // A different account in the same browser profile reads none of it.
  state.email = "other@example.test";
  await page.reload();
  await page.waitForSelector(".side button.item");
  await open("Plain conversation");
  const other = await box();
  await open("Rich conversation");
  check("another account in the same browser sees no draft", other === "" && (await box()) === "", `plain ${JSON.stringify(other)}`);
  state.email = "reader@example.test";
  await ctx.close();
}

// Phone layout (#138) in a real browser against the mock backend, so it runs in CI with no scratch server: the same measurements
// and verdicts as web/scripts/mobile-check.mjs, at 320/375/390/430 portrait and a phone on its side, with the keyboard closed and
// open (an on-screen keyboard is approximated by a shorter viewport), the account menu, and a focused search under the keyboard.
async function mobileLayout(browser) {
  for (const { w, h } of [{ w: 320, h: 568 }, { w: 375, h: 667 }, { w: 390, h: 844 }, { w: 430, h: 932 }, { w: 568, h: 320 }]) {
    CURRENT.w = w;
    // A soft keyboard is about 45% of a small phone's height, 300px at most (375x667 -> 367). A phone on its side is not checked with the
    // keyboard up: a landscape keyboard leaves almost nothing of any layout.
    const keyboardUp = w < h;
    const KEYBOARD = Math.min(300, Math.round(h * 0.45));
    const { ctx, page } = await openPage(browser, { viewport: { width: w, height: h }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
    await page.goto(base + "/");
    await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
    await page.waitForTimeout(300);
    const verdict = async (name, { keyboard = false, searchOpen = false, scope } = {}) => {
      const m = await page.evaluate(measure, scope);
      const v = verdicts(m, keyboard, Boolean(scope), 1, searchOpen);
      check(`mobile layout ${w}px, ${name}`, v.length === 0, v.slice(0, 4).join("; "));
    };
    const short = h - KEYBOARD;
    await verdict("keyboard closed");
    // Scrolled up: the way back to the newest turn and the note above the history must be on screen and work.
    await page.evaluate(() => { document.querySelector(".log").scrollTop = 0; });
    await page.waitForTimeout(300);
    await verdict("scrolled to the top");
    const jump = page.getByRole("button", { name: "Jump to latest" });
    await jump.click();
    await page.waitForTimeout(300);
    check(`mobile layout ${w}px, Jump to latest returns to the newest turn`, await page.evaluate(() => { const l = document.querySelector(".log"); return l.scrollTop + l.clientHeight >= l.scrollHeight - 2; }));
    await page.getByRole("button", { name: "Menu" }).click();
    await verdict("account menu open", { scope: ".bar-actions" });
    check(`mobile layout ${w}px, Sign out is in the menu`, await page.getByRole("button", { name: "Sign out", exact: true }).isVisible());
    const clicks = await pointerCheck(page, ".bar-actions");
    check(`mobile layout ${w}px, a real pointer click reaches every menu action`, clicks.length === 0, clicks.slice(0, 3).join("; "));
    await page.keyboard.press("Escape");
    if (keyboardUp) {
      await page.setViewportSize({ width: w, height: h - KEYBOARD });
      await page.getByRole("button", { name: "Menu" }).click();
      await verdict("account menu open, keyboard open", { keyboard: true, scope: ".bar-actions" });
      const kclicks = await pointerCheck(page, ".bar-actions");
      check(`mobile layout ${w}px, a real pointer click reaches every menu action, keyboard open`, kclicks.length === 0, kclicks.slice(0, 3).join("; "));
      await page.keyboard.press("Escape");
      await page.setViewportSize({ width: w, height: h });
    }
    if (keyboardUp) {
      await page.setViewportSize({ width: w, height: short });
      await page.locator("#composer-input").focus();
      await page.keyboard.type("a draft");
      await page.waitForTimeout(150);
      await verdict("keyboard open, typing", { keyboard: true });
      await page.evaluate(() => { document.querySelector(".log").scrollTop = 0; });
      await page.waitForTimeout(300);
      await verdict("keyboard open, scrolled to the top", { keyboard: true });
      await page.setViewportSize({ width: w, height: h });
    }
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.locator("#search-input").focus();
    if (keyboardUp) {
      await page.setViewportSize({ width: w, height: short });
      await page.waitForTimeout(150);
      await verdict("search focused, keyboard open", { keyboard: true, searchOpen: true });
      const kept = await page.evaluate(() => document.activeElement?.id === "search-input" && Boolean([...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "Close search")));
      check(`mobile layout ${w}px, search input stays focused with Close search`, kept);
    }
    await page.getByRole("button", { name: "Close search" }).click();
    await page.setViewportSize({ width: w, height: h });
    await page.waitForTimeout(150);
    await verdict("keyboard closed again");
    await ctx.close();
  }
}

// A reached token limit opens the Options drawer, and the toggle must still close it so the conversation grows back.
async function mobileBudget(browser) {
  state.budgetReached = true;
  try {
    for (const { w, h } of [{ w: 320, h: 568 }, { w: 375, h: 667 }, { w: 390, h: 844 }, { w: 430, h: 932 }]) {
      CURRENT.w = w;
      const { ctx, page } = await openPage(browser, { viewport: { width: w, height: h }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
      await page.goto(base + "/");
      await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
      await page.waitForSelector("#chat-extras.open", { timeout: 8000 });
      const toggle = page.locator(".extras-toggle");
      const logH = () => page.evaluate(() => document.querySelector(".log").getBoundingClientRect().height);
      const openH = await logH();
      await page.screenshot({ path: path.join(outDir, `budget-${w}-open.png`) });
      check(`mobile budget ${w}px, a reached limit opens the options and says so`, (await toggle.getAttribute("aria-expanded")) === "true" && (await page.locator("#chat-extras").innerText()).includes("Token limit reached"));
      await toggle.click();
      await page.waitForTimeout(200);
      const closedH = await logH();
      await page.screenshot({ path: path.join(outDir, `budget-${w}-after-toggle.png`) });
      check(`mobile budget ${w}px, the toggle closes the options and the conversation grows back`, (await toggle.getAttribute("aria-expanded")) === "false" && !(await page.evaluate(() => document.querySelector("#chat-extras").classList.contains("open"))) && closedH > openH + 20, `log ${Math.round(openH)} -> ${Math.round(closedH)}px`);
      check(`mobile budget ${w}px, the closed toggle still reports the limit`, (await toggle.innerText()).includes("token limit reached"));
      const m = await page.evaluate(measure);
      const v = verdicts(m, false, false, 1, false);
      check(`mobile budget ${w}px, layout with the options closed`, v.length === 0, v.slice(0, 3).join("; "));
      await toggle.click();
      await page.waitForTimeout(200);
      check(`mobile budget ${w}px, the toggle opens them again`, (await toggle.getAttribute("aria-expanded")) === "true");
      await ctx.close();
    }
  } finally {
    state.budgetReached = false;
  }
}


// ---- desktop layout (#139): the rail, the dock, the popover and the note above the history, measured in a real browser ----------
// What this proves that jsdom cannot: nothing the user can press is covered by anything, no target is smaller than WCAG 2.2's 24px,
// the brand / conversation list / account do not overlap, the Menu popover opens and every action in it is reachable, and the
// "Start of the conversation" note (and a shown capability reason) never sits on top of message text, at any scroll position.
// There is no exemption list: every text node in the log is tested against the note.
const desktopMeasure = () => {
  const vw = innerWidth, vh = innerHeight;
  const label = (el) => (el.getAttribute("aria-label") || el.textContent || el.id || el.tagName).trim().replace(/\s+/g, " ").slice(0, 28);
  const box = (el) => el.getBoundingClientRect();
  const shown = (el) => {
    let r = box(el);
    const s = getComputedStyle(el);
    if (r.width < 1 || r.height < 1 || s.visibility === "hidden" || s.display === "none") return null;
    // The content of a closed <details> is not rendered (only its summary is), whatever its rectangle says.
    const closed = el.closest("details:not([open])");
    if (closed && !(el.matches("summary") && el.parentElement === closed)) return null;
    for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
      const cs = getComputedStyle(a);
      if (cs.overflowY === "visible" && cs.overflowX === "visible") continue;
      const c = box(a);
      r = { left: Math.max(r.left, c.left), top: Math.max(r.top, c.top), right: Math.min(r.right, c.right), bottom: Math.min(r.bottom, c.bottom) };
    }
    return r.right > r.left && r.bottom > r.top ? r : null;
  };
  const controls = [...document.querySelectorAll("button, a[href], input:not([type=hidden]):not([type=file]), select, textarea, summary")].filter((el) => shown(el));
  const outside = controls.filter((el) => !el.closest(".log") || el.closest(".vjump"));
  const small = outside.filter((el) => { const r = box(el); return r.width < 24 || r.height < 24; }).map(label);
  const covered = controls.filter((el) => {
    const r = shown(el);
    const x = (r.left + r.right) / 2, y = (r.top + r.bottom) / 2;
    if (x < 0 || y < 0 || x > vw || y > vh) return false;
    const t = document.elementFromPoint(x, y);
    return !(t && (t === el || el.contains(t)));
  }).map(label);
  const overlaps = [];
  for (let i = 0; i < outside.length; i++) for (let j = i + 1; j < outside.length; j++) {
    const a = box(outside[i]), b = box(outside[j]);
    if (outside[i].contains(outside[j]) || outside[j].contains(outside[i])) continue;
    if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 2 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 2) overlaps.push(`${label(outside[i])} / ${label(outside[j])}`);
  }
  const rect = (q) => { const e = document.querySelector(q); return e ? box(e) : null; };
  const brand = rect(".brand"), list = rect(".side"), account = rect(".account"), pane = rect(".pane"), dock = rect(".composer");
  const rail = brand && list && account ? { brandOverList: brand.bottom > list.top + 1, listOverAccount: list.bottom > account.top + 1, paneBesideRail: pane.left >= list.right - 1 } : null;
  const reach = {};
  for (const name of ["New chat", "Menu", "Send"]) {
    const el = [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === name && shown(b));
    reach[name] = Boolean(el);
  }
  const attach = [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "Attach image");
  reach["Attach image"] = Boolean(attach && shown(attach));
  // Text under the note: every text node in the log, tested against the note's rectangle. Nothing is skipped.
  const noteEl = document.querySelector(".vstatus span");
  const note = noteEl && shown(noteEl) ? box(noteEl) : null;
  const log = document.querySelector(".log") ? box(document.querySelector(".log")) : null;
  const under = [];
  if (note && log) {
    const walker = document.createTreeWalker(document.querySelector(".log"), NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      if (!n.textContent.trim() || noteEl.contains(n) || n.parentElement.closest(".vjump")) continue;
      const range = document.createRange();
      range.selectNodeContents(n);
      for (const r of range.getClientRects()) {
        if (r.width < 1 || r.height < 1 || r.bottom < log.top || r.top > log.bottom) continue;
        if (Math.min(r.right, note.right) - Math.max(r.left, note.left) > 2 && Math.min(r.bottom, note.bottom) - Math.max(r.top, note.top) > 2) under.push(n.textContent.trim().slice(0, 24));
      }
    }
  }
  return {
    vw, vh, overflowX: document.documentElement.scrollWidth - vw, pageScrolls: document.documentElement.scrollHeight > vh + 1,
    small, covered, overlaps, rail, reach, dockInView: Boolean(dock && dock.bottom <= vh + 0.5 && dock.top >= 0 && dock.width > 160),
    noteVisible: Boolean(note), noteUnderText: under, notePosition: noteEl ? getComputedStyle(noteEl.parentElement).position : "",
  };
};
// The text rectangles a shown reason must not cover: all log text except the reason itself and its own button.
const reasonOverlap = (controlSel) => {
  const btn = document.querySelector(controlSel);
  const small = btn.closest(".turn-control").querySelector("small");
  const r0 = small.getBoundingClientRect();
  const hits = [];
  const walker = document.createTreeWalker(document.querySelector(".log"), NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (!n.textContent.trim() || small.contains(n)) continue;
    const range = document.createRange();
    range.selectNodeContents(n);
    for (const r of range.getClientRects()) if (r.width > 0 && Math.min(r.right, r0.right) - Math.max(r.left, r0.left) > 2 && Math.min(r.bottom, r0.bottom) - Math.max(r.top, r0.top) > 2) hits.push(n.textContent.trim().slice(0, 24));
  }
  return { size: [Math.round(r0.width), Math.round(r0.height)], hits };
};

async function desktopLayout(browser) {
  for (const { w, h } of [{ w: 1280, h: 800 }, { w: 1024, h: 700 }, { w: 700, h: 700 }, { w: 641, h: 800 }]) {
    const { ctx, page, consoleErrors } = await openPage(browser, { viewport: { width: w, height: h } });
    await page.goto(base + "/");
    await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
    await page.waitForTimeout(300);
    const name = `desktop layout ${w}x${h}`;
    const verdict = async (step) => {
      const m = await page.evaluate(desktopMeasure);
      const bad = [];
      if (m.overflowX > 0) bad.push(`page overflows horizontally by ${m.overflowX}px`);
      if (m.pageScrolls) bad.push("the page itself scrolls");
      if (m.small.length) bad.push(`target below 24px: ${m.small.join(", ")}`);
      if (m.covered.length) bad.push(`covered by another element: ${m.covered.join(", ")}`);
      if (m.overlaps.length) bad.push(`overlapping controls: ${m.overlaps.slice(0, 3).join("; ")}`);
      if (!m.rail || m.rail.brandOverList || m.rail.listOverAccount || !m.rail.paneBesideRail) bad.push(`rail regions overlap: ${JSON.stringify(m.rail)}`);
      for (const [k, ok] of Object.entries(m.reach)) if (!ok) bad.push(`${k} is not reachable on screen`);
      if (!m.dockInView) bad.push("the composer dock is outside the viewport or too narrow");
      if (m.noteUnderText.length) bad.push(`the note above the history sits on message text: ${[...new Set(m.noteUnderText)].slice(0, 3).join(" | ")}`);
      if (m.notePosition === "sticky" || m.notePosition === "fixed" || m.notePosition === "absolute") bad.push(`the note above the history is ${m.notePosition}: it must be in the flow`);
      check(`${name}, ${step}`, bad.length === 0, bad.slice(0, 4).join("; "));
    };
    await verdict("newest turn");
    // The note must not sit on text wherever the reader is, including the top of the loaded history.
    for (const [step, frac] of [["a third of the way up", 0.66], ["mid history", 0.4], ["near the top", 0.02]]) {
      await page.evaluate((f) => { const l = document.querySelector(".log"); l.scrollTop = l.scrollHeight * f - l.clientHeight * f; }, frac);
      await page.waitForTimeout(350);
      await verdict(step);
    }
    await page.evaluate(() => { document.querySelector(".log").scrollTop = 0; });
    await page.waitForTimeout(400);
    await verdict("top of the loaded history");
    const jump = page.getByRole("button", { name: "Jump to latest" });
    check(`${name}, Jump to latest is on screen when away from the newest turn`, await jump.isVisible());
    await jump.click();
    await page.waitForTimeout(350);
    check(`${name}, Jump to latest returns to the newest turn`, await page.evaluate(() => { const l = document.querySelector(".log"); return l.scrollTop + l.clientHeight >= l.scrollHeight - 2; }));
    // The account popover: opens from the rail, every action is reachable, Escape closes it.
    const menu = page.getByRole("button", { name: "Menu" });
    await menu.click();
    const items = await page.evaluate(() => [...document.querySelectorAll(".bar-actions button")].map((b) => { const r = b.getBoundingClientRect(); const t = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return [b.textContent.trim(), (t === b || b.contains(t)) && r.top >= 0 && r.bottom <= innerHeight && r.left >= 0 && r.right <= innerWidth]; }));
    check(`${name}, the Menu popover opens and each action (${items.map((i) => i[0]).join(", ")}) is reachable`, items.length >= 4 && items.every((i) => i[1]), JSON.stringify(items.filter((i) => !i[1])));
    await page.screenshot({ path: path.join(outDir, `desktop-layout-${w}-menu.png`) });
    await page.keyboard.press("Escape");
    check(`${name}, Escape closes the Menu popover`, (await menu.getAttribute("aria-expanded")) === "false");
    // A capability reason is shown by a press (mouse here, touch in the phone check) and is in the flow: it covers no text.
    const regen = '[data-control="regenerate"]';
    await page.locator(regen).scrollIntoViewIfNeeded();
    await page.locator(regen).click({ force: true }); // aria-disabled: Playwright treats it as disabled, but a real pointer press must still reach it
    const shownReason = await page.evaluate((sel) => document.querySelector(sel).closest(".turn-control").getAttribute("data-reason"), regen);
    const ro = await page.evaluate(reasonOverlap, regen);
    check(`${name}, a press shows the capability reason, and it covers no text`, shownReason === "shown" && ro.size[0] > 20 && ro.hits.length === 0, `reason ${ro.size.join("x")}px, covers ${JSON.stringify(ro.hits)}`);
    await page.keyboard.press("Escape");
    check(`${name}, Escape puts the reason away`, (await page.evaluate((sel) => document.querySelector(sel).closest(".turn-control").getAttribute("data-reason"), regen)) === null);
    check(`${name}, no console error, CSP violation or off-origin request`, consoleErrors.length === 0 && (await page.evaluate(() => window.__csp.length)) === 0, JSON.stringify(consoleErrors.slice(0, 2)));
    await ctx.close();
  }
}

// The starting card: real markup, centred in the conversation area, covering nothing, on a phone and a wide screen.
async function startingCard(browser) {
  state.emptyList = true;
  try {
    for (const [label, opts] of [["desktop", { viewport: { width: 1280, height: 800 } }], ["phone", { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true }]]) {
      const { ctx, page } = await openPage(browser, opts);
      await page.goto(base + "/");
      await page.waitForSelector(".hero");
      await page.waitForTimeout(200);
      const m = await page.evaluate(() => {
        const r = (q) => { const e = document.querySelector(q); return e ? e.getBoundingClientRect() : null; };
        const hero = r(".hero"), pane = r(".pane"), dock = r(".composer"), side = r(".side");
        const tag = document.querySelector(".hero-tag");
        const text = document.querySelector(".hero-text");
        const clash = [dock, side].some((o) => o && Math.min(hero.right, o.right) - Math.max(hero.left, o.left) > 2 && Math.min(hero.bottom, o.bottom) - Math.max(hero.top, o.top) > 2);
        const t = text.getBoundingClientRect();
        return { inPane: hero.left >= pane.left - 1 && hero.right <= pane.right + 1 && hero.top >= pane.top - 20 && hero.bottom <= dock.top + 1, clash, tagHidden: tag.getAttribute("aria-hidden") === "true", textInside: t.left >= hero.left && t.right <= hero.right && t.bottom <= hero.bottom, overflowX: document.documentElement.scrollWidth - innerWidth };
      });
      await page.screenshot({ path: path.join(outDir, `starting-card-${label}.png`) });
      check(`starting card, ${label}: inside the conversation area, covers nothing, its tag is decorative`, m.inPane && !m.clash && m.tagHidden && m.textInside && m.overflowX <= 0, JSON.stringify(m));
      await ctx.close();
    }
  } finally {
    state.emptyList = false;
  }
}

// Polls a condition in the page with page.evaluate (waitForFunction compiles a string, which the page's CSP refuses).
async function until(page, fn, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await page.evaluate(fn)) return;
    await page.waitForTimeout(50);
  }
  throw new Error("condition not met within " + ms + "ms: " + fn);
}

// Image attach, remove, retry and send against the mock (the #138 gap). A mock browser: no real camera, no model, no device.
async function images(browser) {
  // Two distinct files: the mock can tell which upload was which, so a test never assumes the order two concurrent uploads arrive in.
  const png = (name) => ({ name, mimeType: "image/png", buffer: Buffer.concat([PNG, Buffer.from(name.replace(/\.png$/, ""), "latin1")]) });
  const idOf = (name) => [...state.assets].find(([, a]) => a.name === name.replace(/\.png$/, ""))?.[0];
  // State the MOCK holds is polled on the node side: the page may be ahead of a request still on its way (a slow runner), never the reverse.
  const settled = async (fn, what, ms = 8000) => {
    const end = Date.now() + ms;
    while (Date.now() < end) {
      if (fn()) return true;
      await new Promise((r) => setTimeout(r, 25));
    }
    console.log(`  (timed out waiting for ${what})`);
    return false;
  };
  for (const [label, opts] of [["desktop", { viewport: { width: 1280, height: 800 } }], ["phone", { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true }]]) {
    const phone = label === "phone";
    if (phone) CURRENT.w = 390;
    state.sent.length = 0; state.deleted.length = 0; state.assets.clear();
    const { ctx, page, consoleErrors } = await openPage(browser, opts);
    await page.goto(base + "/");
    await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
    await page.waitForSelector('button:has-text("Attach image")');
    const input = page.locator('input[type=file][aria-label="Choose images"]');
    const items = () => page.locator('[data-media="item"]').count();
    // A failed upload blocks the send and offers Retry and Remove.
    state.failNextUpload = true;
    await input.setInputFiles(png("bad.png"));
    await page.waitForSelector('[data-media="item"][data-status="failed"]');
    await page.fill("#composer-input", "with a failed image");
    const blocked = await page.getByRole("button", { name: "Send", exact: true }).isDisabled();
    check(`images ${label}: a failed upload shows Retry and Remove, and blocks Send`, blocked && (await page.getByRole("button", { name: "Remove bad.png" }).count()) === 1);
    await page.getByRole("button", { name: "Remove bad.png" }).click();
    check(`images ${label}: removing the failed image clears the block`, (await items()) === 0 && !(await page.getByRole("button", { name: "Send", exact: true }).isDisabled()));
    // Attach two, remove one: the server is told, the other stays.
    await input.setInputFiles([png("one.png"), png("two.png")]);
    await until(page, () => document.querySelectorAll('[data-media="item"][data-status="ready"]').length === 2);
    check(`images ${label}: two images attach and become ready`, state.assets.size === 2);
    if (phone) {
      const m = await page.evaluate(measure);
      const v = verdicts(m, false, false, 0.7, false);
      check(`images ${label}: the phone layout holds with images attached (targets, overlaps, hit-tests, overflow)`, v.length === 0, v.slice(0, 3).join("; "));
    } else {
      const m = await page.evaluate(desktopMeasure);
      const bad = [...m.small.map((x) => `small: ${x}`), ...m.covered.map((x) => `covered: ${x}`), ...m.overlaps.map((x) => `overlap: ${x}`)];
      check(`images ${label}: the desktop layout holds with images attached`, bad.length === 0 && m.overflowX <= 0 && m.dockInView, bad.slice(0, 3).join("; "));
    }
    await page.screenshot({ path: path.join(outDir, `images-${label}-attached.png`) });
    const removedId = idOf("one.png"), keep = idOf("two.png");
    await page.getByRole("button", { name: "Remove one.png" }).click();
    await until(page, () => document.querySelectorAll('[data-media="item"]').length === 1);
    // The strip drops the item at once; the DELETE reaches the server a moment later. Wait for it, then assert both facts.
    const gone = await settled(() => state.deleted.includes(removedId), "the DELETE of one.png");
    check(`images ${label}: Remove deletes the asset on the server and keeps the other`, gone && state.deleted.length === 1 && state.assets.size === 1 && state.assets.has(keep) && (await items()) === 1, `deleted ${JSON.stringify(state.deleted)}, held ${JSON.stringify([...state.assets.keys()])}, removed ${removedId}, kept ${keep}`);
    // A text no earlier run produced: the mock keeps the turns of the desktop pass, so a fixed text would already be on the page (stale) when the phone pass starts.
    const text = `what is in this picture? (${label} ${Date.now().toString(36)})`;
    await page.fill("#composer-input", text);
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.waitForSelector(`text=Received 1 image(s) and: ${text}`, { timeout: 8000 });
    const sent = state.sent.at(-1);
    check(`images ${label}: Send posts the text with exactly the remaining image id (and an idempotency key)`, sent && sent.input === text && sent.imageAssetIds.length === 1 && sent.imageAssetIds[0] === keep && /\S{8,}/.test(sent.key ?? ""), JSON.stringify(sent));
    const after = { strip: await items(), draft: await page.inputValue("#composer-input"), reply: await page.evaluate((want) => { const hits = [...document.querySelectorAll(".log *")].filter((e) => e.textContent.includes("Received 1 image(s) and: " + want)); const el = hits.at(-1); const turn = el?.closest(".turn"); return turn ? turn.className + " | " + turn.innerText.replace(/\s+/g, " ").slice(0, 70) : `no turn around: ${el ? el.tagName + "." + el.className : "no element"}`; }, text), live: await page.locator(".run-streaming, .run-reconnecting, .partial, .notice[role=alert]").count() };
    check(`images ${label}: the composer and the strip are empty after Send, and the reply is shown as completed`, after.strip === 0 && after.draft === "" && after.reply.includes("Received 1 image(s)") && !/partial|failed|cancelled/.test(after.reply.split("|")[0]) && after.live === 0, JSON.stringify(after));
    await page.screenshot({ path: path.join(outDir, `images-${label}-sent.png`) });
    if (phone) {
      // iOS Safari does not focus a tapped button and has no hover: a TAP must show the capability reason.
      const regen = page.locator('[data-control="regenerate"]');
      await regen.scrollIntoViewIfNeeded();
      const box = await regen.boundingBox();
      await page.touchscreen.tap(box.x + box.width / 2, box.y + box.height / 2);
      await page.waitForTimeout(150);
      const reason = await page.evaluate(() => document.querySelector('[data-control="regenerate"]').closest(".turn-control").getAttribute("data-reason"));
      const ro = await page.evaluate(reasonOverlap, '[data-control="regenerate"]');
      check(`images ${label}: a TAP (no hover, no focus) shows the capability reason, and it covers no text`, reason === "shown" && ro.size[0] > 20 && ro.hits.length === 0, `reason ${ro.size.join("x")} covers ${JSON.stringify(ro.hits)}`);
      await page.screenshot({ path: path.join(outDir, `phone-reason-tapped.png`) });
    }
    // #142: the page's CSP is img-src 'self' blob:, so the attached-image thumbnail (a blob: URL the page made) must actually render,
    // while an image from anywhere else is still refused. Nothing in this flow may produce a CSP message except the probes below.
    const csp = await page.evaluate(() => window.__csp.splice(0));
    const unexpected = unexpectedMessages(consoleErrors, csp);
    check(`images ${label}: no console error or CSP violation other than the deliberate 415`, unexpected.length === 0, JSON.stringify(unexpected.slice(0, 2)));
    const rendered = await page.evaluate(() => window.__thumbs ?? []);
    check(`images ${label}: every attached-image blob: thumbnail rendered (blob: allowed by img-src)`, rendered.length > 0 && rendered.every((t) => t.ok && t.w > 0), JSON.stringify(rendered.slice(0, 4)));
    const probes = await page.evaluate(async () => {
      const load = (src) => new Promise((res) => {
        const img = new Image();
        img.onload = () => res("loaded");
        img.onerror = () => res("blocked");
        img.src = src;
      });
      const remote = await load("https://example.invalid/probe.png");
      const data = await load("data:image/gif;base64,R0lGODlhAQABAAAAACw=");
      await new Promise((r) => setTimeout(r, 100));
      return { remote, data, events: window.__csp.splice(0) };
    });
    check(`images ${label}: an image from another origin and a data: image are still refused by img-src`, probes.remote === "blocked" && probes.data === "blocked" && probes.events.some((e) => /^img-src(-elem)? https:\/\/example\.invalid/.test(e)) && probes.events.some((e) => /^img-src(-elem)? data/.test(e)), JSON.stringify(probes));
    await ctx.close();
  }
}

async function mobile(browser) {
  const { ctx, page, consoleErrors } = await openPage(browser, { viewport: { width: Number(process.env.MOBILE_WIDTH || 390), height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true });
  await page.goto(base + "/");
  await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
  await page.waitForTimeout(200);
  const m = await page.evaluate(() => {
    const de = document.documentElement;
    const comp = document.querySelector("#composer-input");
    const r = comp.getBoundingClientRect();
    const send = [...document.querySelectorAll(".composer button")].map((b) => b.getBoundingClientRect());
    const log = document.querySelector(".log").getBoundingClientRect();
    return {
      overflowX: de.scrollWidth - window.innerWidth,
      composerVisible: r.top >= 0 && r.bottom <= window.innerHeight && r.width > 120,
      composerFont: parseFloat(getComputedStyle(comp).fontSize),
      sendH: Math.min(...send.map((b) => b.height)),
      logH: log.height,
      innerH: window.innerHeight,
      pageScrolls: de.scrollHeight > window.innerHeight + 1,
    };
  });
  check("mobile: no horizontal scroll", m.overflowX <= 0, `scrollWidth - viewport = ${m.overflowX}`);
  check("mobile: composer is on screen and usable", m.composerVisible && m.composerFont >= 16 && m.sendH >= 44, `font ${m.composerFont}px, send button ${Math.round(m.sendH)}px high`);
  check("mobile: the conversation has room (the page itself does not scroll)", m.logH >= m.innerH * 0.35 && !m.pageScrolls, `conversation ${Math.round(m.logH)} of ${m.innerH}px`);
  // Scroll back on a phone viewport too.
  let g = 0;
  while (g++ < 500) {
    const top = await page.evaluate(() => {
      const el = document.querySelector(".log");
      el.scrollTop = Math.max(0, el.scrollTop - 1200);
      return el.scrollTop;
    });
    await page.waitForTimeout(40);
    if (top === 0 && (await page.evaluate(() => document.body.innerText.includes("Start of the conversation")))) break;
  }
  const mounted = await page.evaluate(() => document.querySelectorAll(".vitem").length);
  const gaps = await page.evaluate(() => window.__gaps.slice(1));
  check("mobile: scroll-back reaches the start without a stall", (await page.evaluate(() => document.querySelector(".vitem").innerText)).includes("question 0") && Math.max(...gaps) < 250 && mounted <= 100, `max frame gap ${Math.round(Math.max(...gaps))} ms; ${mounted} mounted`);
  await page.screenshot({ path: path.join(outDir, "mobile-top.png") });
  check("mobile: no console error or CSP violation", consoleErrors.length === 0 && (await page.evaluate(() => window.__csp.length)) === 0, JSON.stringify(consoleErrors.slice(0, 2)));
  await ctx.close();
}

// ONLY=shots: review screenshots (no checks) of the chat, Options closed and open, light and dark, desktop and phone,
// for an owner's visual review (#139). Run before and after a visual change and compare the folders.
async function shots(browser) {
  for (const scheme of ["light", "dark"]) {
    for (const v of [{ name: "desktop", width: 1440, height: 900 }, { name: "phone", width: 390, height: 844, isMobile: true, hasTouch: true }]) {
      const { ctx, page } = await openPage(browser, { viewport: { width: v.width, height: v.height }, colorScheme: scheme, isMobile: v.isMobile, hasTouch: v.hasTouch });
      await page.goto(base + "/");
      await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
      await page.waitForTimeout(400);
      await page.screenshot({ path: path.join(outDir, `shot-${v.name}-${scheme}.png`) });
      const toggle = page.locator(".extras-toggle");
      if ((await toggle.count()) > 0 && (await toggle.isVisible())) {
        await toggle.click();
        await page.waitForTimeout(300);
        await page.screenshot({ path: path.join(outDir, `shot-${v.name}-${scheme}-options.png`) });
      }
      await ctx.close();
    }
  }
}

await new Promise((r) => server.listen(0, "127.0.0.1", r));
base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
console.log(`# ${await browser.version()} (Chromium), bundle ${fs.readdirSync(path.join(dist, "assets")).filter((f) => f.endsWith(".js")).join(", ")}`);
const only = process.env.ONLY;
try {
  if (!only || only === "desktop") await desktop(browser);
  if (!only || only === "prepend") await prepend(browser);
  if (!only || only === "rich") await rich(browser);
  if (!only || only === "drafts") await drafts(browser);
  if (!only || only === "mobile") await mobile(browser);
  if (!only || only === "mobile" || only === "mobile-layout") await mobileLayout(browser);
  if (!only || only === "desktop-layout") await desktopLayout(browser);
  if (!only || only === "starting-card") await startingCard(browser);
  if (!only || only === "images") await images(browser);
  if (!only || only === "mobile" || only === "mobile-layout" || only === "mobile-budget") await mobileBudget(browser); // no timing in it: this is the one CI runs
  if (only === "shots") await shots(browser);
} finally {
  await browser.close();
  server.close();
}
console.log(`\n${results.filter((r) => r.ok).length}/${results.length} checks passed; screenshots in ${outDir}`);
process.exit(failed ? 1 : 0);
