#!/usr/bin/env node
// Real-browser check of the chat UI: phone and desktop layout, the starting card, image attach and send,
// and review screenshots (ONLY=shots).
//
// The browser talks to a small in-script server that serves the built bundle
// (internal/webui/dist) with the same CSP as the Go server and mocks /api with
// the documented contract. It exercises the UI, not the Go backend (which
// internal/chat tests and the Phase 1 gate cover). No Hermes, no credentials.
//
//   cd web && npm ci && npm run build && cd ..
//   PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core node scripts/browser-check.mjs [--out DIR]
//   (CHROME=/path/to/chromium selects the browser; ONLY=mobile-layout|desktop-layout|starting-card|images|blog-public|blog-list|blog-editor|shots|phone-shots|share-image|share-menu|story-card|share-preview|blog-discovery|admin-posts|admin-models|admin-a11y|writing-rules|editor-toolbar|ai-editor)
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
const beaconScript = /cloudflareBeaconScript\s*=\s*"([^"]+)"/.exec(serverGo)[1];
const beaconConnect = /cloudflareBeaconConnect\s*=\s*"([^"]+)"/.exec(serverGo)[1];
const CSP = /h\.Set\("Content-Security-Policy", (.*)\)\n/.exec(serverGo)[1].replace('"+openUIStyleHash+"', styleHash).replace('"+cloudflareBeaconScript+"', beaconScript).replace('"+cloudflareBeaconConnect+"', beaconConnect).slice(1, -1);
const TURNS = 500;
const PAGE = 50;
const PLAIN = "a".repeat(32);
const RICH = "b".repeat(32);

const BLOG_ID = "e".repeat(32);
const BLOG_SHARE_ID = "5".repeat(32); // a draft of ordinary content, for the Share preview
const BLOG_EMPTY_ID = "f".repeat(32);
const BLOG_LONG_ID = "9".repeat(32); // a post too long for a check to read
const BLOG_REVIEW_ID = "8".repeat(32); // a post whose check is ready: Review before publishing
const BLOG_CHECKING_ID = "7".repeat(32); // a post whose check is running
const eid = (n) => String(n).repeat(32);
function readyCheck() {
  return { ...runningCheck(), state: "ready", progress: { ...runningCheck().progress, stage: "writing" }, edits: state.ai.edits };
}
// The progress is the server's: its own clock, the step and the last sign of life (specs/006-ai-editor-blog). state.progress picks what the mock reports.
function runningCheck() {
  const now = Date.now();
  if (!state.progressAnchor) state.progressAnchor = now; // the run's times stay put while the real clock moves on, as they do on the server
  const at = state.progressAnchor, ago = (ms) => new Date(at - ms).toISOString();
  const pr = state.progress ?? { stage: "searching", lookups: 3, quietMs: 4000 };
  return { id: "d".repeat(32), state: "running", reason: "", notes: [], notices: [], blocks: [], progress: { stage: pr.stage, stageSince: ago(20000), lastEventAt: ago(pr.quietMs), lookups: pr.lookups, startedAt: ago(72000), now: new Date(now).toISOString() } };
}
const reviewSource = (host) => ({ url: `https://${host}/data`, title: `About ${host} with a title long enough to wrap on a phone`, retrievedAt: "2026-11-08T10:00:00Z", verified: false });
const REVIEW_CHECK = {
  id: "c".repeat(32), state: "ready", reason: "", notices: [],
  edits: [{ id: "a".repeat(32), kind: "replace", part: 0, anchor: "A", original: "sounds cheap", proposed: "looks cheap", reason: "Shorter.", decision: "open" }, { id: "b".repeat(32), kind: "insert", part: 0, anchor: "A", original: "", proposed: "A small server is enough for one person, but it still needs power, cooling and a spare disk.", reason: "Builds on your points.", decision: "open" }],
  notes: [{ kind: "clarity", text: "The second paragraph buries the question; consider leading with it.", afterNode: 0 }, { kind: "unsourced", text: "A claim with no public source. No figure was added.", afterNode: 1 }, { kind: "style", text: "Hype wording in the opening: revolutionary, seamless, ultimate.", afterNode: 0 }],
  blocks: ["one", "two"].map((n, i) => ({ id: String(i + 1).repeat(32), kind: "summary", afterNode: i, markup: 'root = Card([t])\nt = TextContent("Block ' + n + ' as drawn")', fallback: "plain", decision: "proposed", needsAck: true, sources: [reviewSource(`${n}.example.org`)] })),
};
const BLOG_LIMITS = { checkBytes: 122880, docBytes: 524288, imagesPerPost: 30, imageBytes: 5242880, imageBytesTotal: 209715200, blocksPerCheck: 10, sourcesPerBlock: 10, checksPer24h: 30 };
const text = (t, marks) => ({ type: "text", text: t, ...(marks ? { marks } : {}) });
const BLOG_DOC = {
  type: "doc",
  content: [
    { type: "heading", attrs: { level: 2 }, content: [text("A heading that is long enough to wrap onto a second line on a phone")] },
    { type: "paragraph", content: [text("Words ".repeat(40)), text("https://example.org/a/very/long/path/" + "x".repeat(140), [{ type: "link", attrs: { href: "https://example.org/x" } }])] },
    { type: "bulletList", content: [1, 2].map((n) => ({ type: "listItem", content: [{ type: "paragraph", content: [text("Item " + n + " with some words")] }] })) },
    { type: "blockquote", content: [{ type: "paragraph", content: [text("A quotation. ".repeat(12))] }] },
    { type: "codeBlock", content: [text("const long = " + "y".repeat(220))] },
  ],
};
// Accepted blocks as readers get them: a table with its source, a section that opens and closes, and one the reader must not be
// able to run (a button with an action), which has to fall back to its plain text.
const BLOG_BLOCKS = [
  { anchor: 1, markup: 'root = Card([tbl], [{url: "https://example.org/data", title: "The data", sourceName: "example.org"}])\ntbl = Table([Col("City", ["Jakarta", "Bandung"]), Col("People", [11000000, 2500000], "number")])', fallback: "City / People: Jakarta 11,000,000; Bandung 2,500,000.", sources: [{ url: "https://example.org/data", title: "The data", retrievedAt: "2026-11-08", verified: false }] },
  { anchor: 2, markup: 'root = Card([acc])\nacc = Accordion([i1])\ni1 = AccordionItem("more", "More detail", [t1])\nt1 = TextContent("Detail a reader can open and close.")', fallback: "More detail: Detail a reader can open and close.", sources: [] },
  { anchor: 4, markup: 'root = Card([c])\nc = BarChart(["Seats", "Server", "Container"], [Series("Monthly cost", [85, 10, 24])])', fallback: "Monthly cost: seats 85, server 10, container 24.", sources: [] },
  { anchor: 3, markup: 'root = Card([b])\nb = Buttons([Button("Go", @OpenUrl("https://example.org/"))])', fallback: "This block could not be run; its plain text is shown instead.", sources: [] },
];
const SHARE_POST = {
  title: "Self-hosting an assistant: what it really costs", summary: "A small team asked whether running its own assistant beats paying per seat. Here is what we found.",
  coverUrl: "", authorName: "Maya R.", publishedAt: "2026-11-08T01:00:00Z", blocks: [],
  doc: { type: "doc", content: [
    { type: "paragraph", content: [text("Most teams start by comparing sticker prices. That misses the two costs that decide the answer.")] },
    { type: "heading", attrs: { level: 2 }, content: [text("The three options")] },
    { type: "paragraph", content: [text("Hosted seats are predictable but scale with headcount. A single small server is cheap until someone has to patch it.")] },
    { type: "heading", attrs: { level: 2 }, content: [text("What drives the difference")] },
    { type: "paragraph", content: [text("Seats scale with headcount. A server scales with how careful you are.")] },
  ] },
};
const para = (t) => ({ type: "paragraph", content: [text(t)] });
const head = (t) => ({ type: "heading", attrs: { level: 2 }, content: [text(t)] });
const storyPost = (o) => ({ title: "Self-hosting an assistant: what it really costs", summary: "A small team asked whether running its own assistant beats paying per seat. Here is what we found, with the numbers and where they come from.", coverUrl: "", authorName: "Maya R.", publishedAt: "2026-11-08T01:00:00Z", blocks: [], doc: { type: "doc", content: [para("Most teams start by comparing sticker prices."), head("The three options"), para("Hosted seats scale with headcount."), head("What drives the difference"), para("A server scales with how careful you are.")] }, ...o });
const STORY_SAMPLES = {
  "story-ordinary": () => storyPost({ coverUrl: "/api/public/blog/posts/story-ordinary/assets/cover" }),
  "story-nocover": () => storyPost({}),
  "story-nosummary": () => storyPost({ summary: "" }),
  "story-noheadings": () => storyPost({ doc: { type: "doc", content: [para("Only paragraphs here."), para("Nothing that is a heading.")] } }),
  "story-twelve": () => storyPost({ coverUrl: "/api/public/blog/posts/story-twelve/assets/cover", doc: { type: "doc", content: Array.from({ length: 12 }, (_, i) => head(`Section number ${i + 1} of the post with a heading`)) } }),
  "story-huge": () => storyPost({ coverUrl: "/api/public/blog/posts/story-huge/assets/cover", summary: "A very long summary. ".repeat(200), doc: { type: "doc", content: [para("word ".repeat(20000)), head("One"), head("Two"), head("Three"), head("Four")] } }),
  "story-longtitle": () => storyPost({ coverUrl: "/api/public/blog/posts/story-longtitle/assets/cover", title: "Self-hosting an assistant for a small team: what it really costs once you count the hours, the tokens and the patching nobody planned for" }),
  "story-rtl": () => storyPost({ coverUrl: "/api/public/blog/posts/story-rtl/assets/cover", title: "\u0627\u0633\u062a\u0636\u0627\u0641\u0629 \u0645\u0633\u0627\u0639\u062f \u0628\u0646\u0641\u0633\u0643: \u0643\u0645 \u062a\u0643\u0644\u0641 \u0641\u0639\u0644\u0627\u064b", summary: "\u0633\u0623\u0644\u0646\u0627 \u0641\u0631\u064a\u0642\u0627\u064b \u0635\u063a\u064a\u0631\u0627\u064b \u0647\u0644 \u064a\u063a\u0646\u064a \u062a\u0634\u063a\u064a\u0644 \u0645\u0633\u0627\u0639\u062f\u0647 \u0627\u0644\u062e\u0627\u0635 \u0639\u0646 \u0627\u0644\u062f\u0641\u0639 \u0644\u0643\u0644 \u0645\u0642\u0639\u062f.", doc: { type: "doc", content: [head("\u0627\u0644\u062e\u064a\u0627\u0631\u0627\u062a \u0627\u0644\u062b\u0644\u0627\u062b\u0629"), head("\u0645\u0627 \u0627\u0644\u0630\u064a \u064a\u062d\u062f\u062f \u0627\u0644\u0641\u0631\u0642")] } }),
  "story-emoji": () => storyPost({ title: "Self-hosting an assistant \ud83d\ude80: what it really costs \ud83d\udcb8" }),
};
const blogPost = () => ({ id: BLOG_ID, title: "A long title that has to wrap on a phone without pushing the page sideways", summary: "A summary", slug: "first-post", coverUrl: "", status: "draft", latestVersion: 3, publishedVersion: 0, doc: BLOG_DOC, check: null, updatedAt: "2026-11-09T01:00:00Z" });

const TABLE = 'root = Card([hdr, txt, tbl, chart, btns])\nhdr = CardHeader("Compare", "Three plans side by side")\ntxt = TextContent("**A** is cheapest; **C** has the most storage.")\ntbl = Table([Col("Plan", ["A", "B", "C"]), Col("Cost ($)", [1, 2, 3])])\nchart = BarChart(["A", "B", "C"], [Series("Cost", [1, 2, 3])])\nbtns = Buttons([Button("Tell me more", @ToAssistant("Tell me more about plan A"))])';
const HOSTILE = 'root = Card([btn])\nbtn = Buttons([Button("go", @OpenUrl("javascript:alert(1)"))])';

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

const state = { manyPosts: false, visitor: false, meState: "ACTIVE", noWriting: false, log: [], delayMs: 15, models: false, admin: false, email: "reader@example.test", budgetReached: false, emptyList: false, hold: false, assets: new Map(), deleted: [], sent: [], failNextUpload: false, runs: new Map(), blogCheck: false, takedown: false, notices: [], posts: null, models: null, ai: null, progress: null, progressAnchor: null };
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
  if (req.method === "POST" && /^\/api\/chats\/[^/]+\/messages\/\d+\/summary$/.test(url.pathname)) return send(200, { summary: "# Hosting yourself costs less\n- Seats scale with headcount\n- A small server is cheap until someone patches it" });
  if (url.pathname === "/api/me") return state.visitor ? send(401, {}) : send(200, { email: state.email, displayName: "Reader", requestedAt: "", state: state.meState, ...(state.admin ? { canAdmin: true } : {}) });
  // The blog (specs/001-blog-cms-openui-publish): the same shapes the Go server documents, with text that tries to push a
  // phone sideways (a long unbroken address, a long code line).
  if (url.pathname === "/api/blog/capabilities") return send(200, { writing: !state.noWriting, check: state.blogCheck === true, webSearch: state.blogCheck === true });
  if (url.pathname === "/api/blog/limits") return send(200, BLOG_LIMITS);
  // Spec 004: the administrator's takedown, seen by the author as a list row, a banner, and a blocked Publish.
  if (url.pathname === "/api/blog/notices" && req.method === "GET") return send(200, { notices: state.notices });
  if (/^\/api\/blog\/notices\/[^/]+\/seen$/.test(url.pathname) && req.method === "POST") { state.notices = []; res.writeHead(204); return res.end(); }
  // Spec 005: Admin → Capabilities. The mock keeps the writing-rules switch and answers like the server (counts only, the warning when asked for).
  if (state.admin && state.writing && url.pathname === "/api/admin/tool-policy" && req.method === "GET") return send(200, { tools: [{ name: "web", label: "Web search", description: "Search the web.", state: "on", reason: "" }], custom: false, reverifying: 0 });
  if (state.admin && state.writing && url.pathname === "/api/admin/media-models" && req.method === "GET") return send(200, { image: { current: "", default: "img", options: [{ id: "img", name: "img", free: true }] }, video: { current: "", default: "vid", options: [{ id: "vid", name: "vid", free: false }] }, catalogue: "ok", reverifying: 0 });
  if (state.admin && state.writing && url.pathname === "/api/admin/writing-rules") {
    const view = () => ({ enabled: state.writing.enabled, version: "v1", source: "anti-slop (github.com/miqdadbadjuber/anti-slop)", license: "MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)",
      last24h: state.writing.enabled ? { with: 148, without: 0, notApplied: state.writing.lost } : { with: 63, without: 85, notApplied: 0 }, ...(state.writing.enabled && state.writing.lost ? { warning: "not_applied" } : {}) });
    if (req.method === "GET") return send(200, view());
    if (req.method === "PUT") { let raw = ""; req.on("data", (d) => (raw += d)); req.on("end", () => { if (state.writing.fail) return send(500, { error: { code: "INTERNAL", message: "x" } }); state.writing.enabled = JSON.parse(raw).enabled === true; send(200, view()); }); return; }
  }
  // Spec 006, US3: the editor panel. state.ai picks what the mock reports for the post's check; the decision route records and answers like the server.
  if (state.ai && url.pathname === `/api/blog/posts/${BLOG_ID}/check` && req.method === "POST") { state.ai.phase = "running"; return send(202, runningCheck()); }
  if (state.ai && url.pathname === `/api/blog/posts/${BLOG_ID}/check` && req.method === "GET") return send(200, state.ai.phase === "ready" ? readyCheck() : runningCheck());
  if (state.ai && new RegExp(`^/api/blog/posts/${BLOG_ID}/edits/[0-9a-f]{32}$`).test(url.pathname) && req.method === "PUT") {
    let raw = ""; req.on("data", (d) => (raw += d)); req.on("end", () => { const id = url.pathname.split("/").pop(); const e = state.ai.edits.find((x) => x.id === id); e.decision = JSON.parse(raw).decision; state.ai.decisions.push(`${id.slice(0, 2)}:${e.decision}`); send(200, e); }); return;
  }
  // Spec 004: the Models screen. The mock keeps the switches and defaults, and refuses to switch off a default like the server does.
  if (state.admin && state.models && url.pathname === "/api/admin/models" && req.method === "GET") return send(200, modelsView());
  if (state.admin && state.models && url.pathname === "/api/admin/model-policy" && req.method === "PUT") {
    let raw = ""; req.on("data", (d) => (raw += d)); req.on("end", () => {
      const blocked = JSON.parse(raw).blocked;
      const hit = state.models.rows.filter((m) => blocked.includes(m.id) && m.defaultFor.length > 0 && m.on);
      if (hit.length) return send(409, { error: { code: "MODEL_IS_DEFAULT", message: "x", requestId: "r", purposes: hit[0].defaultFor } });
      state.models.rows.forEach((m) => (m.on = !blocked.includes(m.id)));
      send(200, { blocked });
    });
    return;
  }
  if (state.admin && state.models && url.pathname === "/api/admin/model-defaults" && req.method === "PUT") {
    let raw = ""; req.on("data", (d) => (raw += d)); req.on("end", () => { Object.assign(state.models.current, JSON.parse(raw)); send(200, modelsView()); });
    return;
  }
  if (state.admin && state.models && url.pathname === "/api/admin/media-models" && req.method === "PUT") { send(200, {}); return; }
  if (state.admin && url.pathname === "/api/admin/blog-posts" && req.method === "GET") {
    const f = url.searchParams.get("state");
    const items = state.posts.filter((p) => !f || p.state === f);
    return send(200, { items, next: "" });
  }
  const adminPost = /^\/api\/admin\/blog-posts\/([0-9a-f]{32})(\/take-down|\/restore)?$/.exec(url.pathname);
  if (state.admin && adminPost) {
    const p = state.posts.find((x) => x.id === adminPost[1]);
    if (!p) return send(404, { error: { code: "NOT_FOUND" } });
    if (adminPost[2] === "/take-down" && req.method === "POST") { p.state = "taken_down"; p.takedown = { at: "2026-11-10T00:00:00Z", reason: "Spam", byName: "Admin" }; return send(200, p); }
    if (adminPost[2] === "/restore" && req.method === "POST") { p.state = "visible"; p.takedown = null; return send(200, p); }
    return send(200, { ...p, coverUrl: "", doc: BLOG_DOC, blocks: [] });
  }
  if (url.pathname === "/api/blog/posts" && req.method === "GET")
    return send(200, [BLOG_ID, "c".repeat(32), "d".repeat(32)].map((id, i) => ({ ...(i === 0 && state.takedown ? { takedown: { at: "2026-11-09T00:00:00Z", reason: "Advertising a booking site. See the community rules." } } : {}), id, title: ["A long title that has to wrap on a phone without pushing the page sideways", "", "Second post"][i], status: ["published", "draft", "unpublished"][i], slug: i === 0 ? "first-post" : "", updatedAt: "2026-11-09T01:00:00Z", publishedAt: i === 0 ? "2026-11-08T01:00:00Z" : "", interactive: false })));
  if (url.pathname === `/api/blog/posts/${BLOG_ID}` && req.method === "GET") return send(200, state.takedown ? { ...blogPost(), status: "published", takedown: { at: "2026-11-09T00:00:00Z", reason: "Advertising a booking site. See the community rules." } } : blogPost());
  if (url.pathname === `/api/blog/posts/${BLOG_SHARE_ID}` && req.method === "GET") return send(200, { ...blogPost(), id: BLOG_SHARE_ID, title: SHARE_POST.title, summary: SHARE_POST.summary, slug: "share-post", doc: SHARE_POST.doc });
  if (url.pathname === `/api/blog/posts/${BLOG_EMPTY_ID}` && req.method === "GET") return send(200, { ...blogPost(), id: BLOG_EMPTY_ID, title: "", doc: { type: "doc", content: [{ type: "paragraph" }] } });
  if (url.pathname === `/api/blog/posts/${BLOG_LONG_ID}` && req.method === "GET")
    return send(200, { ...blogPost(), id: BLOG_LONG_ID, title: "Too long to check", doc: { type: "doc", content: Array.from({ length: 130 }, () => ({ type: "paragraph", content: [{ type: "text", text: "word ".repeat(200) }] })) } });
  if (url.pathname === `/api/blog/posts/${BLOG_REVIEW_ID}` && req.method === "GET")
    return send(200, { ...blogPost(), id: BLOG_REVIEW_ID, title: "Self-hosting an assistant", status: "ready", check: REVIEW_CHECK });
  if (url.pathname === `/api/blog/posts/${BLOG_CHECKING_ID}` && req.method === "GET")
    return send(200, { ...blogPost(), id: BLOG_CHECKING_ID, title: "Being checked", status: "checking", check: runningCheck() });
  if (url.pathname === `/api/blog/posts/${BLOG_CHECKING_ID}/check` && req.method === "GET") return send(200, runningCheck());
  if (url.pathname === `/api/blog/posts/${BLOG_ID}` && req.method === "PUT") return send(200, { ...blogPost(), latestVersion: 4 });
  // Spec 003: the discovery checks ask for a page, a limit or a search; the older checks keep their one post.
  if (url.pathname === "/api/public/blog/posts" && (state.manyPosts || ["page", "limit", "q"].some((k) => url.searchParams.has(k)))) {
    const q = (url.searchParams.get("q") || "").toLowerCase();
    const limit = Math.min(20, Number(url.searchParams.get("limit")) || 20);
    const page = Math.max(1, Number(url.searchParams.get("page")) || 1);
    const all = Array.from({ length: 45 }, (_, i) => ({ slug: `disc-${i + 1}`, title: i === 0 ? "Averyveryverylongtitlewithoutanyspacesatallsoitcannotwrapnormally" : `Discovery post ${i + 1}`, summary: `Summary of discovery post ${i + 1} with some words to wrap`, coverUrl: "", authorName: i === 0 ? "Averyveryverylongdisplaynamewithoutanyspacesatall" : i % 2 ? "Dion S." : "Maya R.", publishedAt: "2026-11-08T01:00:00Z" })).filter((p) => !q || p.title.toLowerCase().includes(q));
    return send(200, all.slice((page - 1) * limit, page * limit));
  }
  if (url.pathname === "/api/public/blog/posts") return send(200, [{ slug: "first-post", title: "A long title that has to wrap on a phone without pushing the page sideways", summary: "A summary with " + "words ".repeat(30), coverUrl: "", authorName: "A Writer With A Rather Long Display Name", publishedAt: "2026-11-08T01:00:00Z" }]);
  // A post of ordinary content, for the Share checks (the first-post above carries hostile content on purpose).
  if (url.pathname === "/api/public/blog/posts/share-post") return send(200, SHARE_POST);
  // The story picture's samples (spec 002 quickstart step 6): one post per awkward case.
  const sample = /^\/api\/public\/blog\/posts\/(story-[a-z]+)$/.exec(url.pathname);
  if (sample && STORY_SAMPLES[sample[1]]) return send(200, STORY_SAMPLES[sample[1]]());
  if (/^\/api\/public\/blog\/posts\/story-[a-z]+\/assets\/cover$/.test(url.pathname)) {
    res.writeHead(200, { "content-type": "image/svg+xml", "cache-control": "no-store" });
    return res.end('<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="600" viewBox="0 0 1200 600"><rect width="1200" height="600" fill="#b3c1ff"/><circle cx="800" cy="220" r="150" fill="#ff6fae" stroke="#0b0b0b" stroke-width="6"/><rect y="420" width="1200" height="180" fill="#0b0b0b"/></svg>');
  }
  if (url.pathname === "/api/public/blog/posts/first-post") return send(200, { title: "A long title that has to wrap on a phone without pushing the page sideways", summary: "A summary", coverUrl: "", doc: BLOG_DOC, blocks: BLOG_BLOCKS, authorName: "A Writer", publishedAt: "2026-11-08T01:00:00Z" });
  // The shared plan quota every account sees in the chat (GET /api/quota): shares and reset times, no amounts.
  if (url.pathname === "/api/quota")
    return send(200, { state: "OK", plan: "Studio", windows: [{ key: "week", usedFrac: 0.365, resetsAt: "2026-11-03T05:08:00Z" }, { key: "month", usedFrac: 0.92, resetsAt: "2026-11-19T05:08:00Z" }] });
  // The admin user list (#215): a long name, a long email and every kind of status.
  if (state.admin && url.pathname === "/api/admin/users")
    return send(200, {
      users: [
        { id: "a".repeat(32), email: "raufimusaddiq@example.test", displayName: "Raufi Musaddiq", role: "ADMIN", approvalStatus: "APPROVED", tenantStatus: "READY", tenantCheckedAt: "2026-10-29T00:40:19Z", tenantAttempts: 0 },
        { id: "b".repeat(32), email: "a.rather.long.address.for.a.phone@example.test", displayName: "Raufi Musaddiq", role: "USER", approvalStatus: "PENDING", tenantStatus: "UNPROVISIONED", tenantCheckedAt: "", tenantAttempts: 0 },
        { id: "c".repeat(32), email: "failing@example.test", displayName: "", role: "USER", approvalStatus: "APPROVED", tenantStatus: "FAILED", tenantCheckedAt: "2026-10-28T10:00:00Z", tenantAttempts: 3 },
      ],
    });
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
    // ONLY=phone-shots holds the reply open, so the composer shows Steer and Queue beside Send.
    if (state.hold) return void ev("assistant.delta", { text: "Working on it" });
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
    return void setTimeout(reply, state.delayMs);
  }
  // The rest of what the chat screen reads on load, with the real response shapes, so no failure notice distorts the layout.
  if (url.pathname === "/api/capabilities") return send(200, { capabilities: [...(state.models ? [{ id: "models", label: "Models", status: "available" }] : []), ...(state.hold ? [{ id: "steer", label: "Steer", status: "available" }] : [])], toolsets: [], providers: [], skills: [] });
  // ONLY=shots shows the model picker with a long catalogue, as in production.
  if (state.models && url.pathname === "/api/models") {
    const model = (i) => ({ id: `model-${String(i).padStart(2, "0")}-${["flash", "pro", "mini", "vision"][i % 4]}`, available: true, hidden: false, reasoning: i % 3 === 0, reasoningEfforts: i % 3 === 0 ? ["low", "high"] : [], modalities: { input: i % 4 === 3 ? ["text", "image"] : ["text"], output: ["text"] }, contextWindow: 128000, price: null });
    return send(200, { source: "kenari", fetchedAt: "2026-10-28T08:00:00Z", stale: false, default: { provider: "custom", model: "model-00-flash" }, unsupported: {}, providers: [{ slug: "custom", name: "Kenari", available: true, models: Array.from({ length: 40 }, (_, i) => model(i)) }] });
  }
  // Settings → Memory & skills (#248): what the assistant remembers, with a long entry that must wrap.
  if (url.pathname === "/api/me/memory") return send(200, { partial: [], user: ["Prefers short answers in English, with code samples in Go.", "Lives in Jakarta (UTC+7); works on a self-hosted chat app called Antares and wants changes deployed and smoke-tested end to end without being asked each time.", "Dislikes yellow in UIs."], memory: ["Production is antares.investdx.biz.id; deploys are checked with the smoke test."] });
  // Settings → Usage with production-like figures: long chat titles and seven-digit token counts.
  if (url.pathname === "/api/me/usage") {
    const t = (runs, input, output) => ({ runs, unreported: 0, unpriced: runs, input, output, cacheRead: 0, estimate: null });
    return send(200, { days: 7, since: "2026-10-23", currency: "IDR", priceDate: "2026-10-23", total: t(37, 1267791, 34914), byModel: [{ model: "minimax-m3-1-flash", ...t(37, 1267791, 34914) }], byChat: [{ chatId: PLAIN, title: "Kemampuan yang Bisa Dibantu", deleted: false, ...t(5, 340000, 6743) }, { chatId: RICH, title: "Saran Game untuk Weekend", deleted: false, ...t(1, 38000, 1047) }, { chatId: "", title: "", deleted: true, ...t(29, 830000, 21168), unreported: 9 }], byDay: Array.from({ length: 7 }, (_, i) => ({ day: `2026-10-${23 + i}`, ...t(i * 2, 40000 * i * i, 1000 * i) })) });
  }
  if (url.pathname === "/api/me/templates") return send(200, { templates: [] });
  if (url.pathname === "/api/blog/capabilities") return send(200, { writing: true, check: state.blogCheck === true, webSearch: state.blogCheck === true });
  if (url.pathname === "/api/blog/limits") return send(200, BLOG_LIMITS);
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

// Phone layout (#138) in a real browser against the mock backend, so it runs in CI with no scratch server: the same measurements
// and verdicts at 320/375/390/430 portrait and a phone on its side, with the keyboard closed and
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
    check(`${name}, the Menu popover opens and each action (${items.map((i) => i[0]).join(", ")}) is reachable`, items.length >= 2 && items.every((i) => i[1]), JSON.stringify(items.filter((i) => !i[1])));
    await page.screenshot({ path: path.join(outDir, `desktop-layout-${w}-menu.png`) });
    await page.keyboard.press("Escape");
    check(`${name}, Escape closes the Menu popover`, (await menu.getAttribute("aria-expanded")) === "false");
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

// The blog pages on a phone and a wide screen, light and dark: reading a post and the index signed out, and the author's list
// and editor. Each page must stay inside the viewport (no sideways scroll), raise no console error or CSP violation and
// make no off-origin request, and show its main content.
async function blogPages(browser, which, onlyViews) {
  const views = which === "blog-public"
    ? [["index", "/b", "h1, h2"], ["post", "/b/first-post", "article h1, main h1"]]
    : [["list", "/blog", ".blog-rows, .blog-empty"], ["editor", `/blog/${BLOG_ID}`, "[contenteditable=true]"], ["empty editor", `/blog/${BLOG_EMPTY_ID}`, "[contenteditable=true]"], ["publish", `/blog/${BLOG_ID}/publish`, ".blog-limits"], ["publish too long", `/blog/${BLOG_LONG_ID}/publish`, ".blog-limits"], ["review", `/blog/${BLOG_REVIEW_ID}/publish`, ".blog-review-cols"], ["checking", `/blog/${BLOG_CHECKING_ID}/publish`, ".blog-takeover h1"]].filter(([name]) => !onlyViews || onlyViews.includes(name));
  for (const [label, w, h, scheme] of [["phone", 390, 844, "light"], ["phone", 390, 844, "dark"], ["narrow", 320, 640, "light"], ["desktop", 1280, 800, "dark"]]) {
    for (const [name, route, want] of views) {
      state.blogCheck = name.startsWith("publish"); // the Publish screens are shown with checking available
      const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: w, height: h }, colorScheme: scheme, ...(label !== "desktop" ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) });
      const logFrom = state.log.length;
      await page.goto(base + route);
      let shown = true;
      try { await page.waitForSelector(want, { timeout: 8000 }); } catch { shown = false; }
      await page.waitForTimeout(300);
      // The page must fit the device: innerWidth is the layout viewport, which a mobile browser widens (and then zooms out, or scrolls
      // sideways) when some content cannot shrink; scrollWidth minus innerWidth alone reads 0 once that has happened (#314).
      const m = await page.evaluate((devW) => ({ overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth, bodyOverflow: document.body.scrollWidth - document.documentElement.clientWidth, widened: innerWidth - devW }), w);
      // The editor's own layout: the toolbar must clear the writing area (also while it has the focus ring), and an empty body must
      // show the design's placeholder.
      let editorProblems = [];
      // The list: no row's title may run under its own actions (a button does not wrap text unless told to).
      if (which === "blog-editor" && name === "list" && shown) {
        const clash = await page.evaluate(() => [...document.querySelectorAll(".blog-row")].flatMap((row) => {
          const a = row.querySelector(".blog-row-title")?.getBoundingClientRect();
          return [...row.querySelectorAll(".blog-row-end > *")].flatMap((el) => {
            const b = el.getBoundingClientRect();
            return a && a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom ? [el.textContent] : [];
          });
        }));
        if (clash.length) editorProblems.push(`a post title runs under its actions: ${JSON.stringify(clash)}`);
        // Every row says what state its post is in, and the page says who can see what.
        const shape = await page.evaluate(() => ({
          rows: document.querySelectorAll(".blog-row").length,
          withChip: document.querySelectorAll(".blog-row .chip").length,
          note: document.querySelector(".blog-bar-note")?.textContent ?? "",
        }));
        if (shape.rows === 0 || shape.withChip < shape.rows) editorProblems.push(`${shape.rows} rows but ${shape.withChip} status chips: every post shows its state`);
        if (!/Only you can see drafts/.test(shape.note)) editorProblems.push(`the list does not say who can see drafts (${JSON.stringify(shape.note)})`);
        // The rail beside the page (a strip above it on a phone) and a row's "⋯" menu, which opens inside the viewport.
        const rail = await page.evaluate(() => {
          const r = document.querySelector(".blog-rail")?.getBoundingClientRect();
          const page = document.querySelector(".blog-list")?.getBoundingClientRect();
          return { has: !!r && !!page, beside: !!r && !!page && r.right <= page.left + 1, above: !!r && !!page && r.bottom <= page.top + 1, current: document.querySelector('.blog-rail [aria-current="page"]')?.textContent ?? "" };
        });
        if (!rail.has) editorProblems.push("the list has no rail");
        else if (!(w >= 840 ? rail.beside : rail.above)) editorProblems.push(`the rail is not ${w >= 840 ? "beside" : "above"} the page: ${JSON.stringify(rail)}`);
        if (rail.current !== "Blog posts") editorProblems.push(`the rail does not mark Blog posts as the current page: ${JSON.stringify(rail)}`);
        await page.locator(".blog-more").first().click();
        const menu = await page.evaluate(() => {
          const m = document.querySelector('[role="menu"]')?.getBoundingClientRect();
          return { open: !!m, inside: !!m && m.left >= -0.5 && m.right <= innerWidth + 0.5 && m.top >= -0.5, focused: document.activeElement?.getAttribute("role") === "menuitem", items: [...document.querySelectorAll('[role="menuitem"]')].map((e) => e.textContent) };
        });
        if (!menu.open || !menu.inside || !menu.focused) editorProblems.push(`the row menu does not open inside the viewport with focus in it: ${JSON.stringify(menu)}`);
        if (!menu.items.includes("Delete") || !menu.items.includes("Duplicate")) editorProblems.push(`the row menu is missing its actions: ${JSON.stringify(menu.items)}`);
        await page.screenshot({ path: path.join(outDir, `blog-list-menu-${label}-${scheme}.png`) });
        await page.keyboard.press("Escape");
      }
      // Publish is a dialog over the editor: modal, inside the viewport, the editor still there but out of reach; the limits are
      // stated before anyone publishes, and a post too long to check says so and cannot start one.
      if (which === "blog-editor" && name.startsWith("publish") && shown) {
        const f = await page.evaluate(() => {
          const dlg = document.querySelector('.blog-dialog[role="dialog"]');
          const r = dlg?.getBoundingClientRect();
          const btn = (re) => [...(dlg?.querySelectorAll("button") ?? [])].find((b) => re.test(b.textContent ?? ""));
          return {
            modal: dlg?.getAttribute("aria-modal") === "true",
            named: dlg?.getAttribute("aria-labelledby") === "publish-title" && document.getElementById("publish-title")?.textContent === "Publish this post",
            inside: !!r && r.left >= 0 && r.top >= 0 && r.right <= innerWidth + 0.5 && r.bottom <= innerHeight + 0.5,
            behind: { editor: !!document.querySelector(".blog-behind .blog-editor"), inert: document.querySelector(".blog-behind")?.hasAttribute("inert") === true },
            focusIn: !!dlg && dlg.contains(document.activeElement),
            text: dlg?.querySelector(".blog-limits")?.textContent ?? "",
            alert: dlg?.querySelector(".blog-limits [role=alert]")?.textContent ?? "",
            check: btn(/^Check and enrich$/)?.disabled,
            original: btn(/^Publish my original$/)?.disabled,
          };
        });
        if (!f.modal || !f.named) editorProblems.push(`Publish is not a named modal dialog: ${JSON.stringify(f)}`);
        if (!f.inside) editorProblems.push("the Publish dialog does not fit inside the viewport");
        if (!f.behind.editor || !f.behind.inert) editorProblems.push(`the editor is not behind the dialog and out of reach: ${JSON.stringify(f.behind)}`);
        if (!f.focusIn) editorProblems.push("focus did not move into the Publish dialog");
        if (!/up to 10 blocks/.test(f.text) || !/Images: \d+ of 30/.test(f.text)) editorProblems.push(`the limits are not all stated: ${JSON.stringify(f.text)}`);
        if (name === "publish" && (f.alert !== "" || f.check !== false)) editorProblems.push(`a post of ordinary length is refused a check: ${JSON.stringify(f)}`);
        if (name === "publish too long" && (!/longer than the assistant can read/.test(f.alert) || f.check !== true || f.original !== false)) editorProblems.push(`a post too long to check is not told so, or can start one: ${JSON.stringify(f)}`);
      }
      // The check is running: the step says so, can be cancelled, and has its way back to editing.
      if (which === "blog-editor" && name === "checking" && shown) {
        const c = await page.evaluate(() => ({
          h1: document.querySelector(".blog-takeover h1")?.textContent,
          cancel: !![...document.querySelectorAll(".blog-takeover button")].find((b) => b.textContent === "Cancel"),
          back: !![...document.querySelectorAll(".blog-takeover-bar button")].find((b) => /Back to editing/.test(b.textContent ?? "")),
          editorHidden: getComputedStyle(document.querySelector(".blog-behind")).display === "none",
          focused: document.activeElement?.tagName === "H1",
          innerWidth,
        }));
        if (c.h1 !== "Checking your writing" || !c.cancel || !c.back || !c.editorHidden || !c.focused || c.innerWidth !== w) editorProblems.push(`the checking step is not as it should be: ${JSON.stringify(c)}`);
        // Spec 006, US2: the steps, the clock and the sign of life; no bar or percentage; the live region names the step; Cancel and Back never overlap.
        const pr = await page.evaluate(() => ({
          cur: document.querySelector(".step-current")?.textContent,
          done: [...document.querySelectorAll(".step-done")].map((e) => e.textContent),
          pending: document.querySelectorAll(".step-pending").length,
          clock: document.querySelector(".step-clock")?.textContent,
          clockHidden: document.querySelector(".step-clock")?.getAttribute("aria-hidden"),
          live: document.querySelector("[aria-live=polite]")?.textContent,
          bar: !!document.querySelector("progress, [role=progressbar]") || /\d+\s?%/.test(document.body.innerText),
          stalled: !!document.querySelector(".step-stalled"),
          short: [...document.querySelectorAll(".blog-takeover button")].filter((b) => b.getBoundingClientRect().height > 0 && b.getBoundingClientRect().height < 43.5).length,
        }));
        if (!/Looking things up · 3 lookups/.test(pr.cur ?? "") || pr.done.length !== 1 || pr.pending !== 1 || !/1 min \d+ s so far.*Last sign of life \d+ s ago/.test(pr.clock ?? "") || pr.clockHidden !== "true" || pr.live !== "Looking things up" || pr.bar || pr.stalled) editorProblems.push(`the progress steps are not as designed: ${JSON.stringify(pr)}`);
        if (w < 500 && pr.short) editorProblems.push(`${pr.short} buttons on the checking screen are under 44 px`);
        const ov = await page.evaluate(() => { const bs = [...document.querySelectorAll(".blog-takeover button, .blog-takeover a")].filter((b) => b.getBoundingClientRect().width > 0); let n = 0; for (let i = 0; i < bs.length; i++) for (let j = i + 1; j < bs.length; j++) { const a = bs[i].getBoundingClientRect(), b = bs[j].getBoundingClientRect(); if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) n++; } return n; });
        if (ov) editorProblems.push(`${ov} controls overlap on the checking screen`);
        await page.screenshot({ path: path.join(outDir, `checking-steps-${label}-${scheme}.png`), fullPage: true });
        // The clock ticks on its own, and a reload shows the real step (the mock reports writing), and a quiet run says it is still working.
        const t0 = await page.locator(".step-clock").textContent();
        await page.waitForTimeout(2200);
        const t1 = await page.locator(".step-clock").textContent();
        if (t0 === t1) editorProblems.push(`the clock did not tick: ${t0}`);
        state.progress = { stage: "writing", lookups: 3, quietMs: 52000 }; state.progressAnchor = null;
        await page.reload();
        await page.waitForSelector(".step-current", { timeout: 8000 });
        const re = await page.evaluate(() => ({ cur: document.querySelector(".step-current")?.textContent, done: document.querySelectorAll(".step-done").length, stalled: document.querySelector(".step-stalled")?.textContent, live: document.querySelector("[aria-live=polite]")?.textContent }));
        state.progress = null; state.progressAnchor = null;
        if (!/Writing suggestions/.test(re.cur ?? "") || re.done !== 2 || !/Still working\. Nothing new for 5\d s/.test(re.stalled ?? "") || !/Nothing new for a while/.test(re.live ?? "")) editorProblems.push(`a reload did not show the real step, or a quiet run did not say so: ${JSON.stringify(re)}`);
      }
      // Review before publishing: the design's parts in the design's order, inside the viewport, and the way through is gated on the ticks.
      if (which === "blog-editor" && name === "review" && shown) {
        const r = await page.evaluate(() => {
          const box = (sel) => document.querySelector(sel)?.getBoundingClientRect();
          const o = box(".blog-original"), e = box(".blog-enriched"), n = box(".blog-notes"), d = box(".blog-drawer");
          const visible = (b) => !!b && b.width > 0 && b.height > 0;
          const all = [...document.querySelectorAll("button")].find((b) => /Accept all and publish/.test(b.textContent ?? ""));
          return {
            heading: document.querySelector(".blog-review h1")?.textContent,
            originalShown: visible(o), enriched: visible(e), notes: visible(n), drawer: visible(d),
            sideBySide: visible(o) && visible(e) && visible(n) && o.right <= e.left + 1 && e.right <= n.left + 1,
            stacked: visible(e) && visible(n) && e.bottom <= n.top + 1,
            blocks: document.querySelectorAll(".blog-enriched .blog-block").length,
            allDisabled: all?.disabled, drawerSources: document.querySelectorAll(".blog-drawer li").length,
            innerW: innerWidth,
          };
        });
        // Nothing in the screen may run past the edge, and the browser must not have widened its layout to fit something that would not narrow.
        const innerW = r.innerW;
        if (innerW !== w) editorProblems.push(`the layout viewport is ${innerW}px on a ${w}px screen: something in the page cannot get narrower`);
        const past = await page.evaluate(() => [...document.querySelectorAll(".blog-review *")].filter((e) => { const r = e.getBoundingClientRect(); return !e.closest("pre") && r.width > 0 && (r.right > innerWidth + 1 || r.left < -1); }).slice(0, 5).map((e) => `${e.tagName}.${e.className} ${Math.round(e.getBoundingClientRect().left)}..${Math.round(e.getBoundingClientRect().right)} of ${innerWidth}`));
        if (past.length) editorProblems.push(`the review screen runs past the edge of the screen: ${JSON.stringify(past)}`);
        const wide = w >= 1000;
        if (r.heading !== "Review before publishing" || r.blocks !== 2 || !r.enriched || !r.notes || !r.drawer) editorProblems.push(`the review screen is missing parts: ${JSON.stringify(r)}`);
        if (wide && !r.sideBySide) editorProblems.push(`on a wide screen the original, the preview and the notes are not side by side: ${JSON.stringify(r)}`);
        if (!wide && (r.originalShown || !r.stacked)) editorProblems.push(`on a narrow screen the preview and the notes are not stacked (the original is left out): ${JSON.stringify(r)}`);
        if (r.allDisabled !== true || r.drawerSources !== 1) editorProblems.push(`Accept all must wait for the ticks, and the drawer lists the selected block's sources: ${JSON.stringify(r)}`);
        for (const b of await page.locator(".blog-enriched input[type=checkbox]").all()) {
          try {
            await b.check({ timeout: 3000 });
          } catch (e) {
            // Something sits over the tick box: say what, and where, instead of stopping the whole check.
            await page.screenshot({ path: path.join(outDir, `review-stuck-${label}-${scheme}.png`) });
            const over = await b.evaluate((el) => {
              const r = el.getBoundingClientRect();
              const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
              const tk = document.querySelector(".blog-takeover");
              const past = [...document.querySelectorAll(".blog-takeover *")].filter((x) => x.getBoundingClientRect().width > innerWidth + 1).slice(0, 4).map((x) => `${x.tagName}.${x.className} ${Math.round(x.getBoundingClientRect().left)}..${Math.round(x.getBoundingClientRect().right)}`);
              return { box: [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)], over: top ? `${top.tagName}.${top.className}` : null, takeover: tk ? [tk.scrollLeft, tk.scrollWidth, tk.clientWidth] : null, innerWidth, past };
            });
            editorProblems.push(`a tick box cannot be pressed (covered): ${JSON.stringify(over)} ${String(e.message).replace(/\s+/g, " ").slice(0, 400)}`);
            break;
          }
        }
        const after = await page.evaluate(() => [...document.querySelectorAll("button")].find((b) => /Accept all and publish/.test(b.textContent ?? ""))?.disabled);
        if (after !== false) editorProblems.push("Accept all and publish stays disabled after every block is ticked");
        // Style notes (specs/005-antislop-writing): shown with their chip, every Dismiss button is named and at least 44 px tall, none overlaps
        // another control, dismissing one removes only it and moves focus, and Publish stays available.
        const sn = await page.evaluate(() => {
          const notes = [...document.querySelectorAll(".blog-notes .blog-note")];
          const btns = [...document.querySelectorAll(".blog-notes button")];
          const rects = btns.map((b) => b.getBoundingClientRect());
          let overlap = 0;
          for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) { const a = rects[i], b = rects[j]; if (a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom) overlap++; }
          return { notes: notes.length, style: notes.filter((n) => n.querySelector(".chip")?.textContent === "Style").length, names: btns.map((b) => b.textContent), tall: rects.every((r) => r.height >= 43.5), overlap, heading: document.querySelector("#notes-h")?.textContent };
        });
        const sgs = await page.evaluate(() => ({ cards: document.querySelectorAll(".blog-review-side .suggestions-compact .suggestion").length, inCard: !!document.querySelector(".blog-review-side .suggestions-compact.blog-notes"), heading: document.querySelector(".blog-review-side .suggestions-compact h3")?.textContent }));
        if (sgs.cards !== 2 || !sgs.inCard || sgs.heading !== "Suggestions (2)") editorProblems.push(`the review screen's suggestions are not the design's card of one-line suggestions: ${JSON.stringify(sgs)}`);
        if (sn.notes !== 3 || sn.style !== 1 || !sn.names.every((n) => n === "Dismiss this note") || !sn.tall || sn.overlap) editorProblems.push(`style notes are wrong: ${JSON.stringify(sn)}`);
        await page.getByRole("button", { name: "Dismiss this note" }).last().click();
        const dn = await page.evaluate(() => ({ heading: document.querySelector("#notes-h")?.textContent, shown: document.querySelectorAll(".blog-notes .blog-note").length, styleLeft: [...document.querySelectorAll(".blog-notes .chip")].some((c) => c.textContent === "Style"), status: document.querySelector(".blog-notes [role=status]")?.textContent, publish: [...document.querySelectorAll("button")].filter((b) => /publish/i.test(b.textContent ?? "") && !/Accept all/.test(b.textContent ?? "")).every((b) => !b.disabled) }));
        if (dn.shown !== 2 || dn.styleLeft || dn.status !== "1 note dismissed." || !dn.publish) editorProblems.push(`dismissing a style note went wrong: ${JSON.stringify(dn)}`);
      }
      if (which === "blog-editor" && name.endsWith("editor") && shown) {
        await page.focus("[contenteditable=true]");
        const g = await page.evaluate(() => {
          const title = document.querySelector(".blog-title")?.getBoundingClientRect();
          const t = document.querySelector(".blog-toolbar")?.getBoundingClientRect();
          const prose = document.querySelector(".blog-prose");
          const p = prose?.getBoundingClientRect();
          const inset = prose ? parseFloat(getComputedStyle(prose).paddingLeft) : 0;
          const body = document.querySelector(".blog-body");
          const ph = body ? getComputedStyle(body, "::before").content : "";
          return { flush: title && t && p ? Math.max(Math.abs(title.left - t.left), Math.abs(t.left - (p.left + inset))) : null, gap: t && p ? p.top - t.bottom : -999, empty: body?.getAttribute("data-empty") === "true", placeholder: ph };
        });
        // The design puts the toolbar 18px above the body (frame "Editor (empty)"); the 3px shadow, the 3px focus ring and its 2px offset need 8.
        if (g.gap < 17) editorProblems.push(`the toolbar is ${g.gap}px above the writing area (the design has 18; its shadow and the focus ring need 8)`);
        if (g.flush === null) editorProblems.push("the title, toolbar or writing area is missing, so the left edges cannot be compared");
        else if (g.flush > 1) editorProblems.push(`the title, toolbar and body text do not share a left edge (off by ${g.flush}px); the design has them flush`);
        if (name === "empty editor" && (!g.empty || !/Tell your story/.test(g.placeholder))) editorProblems.push(`an empty body shows no placeholder (${JSON.stringify(g)})`);
        if (name === "editor" && g.empty) editorProblems.push("a body with text is marked empty");
      }
      // The reader's blocks (T064): drawn, within the page, keyboard-operable, and the one that must not run falls back to its text.
      if (which === "blog-public" && name === "post" && shown) {
        // The block renderer loads on demand: wait for the two blocks rather than for a fixed time.
        // (A locator, not waitForFunction: that one evaluates code in the page, which the page's CSP refuses.)
        await page.locator(".openui-root").nth(1).waitFor({ timeout: 15000 }).catch(() => {});
        const r = await page.evaluate(() => ({
          drawn: document.querySelectorAll(".openui-root").length,
          plain: [...document.querySelectorAll(".blog-block-plain")].map((e) => e.textContent),
          buttons: document.querySelectorAll(".blog-doc button:not(.openui-accordion-trigger)").length,
          triggers: document.querySelectorAll(".openui-accordion-trigger").length,
        }));
        if (r.drawn !== 3) editorProblems.push(`${r.drawn} blocks were drawn, expected 3`);
        if (r.plain.length !== 1 || !/could not be run/.test(r.plain[0] ?? "")) editorProblems.push(`the block with a button did not fall back to its text: ${JSON.stringify(r.plain)}`);
        if (r.buttons !== 0) editorProblems.push(`${r.buttons} buttons in the post: a reader must not be offered one`);
        // Keyboard: Tab reaches the section's trigger, and Enter closes and reopens it.
        let reached = false;
        for (let i = 0; i < 60 && !reached; i++) {
          await page.keyboard.press("Tab");
          reached = await page.evaluate(() => document.activeElement?.classList.contains("openui-accordion-trigger") ?? false);
        }
        if (!reached) editorProblems.push("Tab never reached the section's trigger");
        else {
          const open = () => page.evaluate(() => document.activeElement?.getAttribute("aria-expanded"));
          const was = await open();
          await page.keyboard.press("Enter");
          const now = await open();
          if (was === now) editorProblems.push(`Enter did not toggle the section (aria-expanded stayed ${was})`);
        }
        // Hover (T059): a chart reacts to the pointer (its highlight band follows the bar and fades out when the pointer leaves),
        // and hovering asks the server for nothing. A pointer is a desktop thing; a phone has touch.
        if (label === "desktop") {
          const asked = state.log.length;
          const band = () => page.evaluate(() => { const g = document.querySelector(".openui-bar-chart-hover-highlight")?.parentElement; return g ? { opacity: Number(getComputedStyle(g).opacity), at: g.style.transform } : null; });
          const bars = page.locator(".openui-bar-chart-bar");
          if ((await bars.count()) !== 3 || !(await band())) editorProblems.push("the chart block has no three bars and highlight band to hover");
          else {
            await bars.first().scrollIntoViewIfNeeded();
            if ((await band()).opacity !== 0) editorProblems.push("the highlight band shows before anything is hovered");
            const seen = new Set();
            for (let i = 0; i < 3; i++) {
              const bx = await bars.nth(i).boundingBox();
              await page.mouse.move(bx.x + bx.width / 2, bx.y + bx.height / 2);
              await page.waitForTimeout(400);
              const h = await band();
              if (h.opacity !== 1) editorProblems.push(`hovering bar ${i + 1} did not show the highlight (opacity ${h.opacity})`);
              seen.add(h.at);
            }
            if (seen.size !== 3) editorProblems.push(`the highlight does not follow the bar under the pointer: ${JSON.stringify([...seen])}`);
            await page.mouse.move(2, 2);
            await page.waitForTimeout(500);
            if ((await band()).opacity !== 0) editorProblems.push("the highlight stays after the pointer leaves");
          }
          if (state.log.length !== asked) editorProblems.push(`hovering made ${state.log.length - asked} request(s): ${JSON.stringify(state.log.slice(asked))}`);
        }
      }
      await page.screenshot({ path: path.join(outDir, `${which}-${name}-${label}-${scheme}.png`), fullPage: true });
      const csp = await page.evaluate(() => window.__csp.length);
      // Signed out means the page never asks who it is: the app shell (which calls /api/me and the chat APIs) is not mounted.
      // Spec 003: the one thing a public page asks is who is reading (a single GET /api/me, answered 401 for a visitor, which the browser logs as an error).
      const meCalls = which === "blog-public" ? state.log.slice(logFrom).filter((l) => l === "GET /api/me").length : 0;
      const asked = which === "blog-public" ? state.log.slice(logFrom).filter((l) => /^\w+ \/api\/(chats|quota|capabilities|models)\b/.test(l)).concat(meCalls > 1 ? [`GET /api/me x${meCalls}`] : []) : [];
      if (which === "blog-public") for (let i = consoleErrors.length - 1; i >= 0; i--) if (/status of 401/.test(consoleErrors[i]) && meCalls === 1) { consoleErrors.splice(i, 1); break; }
      check(`${which} ${name}, ${label} ${w}px ${scheme}: content shown, no sideways scroll, no console error, CSP violation or off-origin request${which === "blog-public" ? ", and no signed-in API call" : ""}`,
        shown && m.overflowX <= 0 && m.bodyOverflow <= 0 && m.widened <= 0 && consoleErrors.length === 0 && csp === 0 && external.length === 0 && asked.length === 0 && editorProblems.length === 0,
        JSON.stringify({ shown, ...m, csp, consoleErrors: consoleErrors.slice(0, 2), external: external.slice(0, 2), asked: asked.slice(0, 3), editorProblems }));
      await ctx.close();
    }
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

// ONLY=shots: review screenshots (no checks) of the chat, Options closed and open, light and dark, desktop and phone,
// for an owner's visual review (#139). Run before and after a visual change and compare the folders.
// The model picker (#139), opened over a 40-model catalogue at phone and desktop widths: it stays on screen, the page
// does not overflow sideways, the list scrolls inside the popover, and on a touch screen its buttons are 44px tall.
async function modelPicker(browser) {
  state.models = true;
  try {
    for (const { w, h, touch } of [{ w: 320, h: 568, touch: true }, { w: 390, h: 844, touch: true }, { w: 568, h: 320, touch: true }, { w: 641, h: 800, touch: false }, { w: 700, h: 700, touch: false }, { w: 900, h: 700, touch: false }, { w: 1280, h: 800, touch: false }]) {
      const { ctx, page } = await openPage(browser, { viewport: { width: w, height: h }, isMobile: touch, hasTouch: touch });
      await page.goto(base + "/");
      await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
      await page.locator(".composer-chips summary", { hasText: "Model" }).first().click();
      await page.waitForSelector(".model-menu .model-row");
      const m = await page.evaluate(() => {
        const menu = document.querySelector(".model-menu");
        const r = menu.getBoundingClientRect();
        const buttons = [...menu.querySelectorAll(".model-row button")].slice(0, 5).map((b) => b.getBoundingClientRect().height);
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, vw: innerWidth, vh: innerHeight, overflowX: document.documentElement.scrollWidth - innerWidth, scrolls: menu.scrollHeight > menu.clientHeight + 10, minButton: buttons.length ? Math.min(...buttons) : 0 }; // no button found measures as 0, never as a pass
      });
      const onScreen = m.left >= 0 && m.right <= m.vw && m.top >= 0 && m.bottom <= m.vh;
      check(`model picker ${w}px: on screen, no sideways overflow, scrolls inside`, onScreen && m.overflowX <= 0 && m.scrolls, JSON.stringify(m));
      if (touch) check(`model picker ${w}px: Hide/Show are 44px tall on touch`, m.minButton >= 44, `${m.minButton}px`);
      await ctx.close();
    }
  } finally {
    state.models = false;
  }
}

// #215: Admin → Users on a phone, at 320 and 390px, at the default size and with a 20px root font. Each account is a
// card whose name and email get most of the width (the table left them ~60px, broken letter by letter), nothing scrolls
// sideways, no nav label breaks inside itself, every cell names its column in visible text, and the list is still a
// table to assistive technology: five column headers and a row header per account.
async function adminPhone(browser) {
  state.admin = true;
  try {
    for (const w of [320, 390]) {
      for (const big of [false, true]) {
        const { ctx, page } = await openPage(browser, { viewport: { width: w, height: 760 }, isMobile: true, hasTouch: true });
        await page.goto(base + "/admin/users");
        await page.waitForSelector(".admin-table tbody tr");
        if (big) await page.evaluate(() => (document.documentElement.style.fontSize = "20px"));
        await page.waitForTimeout(200);
        const m = await page.evaluate(() => {
          const names = [...document.querySelectorAll(".admin-table tbody th")].map((th) => th.getBoundingClientRect().width);
          const cells = [...document.querySelectorAll(".admin-table tbody td")];
          return {
            overflowX: document.documentElement.scrollWidth - innerWidth,
            minName: Math.round(Math.min(...names)),
            vw: innerWidth,
            // A label on two lines has two line boxes for its text (the link's own height counts padding and the 44px target).
            brokenNav: [...document.querySelectorAll(".admin-nav a")].filter((a) => { const r = document.createRange(); r.selectNodeContents(a); return new Set([...r.getClientRects()].map((b) => Math.round(b.top))).size > 1; }).map((a) => a.textContent),
            unlabelled: cells.filter((td) => { const l = td.querySelector(".cell-label"); return !l || !l.textContent || l.getBoundingClientRect().width === 0; }).length,
          };
        });
        const headers = await page.getByRole("columnheader").count();
        const rows = await page.getByRole("rowheader").count();
        // innerWidth must stay the phone's: content wider than the screen makes a mobile browser widen the layout
        // viewport (the old table took a 320px phone to 478px), which would hide the overflow from overflowX.
        const ok = m.vw === w && m.overflowX <= 0 && m.minName >= w * 0.6 && m.brokenNav.length === 0 && m.unlabelled === 0 && headers === 5 && rows === 3;
        check(`admin users ${w}px${big ? " (20px text)" : ""}: cards, labelled cells, unbroken nav, still a table`, ok, JSON.stringify({ ...m, headers, rows }));
        await ctx.close();
      }
    }
    // Between the phone layout and a wide desktop the bar and nav must still shrink: the page never scrolls sideways
    // and the signed-in email stays on screen (a nowrap nav once pushed both out from 640 to 1015px).
    for (const w of [641, 700, 860, 900, 960, 961, 1000, 1016]) {
      const { ctx, page } = await openPage(browser, { viewport: { width: w, height: 760 } });
      await page.goto(base + "/admin/users");
      await page.waitForSelector(".admin-table tbody tr");
      const m = await page.evaluate(() => ({ vw: innerWidth, overflowX: document.documentElement.scrollWidth - innerWidth, who: Math.round(document.querySelector(".admin-app .who-am-i").getBoundingClientRect().right) }));
      check(`admin users ${w}px: no sideways scroll, email on screen`, m.vw === w && m.overflowX <= 0 && m.who <= w, JSON.stringify(m));
      await ctx.close();
    }
  } finally {
    state.admin = false;
  }
}

async function shots(browser) {
  state.models = true;
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
        await toggle.click();
      }
      const picker = page.locator(".composer-chips summary", { hasText: "Model" }).first();
      if ((await picker.count()) > 0) {
        await picker.click();
        await page.waitForTimeout(500);
        await page.screenshot({ path: path.join(outDir, `shot-${v.name}-${scheme}-models.png`) });
        await picker.click();
      }
      // The account menu, then Settings.
      const menu = page.getByRole("button", { name: "Menu" }).first();
      if ((await menu.count()) > 0) {
        await menu.click();
        await page.waitForTimeout(300);
        await page.screenshot({ path: path.join(outDir, `shot-${v.name}-${scheme}-menu.png`) });
        await page.getByRole("button", { name: "Settings" }).click();
        await page.waitForTimeout(400);
        await page.screenshot({ path: path.join(outDir, `shot-${v.name}-${scheme}-settings.png`) });
      }
      await ctx.close();
    }
  }
}

// ONLY=phone-shots: review screenshots (no checks) at 390px, light and dark: the composer while a reply runs (Steer and
// Queue), Settings → Usage, and the chat as iOS lays it out with the keyboard up (keyboard.ts, simulated by its markers).
async function phoneShots(browser) {
  state.hold = true;
  try {
    for (const scheme of ["light", "dark"]) {
      const { ctx, page } = await openPage(browser, { viewport: { width: 390, height: 844 }, colorScheme: scheme, isMobile: true, hasTouch: true });
      await page.goto(base + "/");
      await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
      await page.fill("#composer-input", "Plan a weekend");
      await page.keyboard.press("Enter");
      await page.waitForSelector(".composer [data-intent]");
      await page.fill("#composer-input", "Make it cheaper");
      await page.waitForTimeout(300);
      await page.screenshot({ path: path.join(outDir, `phone-${scheme}-running.png`) });
      await page.evaluate(() => { document.documentElement.dataset.keyboard = "open"; document.documentElement.style.setProperty("--keyboard-h", "420px"); });
      await page.waitForTimeout(300);
      await page.screenshot({ path: path.join(outDir, `phone-${scheme}-keyboard.png`) });
      await page.evaluate(() => { delete document.documentElement.dataset.keyboard; document.documentElement.style.removeProperty("--keyboard-h"); });
      await page.getByRole("button", { name: "Menu" }).first().click();
      await page.getByRole("button", { name: "Settings" }).click();
      await page.getByRole("button", { name: "Usage", exact: true }).click();
      await page.waitForSelector(".usage-table", { state: "attached" });
      await page.waitForTimeout(400);
      const overflow = await page.evaluate(() => { const b = document.querySelector(".settings-page"); return b.scrollWidth - b.clientWidth; });
      console.log(`phone ${scheme} settings usage: sideways overflow ${overflow}px`);
      await page.screenshot({ path: path.join(outDir, `phone-${scheme}-usage.png`), fullPage: false });
      await page.locator(".settings-page").evaluate((el) => (el.scrollTop = el.scrollHeight));
      await page.waitForTimeout(200);
      await page.screenshot({ path: path.join(outDir, `phone-${scheme}-usage-end.png`) });
      await ctx.close();
    }
  } finally {
    state.hold = false;
  }
}

// ONLY=share-image: the chat's "Share image" picture, drawn by the real app in a real browser, whole answer and summary. It
// prints the SHA-256 of each PNG (the SHA-256 of the decoded pixels; the date is pinned, so the same code on the same browser gives the same pixels): run it on
// the code before and after a change to the drawing code and compare. EXPECT_SHARE_IMAGE=hashFull,hashSummary makes the check
// fail when either differs. The summary picture must be exactly 1080 x 1920 (9:16).
async function shareImage(browser) {
  const { ctx, page, consoleErrors } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
  await page.addInitScript(() => {
    const Real = Date;
    const fixed = Date.UTC(2026, 10, 9, 12, 0, 0);
    globalThis.Date = class extends Real {
      constructor(...a) { super(...(a.length ? a : [fixed])); }
      static now() { return fixed; }
    };
  });
  DATA[PLAIN].at(-1).ref = "c".repeat(32); // a turn with a reference can be summarised
  await page.goto(base + "/");
  await page.waitForSelector(`.vitem[data-vkey="${2 * TURNS - 1}"]`);
  const hashes = [];
  for (const mode of ["Full answer", "Summary"]) {
    await page.locator('button[data-control="image"]').last().click();
    await page.waitForSelector("dialog.share-image");
    if (mode === "Summary") await page.getByRole("button", { name: "Summary", exact: true }).click();
    await page.waitForSelector("dialog.share-image img", { timeout: 15000 });
    await page.waitForTimeout(300);
    // The blob: address cannot be fetched (the page's connect-src is 'self'), but it can be drawn: the hash is of the decoded pixels.
    const r = await page.evaluate(async () => {
      const img = document.querySelector("dialog.share-image img");
      await img.decode();
      const c = document.createElement("canvas");
      c.width = img.naturalWidth; c.height = img.naturalHeight;
      const x = c.getContext("2d");
      x.drawImage(img, 0, 0);
      const px = x.getImageData(0, 0, c.width, c.height).data;
      const h = await crypto.subtle.digest("SHA-256", px);
      return { w: c.width, h: c.height, sha: [...new Uint8Array(h)].map((b) => b.toString(16).padStart(2, "0")).join(""), png: c.toDataURL("image/png").split(",")[1] };
    });
    hashes.push(r.sha);
    fs.writeFileSync(path.join(outDir, `share-image-${mode === "Summary" ? "summary" : "full"}.png`), Buffer.from(r.png, "base64"));
    console.log(`  share image (${mode}): ${r.w} x ${r.h} sha256 ${r.sha}`);
    check(`share image, ${mode}: drawn at 1080 px wide${mode === "Summary" ? " and exactly 9:16 (1080 x 1920)" : ""}`, r.w === 1080 && (mode !== "Summary" || r.h === 1920), `${r.w} x ${r.h}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector("dialog.share-image", { state: "detached" });
  }
  const want = process.env.EXPECT_SHARE_IMAGE;
  if (want) check("share image: both pictures are pixel for pixel the expected ones", want === hashes.join(","), hashes.join(","));
  check("share image: no console error", consoleErrors.length === 0, consoleErrors.join(" | "));
  delete DATA[PLAIN].at(-1).ref;
  await ctx.close();
}

// ONLY=share-menu: Share on a published post (spec 002) at 390, 820 and 1280 px, light and dark. The menu opens from the button
// and stays inside the viewport (no sideways scroll), Escape closes it and puts focus back on the button, Copy link says it
// worked, the page raises no console error and makes no request to any other origin (nothing of a social service loads).
async function shareMenu(browser) {
  for (const [label, w, h, scheme] of [["phone", 390, 844, "light"], ["narrow", 320, 640, "light"], ["tablet", 820, 1180, "dark"], ["desktop", 1280, 800, "light"], ["desktop", 1280, 800, "dark"]]) {
    const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: w, height: h }, colorScheme: scheme, ...(label === "phone" || label === "narrow" ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}), permissions: ["clipboard-read", "clipboard-write"] });
    await page.goto(base + "/b/share-post");
    await page.waitForSelector('button[aria-label="Share this post"]');
    await page.waitForTimeout(1500); // the blocks have loaded
    const button = page.getByRole("button", { name: "Share this post" });
    await page.screenshot({ path: path.join(outDir, `share-button-${label}-${scheme}.png`) });
    await button.click({ timeout: 5000 });
    await page.waitForSelector('[role="menu"]');
    const m = await page.evaluate((devW) => {
      const menu = document.querySelector('[role="menu"]').getBoundingClientRect();
      return { left: menu.left, right: menu.right, vw: document.documentElement.clientWidth, widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth, items: [...document.querySelectorAll('[role="menuitem"]')].map((e) => e.textContent) };
    }, w);
    check(`share menu, ${label} ${scheme}: opens inside the viewport, the page fits the device, no sideways scroll`, m.left >= 0 && m.right <= m.vw && m.widened <= 0 && m.overflowX <= 0, JSON.stringify(m));
    check(`share menu, ${label} ${scheme}: Copy link and the services are in it`, m.items.includes("Copy link") && (m.items.includes("WhatsApp") || m.items.includes("Share with your apps")), m.items.join(","));
    await page.screenshot({ path: path.join(outDir, `share-menu-${label}-${scheme}.png`) });
    await page.getByRole("menuitem", { name: "Copy link" }).click();
    await page.locator('[role="status"]').filter({ hasText: "Link copied" }).waitFor({ timeout: 4000 }).then(() => check(`share menu, ${label} ${scheme}: Copy link says "Link copied"`, true), () => check(`share menu, ${label} ${scheme}: Copy link says "Link copied"`, false));
    const copied = await page.evaluate(() => navigator.clipboard.readText()).catch(() => "");
    check(`share menu, ${label} ${scheme}: the copied text is the canonical address`, copied === `${base}/b/share-post`, copied);
    await page.keyboard.press("Escape");
    const back = await page.evaluate(() => document.activeElement?.getAttribute("aria-label"));
    check(`share menu, ${label} ${scheme}: Escape closes it and focus returns to the Share button`, back === "Share this post" && (await page.locator('[role="menu"]').count()) === 0, String(back));
    check(`share menu, ${label} ${scheme}: no console error and no request to another origin`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
    await ctx.close();
  }
}

// ONLY=story-card: the story picture of a post (spec 002) in a real browser, for each awkward case of the quickstart: it must
// be exactly 1080 x 1920, not blank, and carry no ink in the outer 24 px of the card. The PNGs are saved for a look.
async function storyCard(browser) {
  const slugs = Object.keys(STORY_SAMPLES);
  for (const slug of slugs) {
    const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: 1280, height: 800 } });
    await page.goto(base + "/b/" + slug);
    await page.waitForSelector('button[aria-label="Share this post"]');
    await page.getByRole("button", { name: "Share this post" }).click();
    await page.getByRole("menuitem", { name: "Story image" }).click();
    await page.waitForSelector("dialog.share-image img", { timeout: 20000 });
    await page.waitForTimeout(300);
    const r = await page.evaluate(async () => {
      const img = document.querySelector("dialog.share-image img");
      await img.decode();
      const c = document.createElement("canvas");
      c.width = img.naturalWidth; c.height = img.naturalHeight;
      const x = c.getContext("2d");
      x.drawImage(img, 0, 0);
      const d = x.getImageData(0, 0, c.width, c.height).data;
      let dark = 0, edge = 0;
      for (let yy = 0; yy < c.height; yy++) for (let xx = 0; xx < c.width; xx++) {
        const i = (yy * c.width + xx) * 4;
        if (d[i] < 70 && d[i + 1] < 70 && d[i + 2] < 70) { dark++; if (xx < 24 || xx >= c.width - 24 || yy < 24 || yy >= c.height - 24) edge++; }
      }
      return { w: c.width, h: c.height, dark, edge, png: c.toDataURL("image/png").split(",")[1] };
    });
    fs.writeFileSync(path.join(outDir, `story-card-${slug}.png`), Buffer.from(r.png, "base64"));
    check(`story card, ${slug}: exactly 1080 x 1920, drawn (not blank) and no ink in the outer 24 px`, r.w === 1080 && r.h === 1920 && r.dark > 5000 && r.edge === 0, `${r.w} x ${r.h}, ${r.dark} dark px, ${r.edge} at the edge`);
    check(`story card, ${slug}: no console error and no request to another origin`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
    await ctx.close();
  }
}

// The author's Share preview inside Post settings (spec 002): the picture of the draft appears at 9:16 inside the sheet, which
// stays inside the viewport with no sideways scroll, with no console error and no request to another origin.
async function shareSettings(browser) {
  for (const [label, w, h, scheme] of [["phone", 390, 844, "light"], ["phone", 390, 844, "dark"], ["desktop", 1280, 800, "light"]]) {
    const { ctx, page, consoleErrors, external } = await openPage(browser, { viewport: { width: w, height: h }, colorScheme: scheme, ...(label === "phone" ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) });
    await page.goto(base + `/blog/${BLOG_SHARE_ID}`);
    await page.waitForSelector("[contenteditable=true]");
    await page.screenshot({ path: path.join(outDir, `share-preview-before-${label}-${scheme}.png`) });
    await page.getByRole("button", { name: "Post settings" }).click();
    await page.waitForSelector('[aria-label="Post settings"]');
    await page.waitForSelector(".blog-share-preview-row img", { timeout: 15000 });
    await page.waitForTimeout(300);
    const m = await page.evaluate(() => {
      const sheet = document.querySelector('[aria-label="Post settings"]').getBoundingClientRect();
      const img = document.querySelector(".blog-share-preview-row img").getBoundingClientRect();
      return { sheetRight: sheet.right, vw: document.documentElement.clientWidth, ratio: img.width / img.height, inside: img.left >= sheet.left - 1 && img.right <= sheet.right + 1, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth, link: !!document.querySelector('[aria-label="Post settings"] .blog-link-preview'), noSend: document.querySelectorAll('[aria-label="Post settings"] button').length };
    });
    await page.screenshot({ path: path.join(outDir, `share-preview-${label}-${scheme}.png`) });
    check(`share preview, ${label} ${scheme}: the link preview and a 9:16 picture, inside the sheet, no sideways scroll`, m.link && Math.abs(m.ratio - 9 / 16) < 0.02 && m.inside && m.sheetRight <= m.vw + 1 && m.overflowX <= 0, JSON.stringify(m));
    check(`share preview, ${label} ${scheme}: no console error and no request to another origin`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
    await ctx.close();
  }
}

await new Promise((r) => server.listen(0, "127.0.0.1", r));
base = `http://127.0.0.1:${server.address().port}`;
const freshModels = () => ({
  current: { chat: "", summary: "gpt-oss-20b", blog_assistant: "kimi-k2", image: "", video: "" },
  rows: [
    { id: "deepseek-v3.2", name: "deepseek-v3.2", on: true, purposes: ["chat", "summary", "blog_assistant"], defaultFor: [], usage: { calls: 812, tokens: 9400000 } },
    { id: "gpt-oss-20b", name: "gpt-oss-20b", on: true, purposes: ["chat", "summary", "blog_assistant"], defaultFor: ["summary"], usage: null },
    { id: "glm-4.6", name: "glm-4.6", on: false, purposes: ["chat", "summary", "blog_assistant"], defaultFor: [], usage: { calls: 96, tokens: 1100000 } },
    { id: "agnes-image-2-5-flash:free", name: "agnes-image-2-5-flash:free", on: true, purposes: ["image"], defaultFor: ["image"], usage: null },
    { id: "veo-3.1-lite", name: "veo-3.1-lite", on: true, purposes: ["video"], defaultFor: ["video"], usage: null },
  ],
});
function modelsView() {
  const m = state.models;
  const st = (p) => ({ current: m.current[p], effective: m.current[p] === "kimi-k2" ? "" : m.current[p], warning: m.current[p] === "kimi-k2" ? "not_listed" : "" });
  return { catalogue: "ok", models: m.rows, purposes: { chat: st("chat"), summary: st("summary"), blog_assistant: st("blog_assistant"), image: st("image"), video: st("video") } };
}
// ONLY=admin-a11y: the new admin and author screens for keyboard and screen-reader users (spec 004, T044), on a phone and a desktop: every control has
// a name, every touch target on a phone is at least 44 px, a switch works from the keyboard and keeps focus when the screen reloads, focus lands somewhere
// sensible after a take-down, the banner is announced, and reduced motion removes the entrance animation.
async function adminA11y(browser) {
  const names = (page, sel) => page.evaluate((s) => [...document.querySelectorAll(s)].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && !e.closest("[hidden]"); }).filter((e) => {
    const label = e.getAttribute("aria-label") || e.getAttribute("aria-labelledby") || (e.labels && e.labels.length) || e.textContent.trim() || e.getAttribute("title");
    return !label;
  }).map((e) => e.outerHTML.slice(0, 80)), sel);
  const small = (page, sel, min) => page.evaluate(([s, m]) => [...document.querySelectorAll(s)].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && !e.closest(".blog-doc, .admin-post-content, .sr") && (r.height < m || r.width < m); }).map((e) => `${(e.textContent || e.getAttribute("aria-label") || e.tagName).trim().slice(0, 24)} ${Math.round(e.getBoundingClientRect().width)}x${Math.round(e.getBoundingClientRect().height)}`), [sel, min]);
  const controls = "button, a[href], input:not([type=hidden]), select, textarea";
  for (const [label, w, h, mobile] of [["phone", 390, 844, true], ["desktop", 1280, 800, false]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: "light", ...(mobile ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    state.admin = true; state.posts = freshPosts(); state.models = freshModels();
    try {
      const { ctx, page } = await openPage(browser, ctxOpts);
      await page.goto(base + "/admin/posts");
      await page.waitForSelector(".admin-table tbody tr", { timeout: 8000 });
      let bad = await names(page, controls);
      check(`a11y posts list, ${label}: every control has a name`, bad.length === 0, JSON.stringify(bad));
      if (mobile) { bad = await small(page, ".admin-main " + controls.split(", ").join(", .admin-main "), 44); check(`a11y posts list, ${label}: every touch target is at least 44 px`, bad.length === 0, JSON.stringify(bad)); }
      await page.goto(base + `/admin/posts/${pid(1)}`);
      await page.waitForSelector(".admin-post-content", { timeout: 8000 });
      await page.getByRole("button", { name: "Take down" }).click();
      const focused = await page.evaluate(() => document.activeElement?.getAttribute("aria-label") || document.activeElement?.tagName);
      check(`a11y take-down form, ${label}: focus moves into the form that opened`, focused === "Confirm: Take down", String(focused));
      bad = await names(page, controls);
      check(`a11y take-down form, ${label}: every control has a name`, bad.length === 0, JSON.stringify(bad));
      if (mobile) { bad = await small(page, ".confirm button, .confirm textarea, .admin-post .actions button", 44); check(`a11y take-down form, ${label}: touch targets are at least 44 px`, bad.length === 0, JSON.stringify(bad)); }
      await page.getByLabel("Reason (required, shown to the author)").fill("Spam");
      await page.keyboard.press("Tab");
      await page.getByRole("button", { name: "Confirm take down" }).press("Enter");
      await page.waitForSelector(".notice-card.taken", { timeout: 8000 });
      const after = await page.evaluate(() => document.activeElement?.tagName);
      check(`a11y take-down, ${label}: after it, focus is on the post's heading, not lost to the page`, after === "H1", String(after));
      await page.goto(base + "/admin/models");
      await page.waitForSelector(".purpose-row", { timeout: 8000 });
      bad = await names(page, controls);
      check(`a11y models, ${label}: every control has a name (switches, selects)`, bad.length === 0, JSON.stringify(bad));
      if (mobile) { bad = await small(page, ".purpose-row select, .admin-main button, .switch-cell", 44); check(`a11y models, ${label}: touch targets are at least 44 px (the switch included)`, bad.length === 0, JSON.stringify(bad)); }
      const sw = page.getByLabel("glm-4.6 for everyone");
      await sw.focus();
      await page.keyboard.press("Space");
      await page.waitForTimeout(500);
      const kept = await page.evaluate(() => document.activeElement?.getAttribute("aria-label"));
      check(`a11y models, ${label}: a switch works from the keyboard and keeps focus when the screen reloads`, (await sw.isChecked()) && kept === "glm-4.6 for everyone", String(kept));
      await ctx.close();
    } finally { state.admin = false; state.posts = null; state.models = null; }
    // The banner is announced, and reduced motion removes its entrance animation.
    state.takedown = true; state.notices = [{ postId: BLOG_ID, title: "Self-hosting an assistant", reason: "Advertising", at: "2026-11-09T00:00:00Z" }];
    try {
      const { ctx, page } = await openPage(browser, { ...ctxOpts, reducedMotion: "reduce" });
      await page.goto(base + "/");
      await page.waitForSelector(".takedown-notice", { timeout: 8000 });
      const m = await page.evaluate(() => ({ status: !!document.querySelector(".takedown-notice p[role=status]"), anim: getComputedStyle(document.querySelector(".takedown-notice")).animationName, label: document.querySelector(".takedown-notice").getAttribute("aria-label") }));
      check(`a11y banner, ${label}: announced as a status with a landmark name, and no animation under reduced motion`, m.status && m.anim === "none" && !!m.label, JSON.stringify(m));
      if (mobile) { const bad = await small(page, ".takedown-notice button", 44); check(`a11y banner, ${label}: touch targets are at least 44 px`, bad.length === 0, JSON.stringify(bad)); }
      await ctx.close();
    } finally { state.takedown = false; state.notices = []; }
  }
}

// ONLY=admin-models: the administrator's Models screen (spec 004) at 320, 390, 820 and 1280 px, light and dark: a switch per model, a default per
// purpose with the warning for a default that is no longer offered, a refused switch-off that names the purpose, a save. Nothing draws over
// anything else and the page fits the device.
async function adminModels(browser) {
  const fits = (page, w) => page.evaluate((devW) => ({ widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth }), w);
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    state.admin = true; state.models = freshModels();
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/admin/models");
      await page.waitForSelector(".purpose-row", { timeout: 8000 }).catch(() => {});
      const m = await page.evaluate(() => ({ purposes: document.querySelectorAll(".purpose-row").length, switches: document.querySelectorAll("input.switch").length, warn: document.querySelectorAll(".purpose-note.warn").length, nav: [...document.querySelectorAll(".admin-nav a")].filter((a) => { const r = document.createRange(); r.selectNodeContents(a); return new Set([...r.getClientRects()].map((b) => Math.round(b.top))).size > 1; }).length }));
      let f = await fits(page, w);
      check(`admin models, ${label} ${scheme}: five purposes, a switch per model, the warning for the dropped default, nav intact, the page fits`, m.purposes === 5 && m.switches === 5 && m.warn === 1 && m.nav === 0 && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ ...m, ...f }));
      check(`admin models, ${label} ${scheme}: no control draws over another`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `admin-models-${label}-${scheme}.png`), fullPage: true });
      await page.getByLabel("gpt-oss-20b for everyone").click();
      await page.waitForSelector(".notice-card.taken", { timeout: 5000 }).catch(() => {});
      const refused = await page.locator(".notice-card.taken").textContent().catch(() => "");
      check(`admin models, ${label} ${scheme}: switching off a default is refused and names the purpose`, /was not switched off/.test(refused) && /Summaries and titles/.test(refused), refused);
      await page.screenshot({ path: path.join(outDir, `admin-models-refused-${label}-${scheme}.png`), fullPage: true });
      await page.getByLabel("glm-4.6 for everyone").click();
      await page.waitForTimeout(400);
      check(`admin models, ${label} ${scheme}: a model that is not a default switches on`, await page.getByLabel("glm-4.6 for everyone").isChecked());
      await page.getByLabel("Chat", { exact: true }).selectOption("deepseek-v3.2");
      await page.getByRole("button", { name: "Save defaults" }).click();
      await page.waitForTimeout(500);
      check(`admin models, ${label} ${scheme}: Save defaults saves and says so`, (await page.getByText("Saved.").count()) === 1 || (await page.getByText("A chat with a model of its own keeps it").count()) === 1);
      f = await fits(page, w);
      check(`admin models, ${label} ${scheme}: still fits, no console error or off-origin request`, f.widened <= 0 && f.overflowX <= 0 && consoleErrors.filter((e) => !/status of 409/.test(e)).length === 0 && external.length === 0, JSON.stringify(f) + consoleErrors.concat(external).join(" | ")); // the one 409 is the refused switch-off, on purpose
      await ctx.close();
    } finally { state.admin = false; state.models = null; }
  }
}

// ONLY=writing-rules: the administrator's Writing rules card on Capabilities (spec 005) at 320, 390, 820 and 1280 px, light and dark: the state, the
// version, the credit and the counts; switched off; the warning when runs were not covered; a refused save says nothing was changed. Nothing draws
// over anything else, the switch is a named 44 px target that keeps focus while it saves, and the page fits the device.
async function writingRules(browser) {
  const fits = (page, w) => page.evaluate((devW) => ({ widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth }), w);
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    state.admin = true; state.writing = { enabled: true, lost: 3, fail: false };
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/admin/capabilities");
      await page.waitForSelector(".writing-card", { timeout: 8000 }).catch(() => {});
      const card = () => page.evaluate(() => { const c = document.querySelector(".writing-card"); const sw = c?.querySelector("input.switch"); const r = sw?.closest("label")?.getBoundingClientRect();
        return { on: sw?.checked, text: c?.textContent ?? "", chip: !!c?.querySelector(".chip"), alerts: c ? [...c.querySelectorAll("[role=alert]")].map((a) => a.textContent) : [], targetH: r ? Math.round(r.height) : 0, name: sw?.closest("label")?.textContent.trim() }; });
      let c = await card();
      let f = await fits(page, w);
      check(`writing rules, ${label} ${scheme}: on, with version, credit, counts and the not-applied warning; the page fits`, c.on === true && c.chip && /writing-rules v1/.test(c.text) && /Miqdad Badjuber \(antislop\)/.test(c.text) && /148/.test(c.text) && /chat and post-check runs/.test(c.text) && c.alerts.some((a) => /not applied to 3 runs/.test(a)) && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ ...c, text: c.text.slice(0, 60) }) + JSON.stringify(f));
      check(`writing rules, ${label} ${scheme}: the switch is named and its touch target is at least 44 px`, c.targetH >= 44 && /Writing rules are on for everyone/.test(c.name), JSON.stringify(c));
      check(`writing rules, ${label} ${scheme}: no control draws over another`, (await page.evaluate(() => { const els = [...document.querySelectorAll(".writing-card button, .writing-card a, .writing-card input, .admin-nav a, .admin-nav button")].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0; }); let n = 0; for (let i = 0; i < els.length; i++) for (let j = i + 1; j < els.length; j++) { if (els[i].contains(els[j]) || els[j].contains(els[i])) continue; const a = els[i].getBoundingClientRect(), b = els[j].getBoundingClientRect(); if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) n++; } return n; })) === 0);
      await page.screenshot({ path: path.join(outDir, `writing-rules-${label}-${scheme}.png`), fullPage: true });
      const sw = page.getByRole("switch", { name: /Writing rules are on for everyone/ });
      await sw.focus();
      await page.keyboard.press("Space");
      await page.waitForTimeout(500);
      c = await card();
      const kept = await page.evaluate(() => document.activeElement?.classList.contains("switch"));
      check(`writing rules, ${label} ${scheme}: the switch works from the keyboard, keeps focus, and off shows the without count`, c.on === false && kept === true && /without \(switched off\)/.test(c.text) && /85/.test(c.text) && c.alerts.length === 0, JSON.stringify({ ...c, kept, text: c.text.slice(0, 40) }));
      await page.screenshot({ path: path.join(outDir, `writing-rules-off-${label}-${scheme}.png`), fullPage: true });
      state.writing.fail = true;
      await page.getByRole("switch", { name: /Writing rules are off for everyone/ }).click();
      await page.waitForTimeout(500);
      c = await card();
      check(`writing rules, ${label} ${scheme}: a refused save says nothing was changed and the switch stays off`, c.on === false && c.alerts.some((a) => /Could not save\. Nothing was changed\./.test(a)), JSON.stringify(c));
      f = await fits(page, w);
      check(`writing rules, ${label} ${scheme}: still fits, no unexpected console error or off-origin request`, f.widened <= 0 && f.overflowX <= 0 && consoleErrors.filter((e) => !/status of 500/.test(e)).length === 0 && external.length === 0, JSON.stringify(f) + consoleErrors.join("|"));
      await ctx.close();
    } finally { state.admin = false; state.writing = null; }
  }
}

// ONLY=editor-toolbar: the formatting buttons show the truth (spec 006, US1). In a real browser: write a post with bold, italic, a link, headings,
// lists, a quote and a code block; then make 50 cursor moves and selections (arrow keys, clicks, shift-selection, a partly bold range) and, after each,
// every button's aria-pressed must equal what the DOM says is at the caret or in the selection (on, off, or mixed for part of a range). Nothing
// overlaps, and every button is at least 44 px tall.
async function editorToolbar(browser) {
  const LABELS = { Bold: "bold", Italic: "italic", Heading: "heading2", Subheading: "heading3", Bullets: "bullets", Numbers: "numbers", Quote: "quote", Code: "code", Link: "link" };
  const read = (page) => page.evaluate((labels) => {
    const root = document.querySelector(".ProseMirror");
    const sel = getSelection();
    if (!root || !sel || sel.rangeCount === 0) return null;
    const range = sel.getRangeAt(0);
    const css = { bold: "strong, b", italic: "em, i", heading2: "h2", heading3: "h3", bullets: "ul li", numbers: "ol li", quote: "blockquote", code: "pre", link: "a" };
    const targets = [];
    if (range.collapsed) {
      let n = range.startContainer;
      if (n.nodeType !== 3 && n.childNodes[range.startOffset]) n = n.childNodes[range.startOffset];
      if (n.nodeType !== 3 && range.startOffset > 0 && n.childNodes[range.startOffset - 1]) n = n.childNodes[range.startOffset - 1];
      targets.push(n);
    } else {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!range.intersectsNode(n)) continue;
        const from = n === range.startContainer ? range.startOffset : 0;
        const to = n === range.endContainer ? range.endOffset : n.length;
        if (to > from) targets.push(n);
      }
    }
    const el = (n) => (n.nodeType === 3 ? n.parentElement : n);
    const truth = {};
    // A caret on the edge of a mark, or of two blocks, is a matter of which side typing would continue; the editor knows, the DOM cannot say. So at a caret the
    // truth is the editor's own answer at that moment (the thing the toolbar used to read only while rendering), and over a range it is the text in the range.
    const ed = root.editor;
    if (range.collapsed && ed) {
      const q = { bold: ["bold"], italic: ["italic"], link: ["link"], heading2: ["heading", { level: 2 }], heading3: ["heading", { level: 3 }], bullets: ["bulletList"], numbers: ["orderedList"], quote: ["blockquote"], code: ["codeBlock"] };
      for (const [k, a] of Object.entries(q)) truth[k] = ed.isActive(...a) ? "on" : "off";
    } else for (const [label, key] of Object.entries(labels)) {
      const have = targets.filter((n) => { const e = el(n); return e && root.contains(e) && !!e.closest(css[key]); }).length;
      truth[key] = targets.length === 0 || have === 0 ? "off" : have === targets.length ? "on" : "mixed";
    }
    const shown = {};
    for (const [label, key] of Object.entries(labels)) {
      const b = [...document.querySelectorAll(".blog-toolbar button")].find((x) => x.getAttribute("aria-label") === label);
      const v = b?.getAttribute("aria-pressed");
      shown[key] = v === "true" ? "on" : v === "mixed" ? "mixed" : v === "false" ? "off" : "missing";
    }
    return { truth, shown, collapsed: range.collapsed, text: range.toString().slice(0, 20) };
  }, LABELS);
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
    page.on("dialog", (d) => d.accept("https://example.org/page"));
    await page.goto(base + `/blog/${BLOG_ID}`);
    await page.waitForSelector("[contenteditable=true]", { timeout: 8000 });
    await page.focus("[contenteditable=true]");
    const kb = page.keyboard;
    await kb.press("Control+a"); await kb.press("Backspace");
    const type = async (t) => { await kb.type(t, { delay: 2 }); };
    await type("## Heading two\n");
    await type("plain "); await kb.press("Control+b"); await type("bold"); await kb.press("Control+b"); await type(" and "); await kb.press("Control+i"); await type("italic"); await kb.press("Control+i"); await type(" tail\n");
    await type("see the link here");
    for (let i = 0; i < 5; i++) await kb.press("ArrowLeft");
    for (let i = 0; i < 4; i++) await kb.press("Shift+ArrowLeft");
    await page.getByRole("button", { name: "Link", exact: true }).click();
    await kb.press("End"); await type("\n");
    await type("- first item\nsecond item\n\n");
    await type("1. step one\nstep two\n\n");
    await type("> quoted words\n\n");
    await type("### Sub three\n");
    await type("``` code line");
    await page.waitForTimeout(300);
    const bad = [];
    let moves = 0;
    const record = async (what) => {
      await page.waitForTimeout(what.startsWith("click") ? 150 : 60);
      const r = await read(page);
      moves++;
      if (!r) { bad.push(`${what}: no selection`); return; }
      for (const key of Object.keys(r.truth)) if (r.truth[key] !== r.shown[key]) bad.push(`${what} [${r.collapsed ? "caret" : "range \"" + r.text + "\""}]: ${key} should be ${r.truth[key]}, button shows ${r.shown[key]}`);
    };
    await kb.press("Control+Home");
    const acts = ["ArrowRight", "ArrowRight", "ArrowRight", "ArrowDown", "End", "Shift+ArrowLeft", "Shift+ArrowLeft", "ArrowLeft", "Home", "Shift+ArrowRight", "Shift+ArrowRight", "Shift+ArrowRight", "Shift+ArrowRight", "ArrowDown", "ArrowDown", "Shift+End", "ArrowRight", "ArrowUp", "ArrowUp", "Shift+ArrowDown", "Shift+ArrowDown", "ArrowLeft", "End", "ArrowDown", "Home", "Shift+ArrowRight"];
    for (let i = 0; moves < 40; i++) { const a = acts[i % acts.length]; await kb.press(a); await record(a); }
    const box = await page.locator(".ProseMirror").boundingBox();
    const lines = await page.locator(".ProseMirror > *").evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return [r.left + Math.min(40, r.width / 2), r.top + Math.min(r.height / 2, 14)]; }));
    for (const [x, y] of [...lines, ...lines].slice(0, 14)) { await page.mouse.click(x, y); await record(`click at ${Math.round(x)},${Math.round(y)}`); }
    await page.mouse.click(lines[1][0] + 2, lines[1][1]); await kb.press("Shift+End"); await record("range over the line with bold and italic");
    await kb.press("Control+Home"); await kb.press("Shift+ArrowDown"); await kb.press("Shift+ArrowDown"); await record("range over a heading and a paragraph");
    check(`editor toolbar, ${label} ${scheme}: after ${moves} cursor moves and selections every button matches the text at the caret`, bad.length === 0 && moves >= 50, `${moves} moves; ${bad.slice(0, 4).join(" | ")}`);
    const lay = await page.evaluate(() => {
      const bs = [...document.querySelectorAll(".blog-toolbar button")].filter((b) => { const r = b.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
      let overlap = 0;
      for (let i = 0; i < bs.length; i++) for (let j = i + 1; j < bs.length; j++) { const a = bs[i].getBoundingClientRect(), b = bs[j].getBoundingClientRect(); if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) overlap++; }
      return { overlap, short: bs.filter((b) => b.getBoundingClientRect().height < 43.5).length, count: bs.length, widened: innerWidth, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth };
    });
    check(`editor toolbar, ${label} ${scheme}: no button overlaps another, every target is at least 44 px, the page fits`, lay.overlap === 0 && lay.short === 0 && lay.count >= 11 && lay.widened === w && lay.overflowX <= 0, JSON.stringify(lay));
    // a partly bold range shows the mixed state with its outline and bar (not colour alone)
    await page.evaluate(() => {
      const root = document.querySelector(".ProseMirror");
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      let first = null, strong = null;
      for (let n = walker.nextNode(); n; n = walker.nextNode()) { if (n.textContent.startsWith("plain ")) first = n; if (n.parentElement.closest("strong") && !strong) strong = n; }
      getSelection().setBaseAndExtent(first, 2, strong, 2);
    });
    await page.waitForTimeout(150);
    const mixed = await page.evaluate(() => { const b = [...document.querySelectorAll(".blog-toolbar button")].find((x) => x.getAttribute("aria-label") === "Bold"); const cs = getComputedStyle(b); return { v: b.getAttribute("aria-pressed"), border: cs.borderTopWidth, bar: getComputedStyle(b, "::after").content !== "none" }; });
    check(`editor toolbar, ${label} ${scheme}: a partly bold selection shows Bold as mixed, with an outline and a bar`, mixed.v === "mixed" && parseFloat(mixed.border) >= 2 && mixed.bar, JSON.stringify(mixed));
    await page.screenshot({ path: path.join(outDir, `editor-toolbar-${label}-${scheme}.png`), fullPage: true });
    check(`editor toolbar, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.join("|").slice(0, 200));
    await ctx.close();
  }
}

// ONLY=ai-editor: "Check with the editor" (spec 006, US3) in a real browser at 320, 390, 820 and 1280 px, light and dark: the button and the panel, the
// progress, the suggestions beside (or below) the draft; accepting changes only that passage and can be undone, rejecting changes nothing, a passage that
// no longer fits says so; the draft is unchanged until an accept; nothing overlaps; every target is at least 44 px on a phone.
async function aiEditor(browser) {
  const body = (page) => page.evaluate(() => document.querySelector(".ProseMirror").innerText.replace(/\s+/g, " ").trim());
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    state.blogCheck = true;
    state.ai = { phase: "idle", decisions: [], edits: [
      { id: eid(1), kind: "replace", part: 1, anchor: "Running your own assistant sounds cheap until the bill arrives.", original: "sounds cheap until the bill arrives", proposed: "looks cheap until the first bill", reason: "Shorter.", decision: "open" },
      { id: eid(2), kind: "insert", part: 0, anchor: "Hardware", original: "", proposed: "A small server is enough for one person.\n\nIt still needs power and cooling.", reason: "Builds on your points.", decision: "open" },
      { id: eid(3), kind: "replace", part: 2, anchor: "A closing paragraph about the hosting costs.", original: "words that are not there", proposed: "x", reason: "Never applies.", decision: "open" },
    ] };
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + `/blog/${BLOG_ID}`);
      await page.waitForSelector("[contenteditable=true]", { timeout: 8000 });
      await page.focus("[contenteditable=true]");
      await page.keyboard.press("Control+a"); await page.keyboard.press("Backspace");
      await page.keyboard.type("## Hardware\nRunning your own assistant sounds cheap until the bill arrives.\nA closing paragraph about the hosting costs.", { delay: 2 });
      const original = await body(page);
      const open = page.getByRole("button", { name: "Check with the editor" });
      check(`ai editor, ${label} ${scheme}: the editor offers "Check with the editor" when checking is available`, (await open.count()) === 1);
      await open.click();
      await page.waitForSelector(".blog-panel", { timeout: 5000 });
      await page.getByRole("button", { name: "Check my post" }).click();
      await page.locator(".step-current", { hasText: "Looking things up" }).waitFor({ timeout: 5000 }); // (the first moment, before the server answers, shows reading)
      const steps = await page.evaluate(() => ({ cur: document.querySelector(".step-current")?.textContent, live: document.querySelector(".blog-panel [aria-live=polite]")?.textContent }));
      check(`ai editor, ${label} ${scheme}: the panel shows the step the check is in`, /Looking things up/.test(steps.cur ?? "") && steps.live === "Looking things up", JSON.stringify(steps));
      check(`ai editor, ${label} ${scheme}: nothing in the draft changed while the check ran`, (await body(page)) === original);
      await page.screenshot({ path: path.join(outDir, `ai-editor-progress-${label}-${scheme}.png`), fullPage: false });
      state.ai.phase = "ready";
      await page.waitForSelector(".suggestion", { timeout: 6000 });
      const lay = await page.evaluate(() => {
        const r = (sel) => document.querySelector(sel)?.getBoundingClientRect();
        const panel = r(".blog-panel"), bar = r(".blog-bar"), bar2 = r(".blog-toolbar"), pub = r("[data-publish]");
        const hit = (a, b) => !!a && !!b && a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1;
        const els = [...document.querySelectorAll(".blog-panel button, .blog-bar button, .blog-toolbar button")].filter((e) => { const q = e.getBoundingClientRect(); return q.width > 0 && q.height > 0; });
        let overlap = 0;
        for (let i = 0; i < els.length; i++) for (let j = i + 1; j < els.length; j++) if (hit(els[i].getBoundingClientRect(), els[j].getBoundingClientRect())) overlap++;
        const panelBtns = [...document.querySelectorAll(".blog-panel button")].filter((e) => e.getBoundingClientRect().width > 0);
        return { panelOverBar: hit(panel, bar), panelOverToolbar: hit(panel, bar2), panelOverPublish: hit(panel, pub), overlap, short: panelBtns.filter((b) => b.getBoundingClientRect().height < 43.5).length, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth, widened: innerWidth, cards: document.querySelectorAll(".suggestion").length, gone: document.querySelectorAll(".suggestion-gone").length, head: document.querySelector("#blog-panel-h")?.textContent };
      });
      check(`ai editor, ${label} ${scheme}: three suggestions, the one about missing words says it no longer applies; the panel covers neither the bar, the toolbar nor Publish`, lay.cards === 3 && lay.gone === 1 && !lay.panelOverBar && !lay.panelOverToolbar && !lay.panelOverPublish && lay.head === "Suggestions (3)", JSON.stringify(lay));
      check(`ai editor, ${label} ${scheme}: no button overlaps another, targets are at least 44 px, the page fits`, lay.overlap === 0 && lay.short === 0 && lay.overflowX <= 0 && lay.widened === w, JSON.stringify(lay));
      await page.screenshot({ path: path.join(outDir, `ai-editor-suggestions-${label}-${scheme}.png`), fullPage: false });
      check(`ai editor, ${label} ${scheme}: the draft is still exactly as written before any accept`, (await body(page)) === original);
      await page.getByRole("button", { name: "Accept suggestion 1" }).click();
      await page.waitForSelector(".suggestion-accepted", { timeout: 5000 });
      const afterAccept = await body(page);
      check(`ai editor, ${label} ${scheme}: accepting changed only that passage`, afterAccept === original.replace("sounds cheap until the bill arrives", "looks cheap until the first bill"), afterAccept);
      await page.getByRole("button", { name: "Undo suggestion 1" }).click();
      await page.waitForSelector(".suggestion-undone", { timeout: 5000 });
      check(`ai editor, ${label} ${scheme}: Undo brought the passage back`, (await body(page)) === original);
      await page.getByRole("button", { name: "Reject suggestion 2" }).click();
      await page.waitForSelector(".suggestion-rejected", { timeout: 5000 });
      check(`ai editor, ${label} ${scheme}: rejecting changed nothing, and the server heard both choices`, (await body(page)) === original && state.ai.decisions.includes("22:rejected"), JSON.stringify(state.ai.decisions));
      const insertAt = page.getByRole("button", { name: /Accept suggestion/ });
      check(`ai editor, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.join("|").slice(0, 200));
      void insertAt;
      await ctx.close();
    } finally { state.ai = null; state.blogCheck = false; }
  }
}

// ONLY=admin-posts: the administrator's Posts screens and the author's side of a takedown (spec 004) in a real browser at 320, 390, 820 and
// 1280 px, light and dark. The list, a post, the take-down form and the restore; a takedown seen by the author as a list row, a one-time
// banner that goes away for good, and an editor whose Publish is blocked. Every page fits the device and raises no console error.
const pid = (n) => String(n).padStart(32, "0");
const freshPosts = () => [
  { id: pid(1), slug: "home-lab", title: "How I set up a home lab on a mini PC", summary: "Notes from a weekend of cables, one stubborn BIOS and a very quiet fan.", authorName: "Sari Wulandari", publishedAt: "2026-11-08T01:00:00Z", state: "visible", takedown: null },
  { id: pid(2), slug: "long", title: "A very long post title that keeps going so we can see how it wraps in the table on a small screen", summary: "Hidden from visitors because the author's account is suspended.", authorName: "Someone With A Very Long Display Name Indeed", publishedAt: "2026-10-30T01:00:00Z", state: "hidden_account", takedown: null },
  { id: pid(3), slug: "flights", title: "Cheap flights are a lie", summary: "", authorName: "Bima Prakoso", publishedAt: "2026-11-03T01:00:00Z", state: "taken_down", takedown: { at: "2026-11-09T00:00:00Z", reason: "Advertising a booking site", byName: "Admin" } },
];
// No two buttons (or a button and a link) may draw over each other: within each group of controls, no pair of boxes may intersect.
const overlaps = (page) => page.evaluate(() => {
  const out = [];
  for (const g of document.querySelectorAll(".confirm, .actions, .filters, .search, .takedown-notice, .blog-row-end, .blog-bar, .admin-nav")) {
    const els = [...g.querySelectorAll("button, a, input, textarea")].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
    for (let i = 0; i < els.length; i++) for (let j = i + 1; j < els.length; j++) {
      if (els[i].contains(els[j]) || els[j].contains(els[i])) continue;
      const a = els[i].getBoundingClientRect(), b = els[j].getBoundingClientRect();
      if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) out.push(`${(els[i].textContent || els[i].tagName).trim().slice(0, 20)} x ${(els[j].textContent || els[j].tagName).trim().slice(0, 20)}`);
    }
  }
  return out;
});
async function adminPosts(browser) {
  const fits = (page, w) => page.evaluate((devW) => ({ widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth }), w);
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    state.admin = true; state.posts = freshPosts();
    try {
      let { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/admin/posts");
      await page.waitForSelector(".admin-table tbody tr", { timeout: 8000 }).catch(() => {});
      const rows = await page.locator(".admin-table tbody tr").count();
      const badges = await page.locator(".admin-table .badge").allTextContents();
      const nav = await page.evaluate(() => [...document.querySelectorAll(".admin-nav a")].filter((a) => { const r = document.createRange(); r.selectNodeContents(a); return new Set([...r.getClientRects()].map((b) => Math.round(b.top))).size > 1; }).map((a) => a.textContent));
      let f = await fits(page, w);
      check(`admin posts list, ${label} ${scheme}: three posts with their states, the nav does not break, the page fits`, rows === 3 && badges.join("|") === "Visible|Hidden · account|Taken down" && nav.length === 0 && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ rows, badges, nav, ...f }));
      check(`admin posts list, ${label} ${scheme}: no button draws over another`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `admin-posts-${label}-${scheme}.png`), fullPage: true });
      await page.getByRole("button", { name: "Taken down" }).click();
      await page.waitForTimeout(300);
      check(`admin posts list, ${label} ${scheme}: the Taken down filter narrows to one`, (await page.locator(".admin-table tbody tr").count()) === 1);
      await page.goto(base + `/admin/posts/${pid(1)}`);
      await page.waitForSelector(".admin-post-content", { timeout: 8000 }).catch(() => {});
      await page.getByRole("button", { name: "Take down" }).click();
      await page.getByLabel("Reason (required, shown to the author)").fill("Spam");
      f = await fits(page, w);
      const formShown = await page.getByRole("form", { name: "Confirm: Take down" }).isVisible();
      check(`admin post, ${label} ${scheme}: the take-down form opens under the button, with its reason box, and the page fits`, formShown && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ formShown, ...f }));
      check(`admin post, ${label} ${scheme}: no button draws over another (Take down, Confirm, Cancel)`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `admin-post-takedown-${label}-${scheme}.png`), fullPage: true });
      await page.getByRole("button", { name: "Confirm take down" }).click();
      await page.waitForSelector(".notice-card.taken", { timeout: 8000 }).catch(() => {});
      const restoreBtn = await page.getByRole("button", { name: "Restore" }).count();
      check(`admin post, ${label} ${scheme}: after the takedown the post shows who, why, and Restore`, restoreBtn === 1 && (await page.locator(".notice-card.taken").count()) === 1);
      await page.getByRole("button", { name: "Restore" }).click();
      await page.getByRole("button", { name: "Confirm restore" }).click();
      await page.waitForTimeout(400);
      check(`admin post, ${label} ${scheme}: a restore puts it back (Take down is offered again)`, (await page.getByRole("button", { name: "Take down" }).count()) === 1);
      check(`admin posts, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
      await ctx.close();
    } finally { state.admin = false; state.posts = null; }
    // The author's side: the banner once, the list row, the editor.
    state.takedown = true; state.notices = [{ postId: BLOG_ID, title: "Self-hosting an assistant", reason: "Advertising a booking site. See the community rules.", at: "2026-11-09T00:00:00Z" }];
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/");
      await page.waitForSelector(".takedown-notice", { timeout: 8000 }).catch(() => {});
      const shown = await page.locator(".takedown-notice").count();
      let f = await fits(page, w);
      check(`takedown banner, ${label} ${scheme}: shown once with the reason, and the page fits`, shown === 1 && (await page.locator(".takedown-notice").textContent()).includes("Advertising a booking site") && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ shown, ...f }));
      check(`takedown banner, ${label} ${scheme}: its buttons do not draw over each other or the text`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `takedown-banner-${label}-${scheme}.png`) });
      await page.getByRole("button", { name: "Got it" }).click();
      await page.waitForTimeout(300);
      await page.reload();
      await page.waitForTimeout(800);
      check(`takedown banner, ${label} ${scheme}: dismissed, it does not come back after a reload`, (await page.locator(".takedown-notice").count()) === 0);
      await page.goto(base + "/blog");
      await page.waitForSelector(".blog-rows", { timeout: 8000 }).catch(() => {});
      const row = await page.locator(".chip-taken-down").count();
      f = await fits(page, w);
      check(`my posts, ${label} ${scheme}: the taken-down post says Taken down with its reason, and the page fits`, row === 1 && (await page.locator(".blog-takedown-why").count()) === 1 && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ row, ...f }));
      check(`my posts, ${label} ${scheme}: the row's chip and buttons do not draw over each other`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `takedown-list-${label}-${scheme}.png`), fullPage: true });
      await page.goto(base + `/blog/${BLOG_ID}`);
      await page.waitForSelector(".blog-takedown", { timeout: 8000 }).catch(() => {});
      const blocked = await page.evaluate(() => ({ disabled: document.querySelector("[data-publish]")?.disabled, note: !!document.querySelector(".blog-takedown") }));
      f = await fits(page, w);
      check(`editor, ${label} ${scheme}: Publish is disabled and the takedown is explained, the page fits`, blocked.disabled === true && blocked.note && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ ...blocked, ...f }));
      check(`editor, ${label} ${scheme}: the top bar buttons do not draw over each other`, (await overlaps(page)).length === 0, JSON.stringify(await overlaps(page)));
      await page.screenshot({ path: path.join(outDir, `takedown-editor-${label}-${scheme}.png`) });
      check(`takedown author side, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
      await ctx.close();
    } finally { state.takedown = false; state.notices = []; }
  }
}

// ONLY=blog-discovery: the doors to the public blog (spec 003) in a real browser at 320, 390, 820 and 1280 px, light and dark. A visitor's
// sign-in page and a pending account's waiting screen show the three latest posts as cards, the Read the blog button and keep the sign-in
// action first; the chat rail and menu offer Read the blog (for a user with and without writing) without pushing the history down; the public
// list searches, pages and falls back. Every page fits the device, and the only console error allowed is the visitor's one 401 from /api/me.
async function blogDiscovery(browser) {
  const only401 = (errs, n401) => errs.filter((e) => !(n401-- > 0 && /status of 401/.test(e)));
  const fits = (page, w) => page.evaluate((devW) => ({ widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth }), w);
  for (const [label, w, h, scheme] of [["narrow", 320, 640, "light"], ["phone", 390, 844, "dark"], ["tablet", 820, 1180, "light"], ["desktop", 1280, 800, "dark"]]) {
    const ctxOpts = { viewport: { width: w, height: h }, colorScheme: scheme, ...(w < 500 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) };
    // 1. A visitor on the sign-in page.
    state.visitor = true;
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/");
      await page.waitForSelector(".latest-posts a", { timeout: 8000 }).catch(() => {});
      await page.waitForTimeout(500); // the cards have eased in
      const cards = await page.locator(".latest-posts a").count();
      const read = await page.locator('a.read-blog[href="/b"]').count();
      const order = await page.evaluate(() => { const b = [...document.querySelectorAll("a.btn")]; return b.map((a) => a.textContent.trim()); });
      const f = await fits(page, w);
      check(`blog discovery sign-in, ${label} ${scheme}: three latest-post cards, the Read the blog button, the sign-in action first, and the page fits the device`,
        cards === 3 && read === 1 && /Sign in/.test(order[0] || "") && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ cards, read, order, ...f }));
      await page.screenshot({ path: path.join(outDir, `discovery-signin-${label}-${scheme}.png`) });
      const href = await page.locator(".latest-posts a").first().getAttribute("href");
      check(`blog discovery sign-in, ${label} ${scheme}: a card opens its post`, href === "/b/disc-1" || /^\/b\//.test(href || ""), String(href));
      check(`blog discovery sign-in, ${label} ${scheme}: no console error beyond the visitor's 401, no off-origin request`, only401(consoleErrors, 1).length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
      await ctx.close();
    } finally { state.visitor = false; }
    // 2. A pending account on the waiting screen.
    state.meState = "PENDING";
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/");
      await page.waitForSelector(".latest-posts a", { timeout: 8000 }).catch(() => {});
      const cards = await page.locator(".latest-posts a").count();
      const read = await page.locator('a.read-blog[href="/b"]').count();
      const chatControls = await page.locator("textarea, .composer").count();
      const f = await fits(page, w);
      check(`blog discovery waiting screen, ${label} ${scheme}: the cards and Read the blog show, there is no chat, the page fits`, cards === 3 && read === 1 && chatControls === 0 && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ cards, read, chatControls, ...f }));
      await page.screenshot({ path: path.join(outDir, `discovery-waiting-${label}-${scheme}.png`) });
      check(`blog discovery waiting screen, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
      await ctx.close();
    } finally { state.meState = "ACTIVE"; }
    // 3. The chat: Read the blog in the rail (or the menu on a phone), for a writer and for a user without writing.
    for (const noWriting of [false, true]) {
      state.noWriting = noWriting;
      try {
        const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
        await page.goto(base + "/");
        await page.waitForSelector("nav.side", { timeout: 8000, state: "attached" });
        await page.waitForTimeout(600);
        if (w < 500) await page.getByRole("button", { name: "Menu" }).first().click();
        const where = w < 500 ? "#bar-actions" : "nav.side";
        const entry = page.locator(where).getByRole("button", { name: "Read the blog" });
        const n = await entry.count();
        const posts = await page.locator(where).getByRole("button", { name: "Blog posts" }).count();
        const f = await fits(page, w);
        check(`blog discovery chat, ${label} ${scheme}, ${noWriting ? "without" : "with"} writing: Read the blog is offered${noWriting ? ", Blog posts is not" : " beside Blog posts"}, the page fits`, n === 1 && posts === (noWriting ? 0 : 1) && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ n, posts, ...f }));
        if (w >= 500) {
          const top = await page.evaluate(() => { const r = document.querySelector("nav.side .search-panel, nav.side section")?.getBoundingClientRect(); return r ? Math.round(r.top) : -1; });
          check(`blog discovery chat, ${label} ${scheme}: the rail entry adds no row (search stays at ${top}px)`, top > 0 && top < 190, String(top));
        }
        await page.screenshot({ path: path.join(outDir, `discovery-chat-${label}-${scheme}-${noWriting ? "nowrite" : "write"}.png`) });
        check(`blog discovery chat, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
        await ctx.close();
      } finally { state.noWriting = false; }
    }
    // 4. The public list for a signed-in reader: search, Show more, Back to Antares.
    state.manyPosts = true;
    try {
      const { ctx, page, consoleErrors, external } = await openPage(browser, ctxOpts);
      await page.goto(base + "/b");
      await page.waitForSelector(".blog-cards .blog-card", { timeout: 8000 });
      await page.waitForTimeout(500);
      const first = await page.locator(".blog-card").count();
      const back = await page.getByRole("link", { name: "Back to Antares" }).count();
      await page.screenshot({ path: path.join(outDir, `discovery-list-first-${label}-${scheme}.png`), fullPage: true });
      await page.getByRole("button", { name: "Show more" }).click();
      await page.locator(".blog-card").nth(20).waitFor({ timeout: 5000 }).catch(() => {});
      const more = await page.locator(".blog-card").count();
      await page.getByRole("searchbox", { name: "Search posts" }).fill("post 7");
      await page.waitForTimeout(700);
      const hits = await page.locator(".blog-card").count();
      await page.getByRole("searchbox", { name: "Search posts" }).fill("nothing like this");
      await page.waitForTimeout(700);
      const none = await page.getByText("No posts match").count();
      await page.getByRole("button", { name: "Clear" }).click();
      await page.locator(".blog-card").first().waitFor({ timeout: 5000 }).catch(() => {});
      const top = await page.evaluate(() => { window.scrollTo(0, 0); return { display: getComputedStyle(document.body).display, headerTop: Math.round(document.querySelector(".blog-public-head")?.getBoundingClientRect().top ?? -1) }; });
      check(`blog discovery list, ${label} ${scheme}: the page hangs from the top of the screen (not centred in a grid that can leave a band above a short page)`, top.display !== "grid" && top.headerTop === 0, JSON.stringify(top));
      const f = await fits(page, w);
      check(`blog discovery list, ${label} ${scheme}: 20 posts, Show more adds the next page, a search narrows it, no match is said, Clear restores it, Back to Antares is there`,
        first === 20 && more === 40 && hits === 1 && none > 0 && back === 1 && f.widened <= 0 && f.overflowX <= 0, JSON.stringify({ first, more, hits, none, back, ...f }));
      await page.screenshot({ path: path.join(outDir, `discovery-list-${label}-${scheme}.png`), fullPage: true });
      await page.goto(base + "/blog");
      await page.waitForSelector(".blog-rows, .blog-empty", { timeout: 8000 }).catch(() => {});
      const mine = await page.evaluate(() => ({ display: getComputedStyle(document.body).display, rootTop: Math.round(document.getElementById("root").firstElementChild.getBoundingClientRect().top) }));
      check(`blog workspace, ${label} ${scheme}: the page hangs from the top of the screen, no band of dots above it`, mine.display !== "grid" && mine.rootTop === 0, JSON.stringify(mine));
      check(`blog discovery list, ${label} ${scheme}: no console error or off-origin request`, consoleErrors.length === 0 && external.length === 0, consoleErrors.concat(external).join(" | "));
      await ctx.close();
    } finally { state.manyPosts = false; }
  }
}

// Short and landscape screens: the sign-in and waiting pages hold a tall strip now, so on a short viewport the top must not be cut off (a
// centred grid cannot scroll upward), the page must scroll to its last button, and an unbroken long name or title must not push it sideways.
async function blogDiscoveryShort(browser) {
  for (const [label, w, h] of [["short phone", 390, 520], ["very short", 320, 420], ["landscape", 568, 320], ["tablet landscape", 1024, 600]]) {
    for (const meState of ["", "PENDING"]) {
      state.visitor = meState === ""; state.meState = meState || "ACTIVE";
      try {
        const { ctx, page, consoleErrors } = await openPage(browser, { viewport: { width: w, height: h }, ...(w < 700 ? { deviceScaleFactor: 2, isMobile: true, hasTouch: true } : {}) });
        await page.goto(base + "/");
        await page.waitForSelector(".latest-posts a", { timeout: 8000 }).catch(() => {});
        await page.waitForTimeout(500);
        const m = await page.evaluate((devW) => {
          const top = document.querySelector(".screen h1")?.getBoundingClientRect().top ?? -1;
          const btn = document.querySelector("a.read-blog");
          window.scrollTo(0, document.documentElement.scrollHeight);
          const b = btn ? btn.getBoundingClientRect() : { bottom: 99999 };
          const cards = [...document.querySelectorAll(".latest-posts a")].map((a) => { const r = a.getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth; });
          return { widened: innerWidth - devW, overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth, top, btnBottom: Math.round(b.bottom), vh: innerHeight, cardsInside: cards.every(Boolean), cards: cards.length };
        }, w);
        check(`blog discovery ${meState ? "waiting" : "sign-in"}, ${label} ${w}x${h}: nothing sideways, the top is not cut off, the last button can be scrolled to, every card is inside`,
          m.widened <= 0 && m.overflowX <= 0 && m.top >= 0 && m.btnBottom <= m.vh && m.cardsInside && m.cards === 3, JSON.stringify(m));
        await page.screenshot({ path: path.join(outDir, `discovery-short-${meState ? "waiting" : "signin"}-${w}x${h}.png`), fullPage: true });
        await ctx.close();
      } finally { state.visitor = false; state.meState = "ACTIVE"; }
    }
  }
}

const browser = await chromium.launch({ executablePath: process.env.CHROME || undefined });
console.log(`# ${await browser.version()} (Chromium), bundle ${fs.readdirSync(path.join(dist, "assets")).filter((f) => /^(index|Block)-.*\.js$/.test(f)).join(", ")}`);
const only = process.env.ONLY;
try {
  if (!only || only === "mobile-layout") await mobileLayout(browser);
  if (!only || only === "desktop-layout") await desktopLayout(browser);
  if (!only || only === "mobile-layout" || only === "desktop-layout") await modelPicker(browser);
  if (!only || only === "starting-card") await startingCard(browser);
  if (!only || only === "images") await images(browser);
  if (!only || only === "blog-public") { state.visitor = true; try { await blogPages(browser, "blog-public"); } finally { state.visitor = false; } }
  if (!only || only === "blog-editor") await blogPages(browser, "blog-editor");
  if (only === "blog-list") await blogPages(browser, "blog-editor", ["list"]); // the list alone (it is also part of blog-editor)
  if (!only || only === "mobile-layout" || only === "mobile-budget") await mobileBudget(browser);
  if (!only || only === "mobile-layout") await adminPhone(browser);
  if (only === "shots") await shots(browser);
  if (only === "phone-shots") await phoneShots(browser);
  if (only === "share-image") await shareImage(browser);
  if (!only || only === "share-menu") await shareMenu(browser);
  if (!only || only === "story-card") await storyCard(browser);
  if (!only || only === "blog-discovery") { state.manyPosts = true; try { await blogDiscovery(browser); await blogDiscoveryShort(browser); } finally { state.manyPosts = false; } }
  if (!only || only === "admin-posts") await adminPosts(browser);
  if (!only || only === "admin-models") await adminModels(browser);
  if (!only || only === "admin-a11y") await adminA11y(browser);
  if (!only || only === "writing-rules") await writingRules(browser);
  if (!only || only === "editor-toolbar") await editorToolbar(browser);
  if (!only || only === "ai-editor") await aiEditor(browser);
  if (!only || only === "blog-editor" || only === "share-preview") await shareSettings(browser);
} finally {
  await browser.close();
  server.close();
}
console.log(`\n${results.filter((r) => r.ok).length}/${results.length} checks passed; screenshots in ${outDir}`);
process.exit(failed ? 1 : 0);
