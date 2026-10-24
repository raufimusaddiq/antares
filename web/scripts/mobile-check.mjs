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
const TARGET = 44;
const WIDTHS = [
  { w: 320, h: 568 },
  { w: 375, h: 667 },
  { w: 390, h: 844 },
  { w: 430, h: 932 },
];
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

// Runs in the page. Returns the facts the verdicts are built from.
const measure = (scope) => {
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const rect = (el) => {
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), b: Math.round(r.bottom), r: Math.round(r.right) };
  };
  const visible = (el) => {
    const s = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    // The content of a closed <details> keeps its box in the DOM but is not rendered; its summary is.
    for (let d = el.closest("details"); d; d = d.parentElement?.closest("details")) if (!d.open && !(el.tagName === "SUMMARY" && el.parentElement === d)) return false;
    return s.visibility !== "hidden" && s.display !== "none" && r.width > 0 && r.height > 0 && !el.closest(".sr") && !el.closest("[hidden]");
  };
  const label = (el) => (el.getAttribute("aria-label") || el.textContent || el.id || el.tagName).trim().replace(/\s+/g, " ").slice(0, 32);
  // A checkbox is activated through its label, so the label is the target that is measured.
  const controls = [...(scope ? document.querySelector(scope) : document).querySelectorAll("button, a[href], input:not([type=hidden]):not([type=file]), select, textarea, summary")]
    .map((el) => (el.matches("input[type=checkbox], input[type=radio]") ? el.closest("label") ?? el : el))
    .filter((el, i, all) => all.indexOf(el) === i)
    .filter(visible);
  const small = controls.filter((el) => { const r = el.getBoundingClientRect(); return Math.min(r.width, r.height) < 43.5; }).map((el) => ({ el: label(el), ...rect(el) }));
  const outside = controls.filter((el) => {
    // A control inside a horizontally scrolling strip is reachable by scrolling it; the page itself must not scroll.
    const strip = el.closest(".side");
    const r = el.getBoundingClientRect();
    // Inside a region that scrolls on purpose (the log, the options drawer, the menu) only horizontal overflow is a defect.
    if (el.closest(".log, .extras, .bar-actions")) return r.right > vw + 0.5 || r.left < -0.5;
    if (strip && strip.scrollWidth > strip.clientWidth) return r.bottom < 0 || r.top > vh;
    return r.right > vw + 0.5 || r.left < -0.5 || r.bottom > vh + 0.5 || r.top < -0.5;
  }).map((el) => ({ el: label(el), ...rect(el) }));
  const overlaps = [];
  const fixedish = controls.filter((el) => !el.closest(".log") || el.closest(".vjump,.vstatus"));
  for (let i = 0; i < fixedish.length; i++)
    for (let j = i + 1; j < fixedish.length; j++) {
      const a = fixedish[i].getBoundingClientRect();
      const b = fixedish[j].getBoundingClientRect();
      if (fixedish[i].contains(fixedish[j]) || fixedish[j].contains(fixedish[i])) continue;
      const ox = Math.min(a.right, b.right) - Math.max(a.left, b.left);
      const oy = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
      if (ox > 2 && oy > 2) overlaps.push([label(fixedish[i]), label(fixedish[j])]);
    }
  const q = (s) => document.querySelector(s);
  const ta = q(".composer textarea");
  const log = q(".log");
  const bar = q(".chat-app > .bar");
  const side = q(".chat-app > .side");
  const comp = q(".composer");
  const vstatus = q(".vstatus span");
  const firstTurn = q(".log .turn");
  return {
    vw, vh,
    pageOverflowX: document.documentElement.scrollWidth - vw,
    pageScrollY: Math.round(window.scrollY),
    bodyScrollable: document.documentElement.scrollHeight - vh,
    bar: bar && rect(bar), side: side && rect(side), log: log && rect(log), composer: comp && rect(comp), textarea: ta && rect(ta),
    logShare: log ? +(log.getBoundingClientRect().height / vh).toFixed(2) : 0,
    chromeShare: bar ? +(((bar.getBoundingClientRect().height + (side && getComputedStyle(side).display !== "none" ? side.getBoundingClientRect().height : 0))) / vh).toFixed(2) : 0,
    textareaClipped: ta ? ta.scrollHeight > ta.clientHeight + 1 : null,
    vstatusOverFirstTurn: vstatus && firstTurn ? (() => { const a = vstatus.getBoundingClientRect(); const b = firstTurn.getBoundingClientRect(); return Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 2 && Math.min(a.right, b.right) - Math.max(a.left, b.left) > 2; })() : false,
    small, outside, overlaps,
  };
};

const verdicts = (m, keyboard, scoped, minLog = 0.4) => {
  const v = [];
  if (scoped) {
    for (const s of m.small) v.push(`target below ${TARGET}px: "${s.el}" ${s.w}x${s.h}`);
    for (const s of m.outside) v.push(`control outside the viewport: "${s.el}"`);
    for (const [a, b] of m.overlaps) v.push(`controls overlap: "${a}" / "${b}"`);
    if (m.pageOverflowX > 0) v.push(`page overflows horizontally by ${m.pageOverflowX}px`);
    return v;
  }
  if (m.pageOverflowX > 0) v.push(`page overflows horizontally by ${m.pageOverflowX}px`);
  if (m.pageScrollY !== 0 || m.bodyScrollable > 1) v.push(`the page itself scrolls (scrollY ${m.pageScrollY}, extra ${m.bodyScrollable}px): the conversation must be the scroll region`);
  if (m.chromeShare > (keyboard ? 0.45 : 0.3)) v.push(`header+conversation strip take ${Math.round(m.chromeShare * 100)}% of the viewport`);
  if (!keyboard && m.logShare < minLog) v.push(`the log gets ${Math.round(m.logShare * 100)}% of the viewport (<${minLog * 100}%)`);
  if (!m.textarea) v.push("composer textarea missing");
  else {
    if (m.textarea.h < TARGET) v.push(`textarea is ${m.textarea.h}px tall (<${TARGET})`);
    if (m.textarea.w < 120) v.push(`textarea is ${m.textarea.w}px wide (<120)`);
    if (m.composer.b > m.vh + 0.5) v.push("composer extends below the viewport");
    if (m.composer.h > m.vh * (keyboard ? 0.7 : 0.45)) v.push(`composer takes ${m.composer.h}px of ${m.vh}px`);
  }
  if (m.vstatusOverFirstTurn) v.push("the start-of-conversation note overlaps the first message");
  for (const s of m.small) v.push(`target below ${TARGET}px: "${s.el}" ${s.w}x${s.h}`);
  for (const s of m.outside) v.push(`control outside the viewport: "${s.el}"`);
  for (const [a, b] of m.overlaps) v.push(`controls overlap: "${a}" / "${b}"`);
  return v;
};

const browser = await chromium.launch({ executablePath: process.env.CHROME });
const result = { browser: browser.version(), emulator: true, physicalDevice: false, cases: [], violations: 0 };
try {
  await seed();
  for (const { w, h } of WIDTHS) {
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36" });
    await ctx.addCookies([{ name: SESSION, value: A, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
    // The scratch server's same-origin check wants its configured PUBLIC_ORIGIN; the browser talks to loopback.
    await ctx.route(`${BASE}/api/**`, (route) => route.continue({ headers: { ...route.request().headers(), origin: ORIGIN } }));
    // The scratch Hermes has no image catalogue, so the server truthfully says images are off. To lay out Attach and Take photo
    // (what the owner's account shows), only the browser-side answer to the capability probe is replaced; uploads go to the real API.
    await ctx.route(/\/api\/chats\/[^/]+\/input$/, (route) => route.fulfill({ json: { image: { available: true, reason: null, model: "scratch-vision", maxImages: 4, maxTotalBytes: 6291456 } } }));
    const page = await ctx.newPage();
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForSelector(".chat-app", { timeout: 15000 });
    await page.getByRole("button", { name: /^mobile-check/ }).first().click();
    await page.waitForSelector(".log .turn", { timeout: 15000 });
    await page.waitForTimeout(400);
    const step = async (name, fn, scope) => {
      const m = await page.evaluate(measure, scope);
      const keyboard = name.includes("keyboard");
      // An attached image adds its own strip to the composer, so that step only requires the log to stay readable (20%).
      const viol = verdicts(m, keyboard, Boolean(scope), name === "image-attached" ? 0.2 : 0.4);
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
    await page.keyboard.type("First line of a longer message\nsecond line\nthird line that is long enough to wrap onto another row on a narrow phone screen");
    await page.waitForTimeout(200);
    await step("keyboard-open-multiline");
    await page.setViewportSize({ width: w, height: h });
    await page.waitForTimeout(200);
    await step("keyboard-closed-multiline");
    await page.locator('.composer button[type="submit"]').click({ timeout: 8000 });
    await page.waitForFunction(() => document.querySelectorAll(".log .turn.assistant").length >= 7 && !document.querySelector(".run-streaming"), null, { timeout: 20000 }).catch(() => {});
    await step("after-reply");
    const fail = (msg) => { result.violations++; result.cases.at(-1).violations.push(msg); };
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
    await page.waitForSelector("[data-media='strip'] li", { timeout: 8000 }).catch(() => {});
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
  await browser.close();
}
console.log(JSON.stringify({ ...result, out }, null, 2));
process.exit(result.violations > 0 ? 1 : 0);
