// Real-browser check of chat management (#148): rename, pin, archive and delete in the sidebar, on a desktop viewport and on
// phone viewports, against a scratch Antares (scripts/capacity, docs/runbooks/capacity.md) with `load setup` already run.
//
//   node scripts/manage-check.mjs <state.json> [--out DIR]
//
// Needs playwright-core (PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core) and a Chromium (CHROME=/path/to/chrome).
// BASE (default http://127.0.0.1:18080) must be loopback, ORIGIN is the scratch PUBLIC_ORIGIN for the same-origin check.
// Nothing here touches a production host. The mobile viewports are an EMULATOR (Chromium device profile), not a physical phone.
//
// For each viewport: the controls are reachable by keyboard, a rename/pin/archive/restore is applied by the server and shown,
// delete asks first, Cancel deletes nothing, a refused delete (the browser is told 503) leaves the chat and says so, a confirmed
// delete removes the chat from the list, the API (404) and search, and no visible control is smaller than 44 CSS px or
// overlaps another, and nothing scrolls the page sideways. Exit 1 on any violation. Prints one JSON object; screenshots go to
// --out (private: never commit them).

import { readFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { createRequire } from "node:module";

const BASE = process.env.BASE ?? "http://127.0.0.1:18080";
const ORIGIN = process.env.ORIGIN ?? "https://antares.scratch.test";
if (!/^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(BASE)) throw new Error("BASE must be a loopback scratch instance");
const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_CORE ?? "playwright-core");
const state = JSON.parse(readFileSync(process.argv[2], "utf8"));
const [A, B] = state.users ?? state.Users;
const SESSION = "__Host-antares_session";
const outArg = process.argv.indexOf("--out");
const out = outArg > 0 ? process.argv[outArg + 1] : mkdtempSync("/tmp/antares-manage-");
mkdirSync(out, { recursive: true });

const api = (cookie, method, path, body, headers = {}) =>
  fetch(BASE + path, { method, headers: { Cookie: `${SESSION}=${cookie}`, Origin: ORIGIN, "Content-Type": "application/json", ...headers }, body: body && JSON.stringify(body) });
const json = async (r) => (r.headers.get("content-type")?.includes("json") ? r.json() : null);

async function seed(cookie, tag) {
  const made = {};
  for (const name of ["Alpha plan", "Beta notes", "Gamma draft"]) {
    const title = `${tag} ${name}`;
    const chat = (await (await api(cookie, "POST", "/api/chats", { title })).json()).id;
    const r = await api(cookie, "POST", `/api/chats/${chat}/messages`, { input: `launchcodename ${name}` }, { "Idempotency-Key": `mg-${tag}-${name.replace(/\W/g, "")}-${Date.now()}` });
    if (r.status !== 202) throw new Error(`send: HTTP ${r.status}`);
    const { runId } = await r.json();
    if (!(await (await api(cookie, "GET", `/api/runs/${runId}/events`)).text()).includes("run.completed")) throw new Error("run did not complete");
    made[name] = { id: chat, title };
  }
  return made;
}

const TARGET = 44;
// The controls this change adds, as seen by a pointer: only those a click at their centre would reach (a chip scrolled out of the
// strip is clipped and is not one). On a phone each must be at least 44x44 CSS px; on any viewport none overlaps another and none
// pokes outside the window; the page never scrolls sideways.
const layout = ({ sel, floor }) => {
  const root = document.querySelector(sel) ?? document.body;
  const mine = 'button.item, button.row-more, .row-menu [role="menuitem"], .dialog button, .dialog input, .archive button, li.archive-toggle button';
  const box = (e) => e.getBoundingClientRect();
  const reached = (e) => {
    const r = box(e);
    if (r.width === 0 || r.height === 0) return false;
    const x = Math.min(Math.max(r.left + r.width / 2, 0), innerWidth - 1), y = Math.min(Math.max(r.top + r.height / 2, 0), innerHeight - 1);
    const top = document.elementFromPoint(x, y);
    return top !== null && (e === top || e.contains(top) || top.contains(e));
  };
  const els = [...root.querySelectorAll(mine)].filter(reached);
  const name = (e) => (e.getAttribute("aria-label") || e.textContent || e.id || e.tagName).trim().slice(0, 30);
  const small = els.filter((e) => box(e).width < floor - 0.5 || box(e).height < floor - 0.5).map((e) => `${name(e)} ${Math.round(box(e).width)}x${Math.round(box(e).height)}`);
  const overlaps = [];
  for (let i = 0; i < els.length; i++)
    for (let j = i + 1; j < els.length; j++) {
      if (els[i].contains(els[j]) || els[j].contains(els[i])) continue;
      const a = box(els[i]), b = box(els[j]);
      const w = Math.min(a.right, b.right) - Math.max(a.left, b.left), h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
      if (w > 2 && h > 2) overlaps.push(`${name(els[i])} / ${name(els[j])}`);
    }
  // the strip scrolls sideways by design, so a chip partly past its edge is normal; the menu and the dialogs must be fully inside
  const off = els.filter((e) => e.closest('.row-menu, .dialog')).filter((e) => box(e).left < -0.5 || box(e).right > innerWidth + 0.5).map(name);
  return { small, overlaps, off, pageOverflowX: document.documentElement.scrollWidth > innerWidth + 1, count: els.length };
};

const result = { browser: null, emulator: true, physicalDevice: false, viewports: [], violations: 0 };
const browser = await chromium.launch({ executablePath: process.env.CHROME });
result.browser = browser.version();

async function run(label, viewport, mobile, tag) {
  const rec = { label, viewport, steps: [], violations: [] };
  const fail = (m) => { rec.violations.push(m); result.violations++; };
  const made = await seed(A, tag);
  const alpha = made["Alpha plan"], beta = made["Beta notes"], gamma = made["Gamma draft"];
  const ctx = await browser.newContext({
    viewport, deviceScaleFactor: mobile ? 3 : 1, isMobile: mobile, hasTouch: mobile,
    ...(mobile ? { userAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36" } : {}),
  });
  await ctx.addCookies([{ name: SESSION, value: A, domain: new URL(BASE).hostname, path: "/", secure: true, httpOnly: true }]);
  await ctx.route(`${BASE}/api/**`, (route) => route.continue({ headers: { ...route.request().headers(), origin: ORIGIN } }));
  const page = await ctx.newPage();
  const shot = async (name, scope) => {
    await page.screenshot({ path: `${out}/${label}-${name}.png` });
    const m = await page.evaluate(layout, { sel: scope ?? "body", floor: mobile ? TARGET : 24 });
    rec.steps.push({ name, controls: m.count });
    for (const s of m.small) fail(`${name}: control smaller than ${TARGET}px: ${s}`);
    for (const s of m.overlaps) fail(`${name}: overlapping controls: ${s}`);
    for (const s of m.off) fail(`${name}: control outside the viewport: ${s}`);
    if (m.pageOverflowX) fail(`${name}: the page scrolls sideways`);
  };
  // page.waitForFunction evaluates strings, which the app's CSP (script-src 'self') forbids: poll with evaluate instead.
  const until = async (fn, arg, ms = 8000) => {
    const t0 = Date.now();
    while (Date.now() - t0 < ms) {
      if (await page.evaluate(fn, arg)) return;
      await page.waitForTimeout(100);
    }
    throw new Error(`timed out waiting for ${fn}`);
  };
  const titles = async () => (await (await api(A, "GET", "/api/chats?limit=50")).json()).chats.map((c) => c.title);
  const row = (title) => page.locator("nav.side li", { has: page.locator("button.item", { hasText: title }) }).first();
  // On a phone only the open conversation has its actions button in the strip: open it first.
  const actions = async (title) => {
    await page.locator("nav.side button.item", { hasText: title }).first().click();
    await page.waitForTimeout(200);
    const btn = page.getByRole("button", { name: `Actions for ${title}` });
    await btn.click();
    await page.getByRole("menu").waitFor();
    return btn;
  };

  await page.goto(BASE + "/", { waitUntil: "networkidle" });
  await page.waitForSelector(".chat-app", { timeout: 15000 });
  await page.getByRole("button", { name: new RegExp(`^${tag} Alpha plan`) }).waitFor();
  await shot("list", "nav.side");

  // ---- menu: keyboard ----
  const beta0 = await actions(beta.title);
  await shot("menu-open", ".row-menu");
  const names = await page.getByRole("menuitem").allTextContents();
  if (names.join("|") !== "Rename…|Pin to top|Archive|Delete…") fail(`menu items: ${names.join("|")}`);
  if (!(await page.evaluate(() => document.activeElement?.textContent === "Rename…"))) fail("the first menu item does not take focus");
  await page.keyboard.press("ArrowDown");
  if (!(await page.evaluate(() => document.activeElement?.textContent === "Pin to top"))) fail("ArrowDown does not move to the next item");
  await page.keyboard.press("Escape");
  if (await page.getByRole("menu").count()) fail("Escape did not close the menu");
  if (!(await page.evaluate(() => document.activeElement?.getAttribute("aria-label")?.startsWith("Actions for ")))) fail("focus did not return to the actions button");
  void beta0;

  // ---- rename (keyboard only from the actions button) ----
  await page.keyboard.press("Enter");
  await page.getByRole("menu").waitFor();
  await page.keyboard.press("Enter"); // Rename…
  const dlg = page.getByRole("dialog", { name: "Rename conversation" });
  await dlg.waitFor();
  await shot("rename-dialog", ".dialog");
  if (!(await page.evaluate(() => document.activeElement?.tagName === "INPUT"))) fail("the rename field does not take focus");
  await page.keyboard.press("Control+A");
  await page.keyboard.type(`${tag} Beta renamed`);
  await page.keyboard.press("Enter");
  await dlg.waitFor({ state: "detached" });
  if (!(await titles()).includes(`${tag} Beta renamed`)) fail("the server does not have the new title");
  await page.locator("nav.side button.item", { hasText: `${tag} Beta renamed` }).waitFor();
  const q = await (await api(A, "GET", "/api/search/chats?q=renamed")).json();
  rec.steps.push({ name: "rename applied", searchHits: q.results?.length ?? 0 });
  if (!(q.results ?? []).some((r) => r.chatId === beta.id && r.kind === "title")) fail("search does not find the new title");
  await shot("after-rename", "nav.side");

  // ---- pin ----
  await actions(`${tag} Alpha plan`);
  await page.getByRole("menuitem", { name: "Pin to top" }).click();
  await until((t) => document.querySelector("nav.side > ul > li button.item")?.textContent?.startsWith(t), `${tag} Alpha plan`);
  const order = await titles();
  if (order[0] !== `${tag} Alpha plan`) fail(`pinned chat is not first on the server: ${order.slice(0, 3)}`);
  await shot("after-pin", "nav.side");

  // ---- archive and restore ----
  await actions(`${tag} Gamma draft`);
  await page.getByRole("menuitem", { name: "Archive" }).click();
  await until((t) => ![...document.querySelectorAll("nav.side > ul > li button.item")].some((b) => b.textContent?.includes(t)), `${tag} Gamma draft`);
  if ((await titles()).includes(`${tag} Gamma draft`)) fail("an archived chat is still in the main list on the server");
  await page.getByRole("button", { name: "Show archived" }).click();
  await page.getByRole("region", { name: "Archived conversations" }).getByText(`${tag} Gamma draft`).waitFor();
  await shot("archive-open", "nav.side");
  await page.getByRole("button", { name: `Actions for ${tag} Gamma draft` }).click();
  await shot("archive-menu", ".row-menu");
  await page.getByRole("menuitem", { name: "Restore from archive" }).click();
  await until((t) => !document.querySelector(".archive")?.textContent?.includes(t), `${tag} Gamma draft`);
  await page.getByRole("button", { name: "Hide archived" }).click();
  await page.locator("nav.side > ul > li button.item", { hasText: `${tag} Gamma draft` }).waitFor();

  // ---- delete: ask, cancel ----
  await actions(`${tag} Gamma draft`);
  await page.getByRole("menuitem", { name: "Delete…" }).click();
  const confirm = page.getByRole("alertdialog");
  await confirm.waitFor();
  await shot("delete-confirm", ".dialog");
  if ((await page.evaluate(() => document.activeElement?.textContent)) !== "Cancel") fail("the delete dialog does not start on Cancel");
  await page.keyboard.press("Escape");
  if (await confirm.count()) fail("Escape did not close the delete dialog");
  if (!(await titles()).includes(`${tag} Gamma draft`)) fail("Cancel deleted the chat");

  // ---- delete: the server is told to refuse (browser-level emulation of a failed upstream) ----
  await ctx.route(new RegExp(`/api/chats/${gamma.id}$`), (route) => (route.request().method() === "DELETE" ? route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "HERMES_UNAVAILABLE", message: "x", requestId: "r" } }) }) : route.continue()));
  await actions(`${tag} Gamma draft`);
  await page.getByRole("menuitem", { name: "Delete…" }).click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  const alert = confirm.getByRole("alert");
  await alert.waitFor();
  const said = await alert.textContent();
  await shot("delete-refused", ".dialog");
  if (!/did not confirm the delete/.test(said ?? "") || !/still here/.test(said ?? "")) fail(`refused delete says: ${said}`);
  if (!(await titles()).includes(`${tag} Gamma draft`)) fail("a refused delete removed the chat");
  await ctx.unroute(new RegExp(`/api/chats/${gamma.id}$`));

  // ---- delete: confirmed ----
  await page.getByRole("button", { name: "Try again" }).click();
  await confirm.waitFor({ state: "detached" });
  const after = await titles();
  if (after.includes(`${tag} Gamma draft`)) fail("a confirmed delete left the chat in the list on the server");
  if ((await api(A, "GET", `/api/chats/${gamma.id}`)).status !== 404) fail("the deleted chat still opens");
  if ((await api(A, "DELETE", `/api/chats/${gamma.id}`)).status !== 404) fail("a second delete is not a 404");
  const s2 = await (await api(A, "GET", "/api/search/chats?q=launchcodename")).json();
  if ((s2.results ?? []).some((r) => r.chatId === gamma.id)) fail("search still finds the deleted chat");
  if ((await page.locator("nav.side button.item", { hasText: `${tag} Gamma draft` }).count()) !== 0) fail("the deleted chat is still in the sidebar");
  await shot("after-delete", "nav.side");
  // another account cannot touch these chats (the negative IDOR, over the real HTTP stack)
  if (B) for (const [m, body] of [["PATCH", { title: "x" }], ["DELETE", undefined]]) {
    const r = await api(B, m, `/api/chats/${alpha.id}`, body);
    if (r.status !== 404) fail(`another account's ${m} of this chat answered ${r.status}`);
  }
  if (!(await titles()).includes(`${tag} Alpha plan`)) fail("the other account's attempts changed the chat");
  await ctx.close();
  result.viewports.push(rec);
}

try {
  const tag = `mg${Date.now().toString(36)}`;
  await run("desktop-1280", { width: 1280, height: 800 }, false, `${tag}d`);
  await run("phone-390", { width: 390, height: 844 }, true, `${tag}p`);
  await run("phone-320", { width: 320, height: 568 }, true, `${tag}s`);
} finally {
  await browser.close();
}
console.log(JSON.stringify(result, null, 2));
process.exit(result.violations === 0 ? 0 : 1);
