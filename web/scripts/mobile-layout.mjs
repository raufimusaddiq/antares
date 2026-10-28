// The mobile layout measurements and verdicts, used by
// scripts/browser-check.mjs (mock backend, runs in CI). `measure` runs inside the page and must stay self-contained.
export const TARGET = 44;
// Runs in the page. Returns the facts the verdicts are built from.
export const measure = async (scope) => {
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
    const strip = el.closest(".side > ul");
    const r = el.getBoundingClientRect();
    // Inside a region that scrolls on purpose (the log, the options drawer, the menu) only horizontal overflow is a defect.
    if (strip) return r.bottom < 0 || r.top > vh; // the chips scroll sideways in their own region
    if (el.closest(".log, .extras, .bar-actions, .side")) return r.right > vw + 0.5 || r.left < -0.5;
    return r.right > vw + 0.5 || r.left < -0.5 || r.bottom > vh + 0.5 || r.top < -0.5;
  }).map((el) => ({ el: label(el), ...rect(el) }));
  const overlaps = [];
  const fixedish = controls.filter((el) => !el.closest(".log") || el.closest(".vjump,.vstatus"));
  // What the user can touch is the part of a control its scrolling or clipping ancestors leave visible.
  const shown = (el) => {
    let r = el.getBoundingClientRect();
    r = { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
    for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
      if (getComputedStyle(a).overflowY === "visible" && getComputedStyle(a).overflowX === "visible") continue;
      const c = a.getBoundingClientRect();
      r = { left: Math.max(r.left, c.left), top: Math.max(r.top, c.top), right: Math.min(r.right, c.right), bottom: Math.min(r.bottom, c.bottom) };
    }
    return r.right > r.left && r.bottom > r.top ? r : null;
  };
  for (let i = 0; i < fixedish.length; i++)
    for (let j = i + 1; j < fixedish.length; j++) {
      const a = shown(fixedish[i]);
      const b = shown(fixedish[j]);
      if (!a || !b) continue;
      if (fixedish[i].contains(fixedish[j]) || fixedish[j].contains(fixedish[i])) continue;
      const ox = Math.min(a.right, b.right) - Math.max(a.left, b.left);
      const oy = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
      if (ox > 2 && oy > 2) overlaps.push([label(fixedish[i]), label(fixedish[j])]);
    }
  // Controls the user must always be able to reach: found by label, visible, and inside the screen (a strip that scrolls sideways counts).
  // The way back to the newest turn and the note above the history (with its Retry) must stay reachable wherever the log is scrolled.
  const lgEl = document.querySelector(".log");
  const scrolledUp = lgEl ? lgEl.scrollTop + lgEl.clientHeight < lgEl.scrollHeight - 80 : false;
  const atTop = lgEl ? lgEl.scrollTop < 2 : false;
  const noteEl = document.querySelector(".vstatus span");
  const noteText = noteEl ? (noteEl.textContent ?? "").trim() : "";
  const noteShown = noteEl ? Boolean(shown(noteEl) && noteEl.getBoundingClientRect().height > 8) : false;
  const reach = {};
  for (const name of ["Menu", "New chat", "Search", "Close search", "Options", "Attach image", "Take photo", "Send", "Jump to latest"]) {
    // The Options toggle also says when a token limit is reached ("Options · token limit reached").
    const el = [...document.querySelectorAll("button")].find((b) => (name === "Options" ? b.textContent.trim().startsWith("Options") : b.textContent.trim() === name) && visible(b));
    const r = el && shown(el);
    reach[name] = Boolean(r && r.bottom <= vh + 0.5 && r.top >= -0.5 && r.left >= -0.5 && (r.right <= vw + 0.5 || el.closest(".side")));
  }
  // A control another element sits on top of cannot be tapped, whatever its rectangle says: hit-test the centre of what is visible.
  const occluded = controls.filter((el) => !el.closest(".log") || el.closest(".vjump")).filter((el) => {
    const r = shown(el);
    if (!r || r.bottom - r.top < 8 || r.right - r.left < 8) return false; // scrolled out of its scrolling region: reached by scrolling, not covered
    const x = (r.left + r.right) / 2;
    const y = (r.top + r.bottom) / 2;
    if (x < 0 || y < 0 || x > vw || y > vh) return false;
    const t = document.elementFromPoint(x, y);
    return !(t && (t === el || el.contains(t)));
  }).map((el) => label(el));
  // Labels that must be whole on screen: the top row (Menu, New chat, Search / Close search) and the open conversation's chip.
  const cut = [];
  for (const el of [...document.querySelectorAll(".bar button, .side > button, .search-toggle, .side button.item.current")].filter(visible)) {
    const r = el.getBoundingClientRect();
    const v = shown(el);
    if (!v || v.right - v.left < r.width - 1 || v.bottom - v.top < r.height - 1 || r.right > vw + 0.5 || r.left < -0.5) cut.push(label(el));
  }
  // The conversation must be readable to its end: scrolled to the bottom, the newest turn is entirely inside the log.
  const lg = document.querySelector(".log");
  let newestInView = null;
  if (lg) {
    const st = lg.scrollTop;
    lg.scrollTop = lg.scrollHeight;
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))); // the virtual list measures real heights on the next frames
    lg.scrollTop = lg.scrollHeight;
    const turns = lg.querySelectorAll(".turn");
    const last = turns[turns.length - 1];
    if (last) { const a = last.getBoundingClientRect(); const b = lg.getBoundingClientRect(); newestInView = a.bottom <= b.bottom + 2 && (a.top >= b.top - 2 || a.height > b.height); }
    lg.scrollTop = st;
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
    small, outside, overlaps, occluded, reach, cut, newestInView, scrolledUp, atTop, noteText, noteShown,
  };
};

export const CURRENT = { w: 0 };
// `relax` scales the log-share floor for a state that adds its own strip to the composer (an attached image).
export const verdicts = (m, keyboard, scoped, relax = 1, searchOpen = false) => {
  const v = [];
  if (scoped) {
    for (const s of m.small) v.push(`target below ${TARGET}px: "${s.el}" ${s.w}x${s.h}`);
    for (const s of m.outside) v.push(`control outside the viewport: "${s.el}"`);
    for (const [a, b] of m.overlaps) v.push(`controls overlap: "${a}" / "${b}"`);
    for (const n of m.occluded) v.push(`covered by another element, so a tap cannot reach it: "${n}"`);
    if (m.pageOverflowX > 0) v.push(`page overflows horizontally by ${m.pageOverflowX}px`);
    if (m.vw !== CURRENT.w) v.push(`the layout viewport is ${m.vw}px wide, not ${CURRENT.w}px: the page is wider than the screen`);
    return v;
  }
  for (const n of m.occluded) v.push(`covered by another element, so a tap cannot reach it: "${n}"`);
  if (m.pageOverflowX > 0) v.push(`page overflows horizontally by ${m.pageOverflowX}px`);
  // A page wider than the device is shrunk to fit, which makes every measurement look smaller than it is.
  if (m.vw !== CURRENT.w) v.push(`the layout viewport is ${m.vw}px wide, not ${CURRENT.w}px: the page is wider than the screen`);
  if (m.pageScrollY !== 0 || m.bodyScrollable > 1) v.push(`the page itself scrolls (scrollY ${m.pageScrollY}, extra ${m.bodyScrollable}px): the conversation must be the scroll region`);
  if (!searchOpen && m.chromeShare > (keyboard ? 0.45 : 0.3)) v.push(`header+conversation strip take ${Math.round(m.chromeShare * 100)}% of the viewport`);
  // The conversation keeps a real share of the screen, keyboard open or closed (40% closed, 35% with the keyboard up, 20% and at least three lines (72px) on a
  // viewport of 512px or less: a phone on its side, or a small phone with the keyboard up), and is readable to its end.
  const floor = (m.vh > 512 ? (keyboard ? 0.35 : 0.4) : Math.max(0.2, 72 / m.vh)) * relax;
  if (!searchOpen && m.log.h < floor * m.vh - 1) v.push(`the log gets ${m.log.h}px, ${Math.round(m.logShare * 100)}% of the viewport (<${Math.round(floor * 100)}%)`);
  if (!searchOpen && m.scrolledUp && !m.reach["Jump to latest"]) v.push('"Jump to latest" is not reachable after scrolling up');
  if (!searchOpen && m.atTop && m.noteText && !m.noteShown) v.push(`the note above the history ("${m.noteText.slice(0, 40)}") is not visible at the top`);
  if (!searchOpen && m.newestInView === false) v.push("scrolled to its end, the newest message is clipped by the log");
  for (const name of m.cut) v.push(`cut off at the edge of its row: "${name}"`);
  // With a search open the strip is the working area (input, filters, results), so the chrome-share rule does not apply to that step.
  // With a search open on a short viewport the keyboard belongs to the search box, so the composer steps aside (and must be back after Close).
  const composerAside = searchOpen && m.vh <= 576;
  const required = searchOpen ? ["Menu", "Close search"] : ["Menu", "New chat", "Search", "Options", "Attach image", "Take photo", "Send"];
  for (const name of required) if (!m.reach[name] && !(composerAside && ["Attach image", "Take photo", "Send"].includes(name))) v.push(`"${name}" is not reachable on screen`);
  if (composerAside) { /* nothing below applies to the composer */ }
  else if (!m.textarea || m.textarea.w === 0) v.push("composer textarea missing");
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


// Real pointer clicks (the mouse, at the centre of each control) on every button inside `scope`; a capture listener records which control
// the click reached and stops it, so nothing is executed (Sign out would end the session). Returns the failures.
export async function pointerCheck(page, scope) {
  await page.evaluate(() => {
    window.__hits = [];
    if (!window.__hitListener) {
      window.__hitListener = (e) => {
        if (!window.__armed) return;
        const b = e.target.closest?.("button, a");
        window.__hits.push(b ? (b.textContent ?? "").trim().slice(0, 40) : `${e.target.tagName}.${String(e.target.className).slice(0, 30)}`);
        e.preventDefault();
        e.stopPropagation();
      };
      document.addEventListener("click", window.__hitListener, true);
    }
    window.__armed = true;
  });
  const failures = [];
  const buttons = page.locator(`${scope} button`);
  for (let i = 0, n = await buttons.count(); i < n; i++) {
    const b = buttons.nth(i);
    if (!(await b.isVisible())) continue;
    const name = ((await b.textContent()) ?? "").trim().slice(0, 40);
    await b.scrollIntoViewIfNeeded();
    const box = await b.boundingBox();
    if (!box) continue;
    await page.evaluate(() => { window.__hits.length = 0; });
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    const hit = await page.evaluate(() => window.__hits.at(-1) ?? "(nothing)");
    if (hit !== name) failures.push(`a pointer click on "${name}" reached "${hit}"`);
  }
  await page.evaluate(() => { window.__armed = false; });
  return failures;
}
