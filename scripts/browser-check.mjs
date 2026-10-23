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
//
// playwright-core and a Chromium are deliberately not dependencies of web/.

import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const dist = path.join(here, "..", "internal", "webui", "dist");
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
    m.push({ seq: 2 * i, role: "user", text: `question ${i}` }, { seq: 2 * i + 1, role: "assistant", text: `answer ${i}\n\n${body}` });
  }
  return m;
}
const DATA = { [PLAIN]: transcript(false), [RICH]: transcript(true) };
const b64 = (s) => Buffer.from(s).toString("base64url");

const state = { log: [], holdOlder: null, delayMs: 15, email: "reader@example.test" };

function api(req, res, url) {
  const send = (status, body) => {
    res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
    res.end(JSON.stringify(body));
  };
  state.log.push(`${req.method} ${url.pathname}${url.search}`);
  if (url.pathname === "/api/me") return send(200, { email: state.email, displayName: "Reader", requestedAt: "", state: "ACTIVE" });
  if (url.pathname === "/api/chats") {
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
  return send(404, { error: { code: "NOT_FOUND", message: "x", requestId: "r" } });
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  if (url.pathname.startsWith("/api/")) return api(req, res, url);
  state.log.push(`GET ${url.pathname}`);
  let file = path.join(dist, url.pathname.startsWith("/assets/") ? url.pathname : "index.html");
  if (!file.startsWith(dist) || !fs.existsSync(file)) file = path.join(dist, "index.html");
  const ext = path.extname(file);
  const type = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html" }[ext] || "application/octet-stream";
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

async function mobile(browser) {
  const { ctx, page, consoleErrors } = await openPage(browser, { viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true });
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
  check("mobile: composer is on screen and usable", m.composerVisible && m.composerFont >= 16 && m.sendH >= 36, `font ${m.composerFont}px, send button ${Math.round(m.sendH)}px high`);
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

await new Promise((r) => server.listen(0, "127.0.0.1", r));
base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch();
console.log(`# ${await browser.version()} (Chromium), bundle ${fs.readdirSync(path.join(dist, "assets")).filter((f) => f.endsWith(".js")).join(", ")}`);
const only = process.env.ONLY;
try {
  if (!only || only === "desktop") await desktop(browser);
  if (!only || only === "prepend") await prepend(browser);
  if (!only || only === "rich") await rich(browser);
  if (!only || only === "drafts") await drafts(browser);
  if (!only || only === "mobile") await mobile(browser);
} finally {
  await browser.close();
  server.close();
}
console.log(`\n${results.filter((r) => r.ok).length}/${results.length} checks passed; screenshots in ${outDir}`);
process.exit(failed ? 1 : 0);
