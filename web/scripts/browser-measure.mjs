// Real-browser measurement of the PWA cache privacy question and of the long-chat list (#87).
//
//   node scripts/browser-measure.mjs <state.json> [turns,turns,...]      (see docs/runbooks/capacity.md "Browser and PWA")
//
// Needs playwright-core (PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core) and a scratch Antares from
// scripts/capacity with `load setup` already run (state.json holds two approved accounts' session cookies).
// Env: CHROME (a chromium or chrome-headless-shell binary), BASE (default http://127.0.0.1:18080), ORIGIN (the scratch PUBLIC_ORIGIN, for the same-origin check on POST).
// Prints one JSON object. Nothing here touches a production host: it refuses a BASE that is not loopback.

import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";

const BASE = process.env.BASE ?? "http://127.0.0.1:18080";
const ORIGIN = process.env.ORIGIN ?? "https://antares.scratch.test";
if (!/^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(BASE)) throw new Error("BASE must be a loopback scratch instance");
const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_CORE ?? "playwright-core");
const state = JSON.parse(readFileSync(process.argv[2], "utf8"));
const [A, B] = state.users ?? state.Users;
const SESSION = "__Host-antares_session";
const turnsList = (process.argv[3] ?? "50,500").split(",").map(Number);
const PROMPT = "Please summarise the main trade-offs between SQLite and Postgres for a small multi-user web application.";

const api = (cookie, method, path, body, headers = {}) =>
  fetch(BASE + path, { method, headers: { Cookie: `${SESSION}=${cookie}`, Origin: ORIGIN, "Content-Type": "application/json", ...headers }, body: body && JSON.stringify(body) });

async function seed(cookie, turns) {
  const chat = (await (await api(cookie, "POST", "/api/chats", { title: "browser-measure" })).json()).id;
  for (let i = 0; i < turns; i++) {
    let r;
    do { // the default 30 runs/min limit applies to the seeding account too
      r = await api(cookie, "POST", `/api/chats/${chat}/messages`, { input: `${PROMPT} (${i})` }, { "Idempotency-Key": `bm-${Date.now()}-${i}` });
      if (r.status === 429) await new Promise((ok) => setTimeout(ok, 2000));
    } while (r.status === 429);
    if (r.status !== 202) throw new Error(`send ${i}: HTTP ${r.status}`);
    const { runId } = await r.json();
    const ev = await api(cookie, "GET", `/api/runs/${runId}/events`);
    const text = await ev.text(); // the stream ends at the terminal event
    if (!text.includes("run.completed")) throw new Error(`run ${i} did not complete`);
  }
  return chat;
}

const browser = await chromium.launch({ executablePath: process.env.CHROME });
const out = { browser: browser.version(), ua: null, pwa: {}, list: [] };

// ---- 1. Service worker: what is cached, keyed how, who can reach it --------------------------
async function snapshot(page) {
  return page.evaluate(async () => {
    const enc = new TextEncoder();
    const hex = async (buf) => [...new Uint8Array(await crypto.subtle.digest("SHA-256", buf))].map((b) => b.toString(16).padStart(2, "0")).join("").slice(0, 12);
    const res = {};
    for (const name of await caches.keys()) {
      const c = await caches.open(name);
      const entries = [];
      for (const req of await c.keys()) {
        const r = await c.match(req);
        const body = await r.clone().arrayBuffer();
        const text = new TextDecoder().decode(body);
        entries.push({ key: `${req.method} ${new URL(req.url).pathname}`, status: r.status, bytes: body.byteLength, sha: await hex(body),
          setCookie: r.headers.get("set-cookie"), vary: r.headers.get("vary"), hasEmail: /@scratch\.invalid/.test(text), hasPrompt: text.includes("trade-offs") });
      }
      res[name.slice(0, 22)] = entries.sort((a, b) => (a.key < b.key ? -1 : 1));
    }
    return res;
  });
}

const ctx = await browser.newContext();
const page = await ctx.newPage();
out.ua = await page.evaluate(() => navigator.userAgent);
await ctx.addCookies([{ name: SESSION, value: A, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
const chatA = await seed(A, 2);
await page.goto(BASE + "/");
await page.evaluate(() => navigator.serviceWorker.ready.then(() => true));
await page.reload();
out.pwa.controlled = await page.evaluate(() => !!navigator.serviceWorker.controller);
const before = await snapshot(page);

// Signed-in use as A: the app's own API calls, a chat page, a draft.
await page.goto(`${BASE}/chat/${chatA}`);
await page.waitForSelector("[data-vkey]");
const ta = page.locator("textarea").first();
await ta.fill("draft text typed by A");
await page.waitForTimeout(800);
await page.evaluate(async (c) => { for (const p of ["/api/me", "/api/chats", `/api/chats/${c}/messages`]) await fetch(p); }, chatA);
const afterUse = await snapshot(page);
out.pwa.cacheBeforeSignedInUse = before;
out.pwa.cacheUnchangedAfterSignedInUse = JSON.stringify(before) === JSON.stringify(afterUse);

// Background: freeze then resume the page (the lifecycle a hidden/backgrounded tab goes through).
const cdp = await ctx.newCDPSession(page);
await cdp.send("Page.enable");
await cdp.send("Page.setWebLifecycleState", { state: "frozen" });
await new Promise((r) => setTimeout(r, 1500));
await cdp.send("Page.setWebLifecycleState", { state: "active" });
out.pwa.cacheUnchangedAfterFreezeResume = JSON.stringify(await snapshot(page)) === JSON.stringify(afterUse);
out.pwa.draftKeysAsA = await page.evaluate(() => Object.keys(localStorage));

// Second account on the SAME browser profile (same origin, same Cache Storage, same localStorage),
// without an intervening sign-out: the worst case.
await ctx.clearCookies();
await ctx.addCookies([{ name: SESSION, value: B, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
await page.goto(BASE + "/");
const asB = await snapshot(page);
out.pwa.cacheIdenticalForSecondAccount = JSON.stringify(asB) === JSON.stringify(afterUse);
out.pwa.secondAccountProbe = await page.evaluate(async (c) => {
  const r = {};
  const hit = async (u) => { for (const n of await caches.keys()) { if (await (await caches.open(n)).match(u)) return true; } return false; };
  r.cacheMatchApiMe = await hit("/api/me");
  r.cacheMatchApiChats = await hit("/api/chats");
  r.cacheMatchAChatMessages = await hit(`/api/chats/${c}/messages`);
  r.cacheMatchAChatPage = await hit(`/chat/${c}`);
  // A's chat over the wire as B: ownership must refuse it.
  r.wireAChatAsB = (await fetch(`/api/chats/${c}/messages`)).status;
  // HTTP cache: does the browser still hold A's /api/me for anyone to read?
  r.httpCacheApiMe = await fetch("/api/me", { cache: "only-if-cached", mode: "same-origin" }).then((x) => x.status, (e) => "not cached (" + e.name + ")");
  r.localStorageKeys = Object.keys(localStorage);
  return r;
}, chatA);
out.pwa.responseHeaders = await page.evaluate(async (c) => {
  const h = {};
  for (const p of ["/api/me", "/api/chats", `/api/chats/${c}/messages`, "/", "/sw.js", "/offline.html"]) h[p.replace(c, ":chat")] = (await fetch(p, { cache: "no-store" })).headers.get("cache-control");
  return h;
}, chatA);
await ctx.close();

// ---- 2. Long thread: windowing and scroll smoothness ------------------------------------------
for (const turns of turnsList) {
  const cookie = A;
  const t0 = Date.now();
  const chat = await seed(cookie, turns);
  const seedSecs = (Date.now() - t0) / 1000;
  const c = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await c.addCookies([{ name: SESSION, value: cookie, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
  const p = await c.newPage();
  const t1 = Date.now();
  await p.goto(`${BASE}/chat/${chat}`);
  await p.waitForSelector("[data-vkey]");
  const openMs = Date.now() - t1;
  const m = await p.evaluate(async (expected) => {
    const el = document.querySelector(".log");
    const longtasks = [];
    new PerformanceObserver((l) => l.getEntries().forEach((e) => longtasks.push(e.duration))).observe({ entryTypes: ["longtask"] });
    const mounted = () => document.querySelectorAll("[data-vkey]").length;
    const nodes = () => document.getElementsByTagName("*").length;
    const r = { atOpen: { mounted: mounted(), domNodes: nodes(), scrollHeight: el.scrollHeight } };
    // A fast sustained flick upward at 4000 px/s through the whole thread: it keeps going as older pages
    // load, and stops when every message is held and the top is reached (or after 90 s).
    const deltas = [];
    let last = performance.now();
    const t0 = last;
    let maxMounted = 0;
    const held = () => +document.querySelector("[data-vkey]")?.getAttribute("aria-setsize");
    await new Promise((done) => {
      const step = (now) => {
        deltas.push(now - last);
        el.scrollTop = Math.max(0, el.scrollTop - 4000 * ((now - last) / 1000));
        last = now;
        maxMounted = Math.max(maxMounted, mounted());
        if ((el.scrollTop <= 0 && held() >= expected && now - t0 > 500) || now - t0 > 90000) done();
        else requestAnimationFrame(step);
      };
      requestAnimationFrame(step);
    });
    deltas.shift();
    const s = [...deltas].sort((a, b) => a - b);
    const q = (f) => +s[Math.min(s.length - 1, Math.floor(f * s.length))].toFixed(1);
    r.scrollUp = { frames: deltas.length, seconds: +((last - t0) / 1000).toFixed(2), p50ms: q(0.5), p95ms: q(0.95), p99ms: q(0.99), maxMs: +s[s.length - 1].toFixed(1),
      over25ms: deltas.filter((d) => d > 25).length, over50ms: deltas.filter((d) => d > 50).length, longTasks: longtasks.length, longTaskTotalMs: +longtasks.reduce((a, b) => a + b, 0).toFixed(0),
      maxMounted, mountedAtEnd: mounted(), domNodesAtEnd: nodes(), heapMB: performance.memory ? +(performance.memory.usedJSHeapSize / 1048576).toFixed(1) : null };
    r.scrollUp.messagesHeldAtEnd = held();
    return r;
  }, turns * 2);
  out.list.push({ turns, messagesSeeded: turns * 2, seedSecs, openMs, ...m });
  await c.close();
}
// Control: the same flick over a plain, non-virtual page of the same height, to separate this renderer's
// frame cost (software compositing, no GPU) from the list's.
{
  const c = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const p = await c.newPage();
  await p.setContent(`<div id=s style="height:800px;overflow:auto"><div id=c></div></div>`);
  out.control = await p.evaluate(async () => {
    const el = document.getElementById("s");
    document.getElementById("c").innerHTML = Array.from({ length: 600 }, (_, i) => `<p style="margin:0;padding:16px;height:100px">message ${i} lorem ipsum dolor sit amet</p>`).join("");
    el.scrollTop = el.scrollHeight;
    const d = [];
    let last = performance.now();
    await new Promise((done) => {
      const step = (now) => { d.push(now - last); el.scrollTop = Math.max(0, el.scrollTop - 4000 * ((now - last) / 1000)); last = now; if (el.scrollTop <= 0) done(); else requestAnimationFrame(step); };
      requestAnimationFrame(step);
    });
    d.shift();
    const s = [...d].sort((a, b) => a - b);
    return { frames: d.length, p50ms: +s[Math.floor(s.length / 2)].toFixed(1), p95ms: +s[Math.floor(s.length * 0.95)].toFixed(1), maxMs: +s[s.length - 1].toFixed(1), over50ms: d.filter((x) => x > 50).length, domNodes: document.getElementsByTagName("*").length };
  });
  await c.close();
}
await browser.close();
console.log(JSON.stringify(out, null, 1));
