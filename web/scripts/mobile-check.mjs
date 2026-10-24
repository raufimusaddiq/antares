// Real-browser mobile layout check of the chat UI (#138): authenticated, against a scratch Antares.
//
//   node scripts/mobile-check.mjs <state.json> [--out DIR] [--seed]
//
// Needs playwright-core (PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core) and a Chromium (CHROME=/path/to/chrome),
// and a scratch Antares from scripts/capacity with `load setup` already run (docs/runbooks/capacity.md); state.json holds
// fabricated accounts' session cookies. BASE defaults to http://127.0.0.1:18080 and must be loopback; ORIGIN is the
// scratch PUBLIC_ORIGIN for the same-origin check on POST. Nothing here touches a production host.
//
// This is an EMULATOR: Chromium with a mobile device profile (touch, device scale factor, mobile viewport meta handling).
// It is not a physical phone. The on-screen keyboard is approximated by shrinking the viewport height; real iOS/Android
// keyboard, browser-chrome and safe-area behaviour needs a physical device and is reported separately.
//
// Checks at 320, 375, 390 and 430 CSS px portrait: no page horizontal overflow, the header and the conversation strip leave
// the log most of the screen, the composer and its controls are inside the viewport with the keyboard closed and open, every
// visible control is at least 44 CSS px in both dimensions, and no control overlaps another. Exit 1 on any violation,
// 2 when the scratch server is unusable. Prints one JSON object; screenshots go to --out (private: never commit them).

import { readFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { createRequire } from "node:module";
import { measure, verdicts, CURRENT } from "./mobile-layout.mjs";

const BASE = process.env.BASE ?? "http://127.0.0.1:18080";
const ORIGIN = process.env.ORIGIN ?? "https://antares.scratch.test";
if (!/^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(BASE)) throw new Error("BASE must be a loopback scratch instance");
const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_CORE ?? "playwright-core");
const state = JSON.parse(readFileSync(process.argv[2], "utf8"));
const [A] = state.users ?? state.Users;
const SESSION = "__Host-antares_session";
const outArg = process.argv.indexOf("--out");
const out = outArg > 0 ? process.argv[outArg + 1] : mkdtempSync("/tmp/antares-mobile-");
mkdirSync(out, { recursive: true });
const opt = (name) => (process.argv.includes(name) ? process.argv[process.argv.indexOf(name) + 1] : undefined);
// --fault drop-send | stuck-run: negative proof that the reply requirement bites. The run must then FAIL on the reply step
// (exit 0 means the fault was detected, exit 1 means a missing reply slipped through). --widths 390,375 limits the widths.
const FAULT = opt("--fault");
if (FAULT && !["drop-send", "stuck-run"].includes(FAULT)) throw new Error("--fault is drop-send or stuck-run");
const REPLY_TIMEOUT = 25000;
const ALL_WIDTHS = [
  { w: 320, h: 568 },
  { w: 375, h: 667 },
  { w: 390, h: 844 },
  { w: 430, h: 932 },
  { w: 568, h: 320 }, // a small phone on its side
];
const WIDTHS = opt("--widths") ? ALL_WIDTHS.filter(({ w }) => opt("--widths").split(",").map(Number).includes(w)) : ALL_WIDTHS;
const KEYBOARD = 300; // a typical soft keyboard, subtracted from the viewport height

const api = (method, path, body, headers = {}) =>
  fetch(BASE + path, { method, headers: { Cookie: `${SESSION}=${A}`, Origin: ORIGIN, "Content-Type": "application/json", ...headers }, body: body && JSON.stringify(body) });

async function seed() {
  const list = await (await api("GET", "/api/chats")).json();
  const have = (list.chats ?? list.items ?? list ?? []).filter?.((c) => c.title === "mobile-check") ?? [];
  const mk = async (title, turns) => {
    const chat = (await (await api("POST", "/api/chats", { title })).json()).id;
    for (let i = 0; i < turns; i++) {
      const r = await api("POST", `/api/chats/${chat}/messages`, { input: `Question ${i}: please explain the difference between a queue and a stack in a few sentences.` }, { "Idempotency-Key": `mc-${Date.now()}-${i}` });
      if (r.status !== 202) throw new Error(`send ${i}: HTTP ${r.status}`);
      const { runId } = await r.json();
      const text = await (await api("GET", `/api/runs/${runId}/events`)).text();
      if (!text.includes("run.completed")) throw new Error(`run ${i} did not complete`);
    }
    return chat;
  };
  if (have.length === 0) {
    await mk("mobile-check", 6);
    await mk("mobile-check second", 1);
  }
}

// stuck-run: the scratch fake Hermes holds every run far longer than the reply timeout, so the reply streams forever. Loopback only.
const UPSTREAM = process.env.UPSTREAM ?? "http://127.0.0.1:18081";
if (!/^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(UPSTREAM)) throw new Error("UPSTREAM must be the loopback scratch fake Hermes");
const hold = (ms) => fetch(`${UPSTREAM}/_ctl?hold_ms=${ms}`).catch(() => {});
if (FAULT === "stuck-run") await hold(120000);
const browser = await chromium.launch({ executablePath: process.env.CHROME });
const result = { browser: browser.version(), emulator: true, physicalDevice: false, cases: [], violations: 0 };
try {
  await seed();
  for (const { w, h } of WIDTHS) {
    CURRENT.w = w;
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36" });
    await ctx.addCookies([{ name: SESSION, value: A, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
    // The scratch server's same-origin check wants its configured PUBLIC_ORIGIN; the browser talks to loopback.
    await ctx.route(`${BASE}/api/**`, (route) => route.continue({ headers: { ...route.request().headers(), origin: ORIGIN } }));
    // The scratch Hermes has no image catalogue, so the server truthfully says images are off. To lay out Attach and Take photo
    // (what the owner's account shows), only the browser-side answer to the capability probe is replaced; uploads go to the real API.
    await ctx.route(/\/api\/chats\/[^/]+\/input$/, (route) => route.fulfill({ json: { image: { available: true, reason: null, model: "scratch-vision", maxImages: 4, maxTotalBytes: 6291456 } } }));
    if (FAULT === "drop-send") await ctx.route(/\/api\/chats\/[^/]+\/messages$/, (route) => (route.request().method() === "POST" ? route.abort() : route.continue()));
    const page = await ctx.newPage();
    let chatId = "";
    page.on("request", (r) => { const m = /\/api\/chats\/([^/?]+)\/messages(\?|$)/.exec(r.url()); if (m && r.method() === "GET") chatId = m[1]; });
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForSelector(".chat-app", { timeout: 15000 });
    await page.getByRole("button", { name: /^mobile-check/ }).first().click();
    await page.waitForSelector(".log .turn", { timeout: 15000 });
    await page.waitForTimeout(400);
    const step = async (name, fn, scope) => {
      const m = await page.evaluate(measure, scope);
      const keyboard = name.includes("keyboard");
      // An attached image adds its own strip to the composer, so that step only requires the log to stay readable (20%).
      const viol = verdicts(m, keyboard, Boolean(scope), name === "image-attached" ? 0.2 : 0.4, name === "search-keyboard-open");
      await page.screenshot({ path: `${out}/${w}-${name}.png` });
      result.cases.push({ width: w, name, measure: { vw: m.vw, vh: m.vh, chromeShare: m.chromeShare, logShare: m.logShare, composer: m.composer, textarea: m.textarea, pageOverflowX: m.pageOverflowX }, violations: viol });
      result.violations += viol.length;
      if (fn) await fn();
    };
    await step("open", async () => {
      // Scroll history: the log, not the page, scrolls.
      const moved = await page.evaluate(() => { const l = document.querySelector(".log"); const before = l.scrollTop; l.scrollTop = 0; return { scrollable: l.scrollHeight > l.clientHeight, before, after: l.scrollTop }; });
      if (!moved.scrollable) { result.violations++; result.cases.at(-1).violations.push("the log has nothing to scroll: history does not overflow its region"); }
    });
    await step("history-top");
    // Multiline typing, keyboard open (viewport shrunk), then send.
    await page.setViewportSize({ width: w, height: Math.max(h - KEYBOARD, 260) });
    await page.locator("#composer-input").focus();
    await page.fill("#composer-input", ""); // a draft left by an earlier run must not leak into this one
    // Enter sends in this app; a new line is Shift+Enter, so the three lines are typed with it and nothing is sent early.
    const lines = ["First line of a longer message", "second line", "third line that is long enough to wrap onto another row on a narrow phone screen"];
    for (const [i, line] of lines.entries()) {
      if (i > 0) await page.keyboard.press("Shift+Enter");
      await page.keyboard.type(line);
    }
    if ((await page.locator("#composer-input").inputValue()).split("\n").length !== 3) throw new Error("the multiline draft was not kept as three lines");
    await page.waitForTimeout(200);
    await step("keyboard-open-multiline");
    await page.setViewportSize({ width: w, height: h });
    await page.waitForTimeout(200);
    await step("keyboard-closed-multiline");
    // The reply must be NEW and non-empty relative to the state before sending, completed on the server and shown as completed in
    // the page (no streaming / reconnecting / failed / cancelled / partial). A timeout is a failure, never a pass.
    const replies = async () => {
      const r = await api("GET", `/api/chats/${chatId}/messages`);
      const turns = r.ok ? ((await r.json()).messages ?? []) : [];
      return turns.filter((t) => t.role === "assistant" && String(t.text ?? "").trim() !== "").map((t) => t.seq);
    };
    if (!chatId) throw new Error("could not tell which chat is open");
    const before = await replies();
    const t0 = Date.now();
    await page.locator('.composer button[type="submit"]').click({ timeout: 8000 });
    const terminalDom = () => page.evaluate(() => ({
      live: document.querySelectorAll(".run-streaming, .run-reconnecting, .run-failed, .run-cancelled, .partial, .log .turn.pending").length,
      alerts: [...document.querySelectorAll(".log [role=alert], .pane > [role=alert]")].map((e) => (e.textContent ?? "").trim().slice(0, 80)),
      text: [...document.querySelectorAll(".log .turn.assistant")].at(-1)?.textContent?.trim().length ?? 0,
    }));
    let reply = { ok: false, why: "no new reply" };
    while (Date.now() - t0 < REPLY_TIMEOUT) {
      const now = await replies();
      const fresh = now.filter((q) => !before.includes(q));
      const dom = await terminalDom();
      if (fresh.length === 1 && dom.live === 0 && dom.alerts.length === 0 && dom.text > 0) { reply = { ok: true, ms: Date.now() - t0 }; break; }
      reply = { ok: false, why: `new replies ${fresh.length}, page still live/partial ${dom.live}, alerts ${JSON.stringify(dom.alerts)}, last reply text ${dom.text} chars` };
      await page.waitForTimeout(250);
    }
    await step("after-reply");
    const fail = (msg) => { result.violations++; result.cases.at(-1).violations.push(msg); };
    if (!reply.ok) { fail(`no new non-empty completed reply within ${REPLY_TIMEOUT / 1000}s: ${reply.why}`); result.replyMissing = true; } else result.cases.at(-1).replyMs = reply.ms;
    if (FAULT) { await ctx.close(); continue; }
    // Account actions: reachable through the compact menu, closed again by Escape.
    await page.getByRole("button", { name: "Menu" }).click();
    await step("menu-open", null, ".bar-actions");
    for (const name of ["Sign out"]) if (!(await page.getByRole("button", { name }).isVisible())) fail(`"${name}" is not reachable in the open menu`);
    await page.keyboard.press("Escape");
    if (await page.getByRole("button", { name: "Sign out" }).isVisible()) fail("Escape did not close the menu");
    // Search: opens in the strip, finds the seeded text, opens the hit, and closes.
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.fill("#search-input", "queue");
    await page.keyboard.press("Enter");
    await page.waitForSelector(".search-results button.hit", { timeout: 8000 }).catch(() => {});
    await step("search-open", null, ".side");
    if ((await page.locator(".search-results button.hit").count()) === 0) fail("search returned no result for seeded text");
    await page.getByRole("button", { name: "Close search" }).click();
    if (await page.locator("#search-input").isVisible()) fail("the search box did not close");
    // Search with the keyboard up: the input the user is typing in and Close search must survive the viewport shrinking.
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.locator("#search-input").focus();
    await page.setViewportSize({ width: w, height: Math.max(h - KEYBOARD, 260) });
    await page.waitForTimeout(250);
    await page.keyboard.type("que");
    await step("search-keyboard-open");
    const inView = (sel) => page.evaluate((q) => { const e = document.querySelector(q); if (!e) return false; const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.top >= -0.5 && r.bottom <= innerHeight + 0.5; }, sel);
    if (!(await inView("#search-input"))) fail("the search input is not visible inside the viewport when the keyboard is up");
    if ((await page.evaluate(() => document.activeElement?.id)) !== "search-input") fail("the search input lost focus when the keyboard opened");
    if (!(await inView(".search-toggle"))) fail("Close search is not visible inside the viewport when the keyboard is up");
    await page.getByRole("button", { name: "Close search" }).click();
    if (await page.locator("#search-input").isVisible()) fail("Close search did not close the search with the keyboard up");
    if (!(await inView("#composer-input"))) fail("the composer did not come back after Close search with the keyboard up");
    await page.setViewportSize({ width: w, height: h });
    await page.waitForTimeout(200);
    // Options: usage, model, voice note and templates open on demand.
    await page.getByRole("button", { name: "Options" }).click();
    await step("options-open", null, ".extras");
    await page.getByRole("button", { name: "Options" }).click();
    // Switch chats from the strip.
    await page.locator(".side button.item", { hasText: "mobile-check second" }).click();
    await page.waitForSelector(".log .turn", { timeout: 8000 });
    await step("switched-chat");
    // Attach and remove an image (real /api/assets upload; the send path is not exercised against a vision model here).
    await page.locator("input[type=file][aria-label='Choose images']").setInputFiles({ name: "t.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg==", "base64") });
    await page.waitForSelector("[data-media='strip'] li", { timeout: 8000 });
    // measure the settled strip, not the upload in flight (polled with evaluate: the page's CSP forbids the string evaluation waitForFunction may use)
    for (let n = 0; n < 40 && (await page.evaluate(() => Boolean(document.querySelector("[data-media='strip'] progress")))); n++) await page.waitForTimeout(200);
    await step("image-attached");
    const removeBtn = page.getByRole("button", { name: /^Remove/ });
    if ((await removeBtn.count()) === 0) fail("the attached image shows no Remove control");
    else { await removeBtn.first().click(); await page.waitForTimeout(300); if ((await page.locator("[data-media='strip'] li").count()) !== 0) fail("Remove did not remove the image"); }
    // The bottom safe area (home indicator): the composer must clear it. Chromium's emulation of the inset is used.
    const cdp = await ctx.newCDPSession(page);
    const inset = await cdp.send("Emulation.setSafeAreaInsetsOverride", { insets: { top: 47, bottom: 34, left: 0, right: 0 } }).then(() => true, () => false);
    if (inset) {
      await page.waitForTimeout(200);
      const room = await page.evaluate(() => innerHeight - document.querySelector(".composer").getBoundingClientRect().bottom);
      if (room < 34) fail(`the composer is ${Math.round(room)}px above the bottom edge (<34px safe area)`);
    } else result.safeAreaEmulation = "unavailable";
    await ctx.close();
  }
} catch (e) {
  console.error(String(e));
  console.log(JSON.stringify(result, null, 2));
  process.exit(2);
} finally {
  if (FAULT === "stuck-run") await hold(800);
  await browser.close();
}
console.log(JSON.stringify({ ...result, out }, null, 2));
if (FAULT) {
  // The fault run stops at the reply step; it passes only if the reply requirement caught the fault.
  const caught = result.replyMissing === true;
  console.error(caught ? `fault ${FAULT}: missing reply detected (expected)` : `fault ${FAULT}: a missing reply was NOT detected`);
  process.exit(caught ? 0 : 1);
}
process.exit(result.violations > 0 ? 1 : 0);
