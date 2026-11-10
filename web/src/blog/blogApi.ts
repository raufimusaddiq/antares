// The browser's whole view of the blog backend. It sends cookies and the post's own fields only: never a profile,
// key, route prefix, header or session id (the server picks those). Every failure is a code or a status, and the
// server's own message is never displayed.

import { purgeDrafts } from "../drafts";
import { purgeUserState } from "../pwa";
import type { Limits } from "./limits";
import { EMPTY_DOC, type Capabilities, type Check, type CheckBlock, type CheckNote, type Decision, type DocNode, type Post, type PostStatus, type PostSummary, type PublicBlock, type PublicPost, type PublicSummary, type Source, type Takedown, type TakedownNotice } from "./types";

export type BlogResult<T> = { ok: true; value: T } | { ok: false; status: number; code: string; latest?: Post };

const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);
const str = (v: unknown): string => (typeof v === "string" ? v : "");
const num = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

const STATUSES: readonly string[] = ["draft", "checking", "ready", "published", "unpublished"];
const CHECK_STATES: readonly string[] = ["running", "ready", "failed", "cancelled", "timed_out", "out_of_date"];

/** An image address is this site's own path, a local blob, or https without a login in it; anything else is dropped. */
export const mediaUrl = (u: unknown): string => {
  if (typeof u !== "string") return "";
  if (u.startsWith("/") && !u.startsWith("//") && !u.includes("\\")) return u;
  if (u.startsWith("blob:")) return u;
  return httpsOnly(u);
};

/** Only an https address without a login in it is ever kept: a source or cover with any other scheme is dropped. */
export const httpsOnly = (u: unknown): string => {
  if (typeof u !== "string") return "";
  try {
    const p = new URL(u);
    return p.protocol === "https:" && !p.username && !p.password ? p.href : "";
  } catch {
    return "";
  }
};

export function parseSource(v: unknown): Source | null {
  const o = obj(v);
  const url = o ? httpsOnly(o.url) : "";
  return o && url ? { url, title: str(o.title) || url, retrievedAt: str(o.retrievedAt), verified: o.verified === true } : null;
}

const sourcesOf = (v: unknown): Source[] => arr(v).map(parseSource).filter((s): s is Source => s !== null);

export function parseDoc(v: unknown): DocNode {
  const o = obj(v);
  return o && o.type === "doc" ? (o as unknown as DocNode) : EMPTY_DOC;
}

export function parseBlock(v: unknown): CheckBlock | null {
  const o = obj(v);
  if (!o || !str(o.id)) return null;
  const d = str(o.decision);
  return {
    id: str(o.id),
    kind: str(o.kind),
    afterNode: num(o.afterNode),
    markup: str(o.markup),
    fallback: str(o.fallback),
    decision: (d === "accepted" || d === "rejected" ? d : "proposed") as Decision,
    needsAck: o.needsAck === true,
    sources: sourcesOf(o.sources),
  };
}

const NOTE_KINDS = ["clarity", "fact", "missing-context", "unsourced", "style"];

export function parseCheck(v: unknown): Check | null {
  const o = obj(v);
  if (!o || !str(o.id) || !CHECK_STATES.includes(str(o.state))) return null;
  const r = str(o.reason);
  return {
    id: str(o.id),
    state: str(o.state) as Check["state"],
    reason: r === "disconnected" || r === "unusable" || r === "error" ? r : "",
    notes: arr(o.notes)
      .map((n) => obj(n))
      .filter((n): n is Record<string, unknown> => n !== null && str(n.text) !== "")
      .map((n): CheckNote => ({ kind: (NOTE_KINDS.includes(str(n.kind)) ? str(n.kind) : "clarity") as CheckNote["kind"], text: str(n.text), afterNode: num(n.afterNode) })),
    notices: arr(o.notices).map(str).filter(Boolean),
    blocks: arr(o.blocks).map(parseBlock).filter((b): b is CheckBlock => b !== null),
  };
}

function parseTakedown(v: unknown): Takedown | null {
  const o = obj(v);
  return o && str(o.at) ? { at: str(o.at), reason: str(o.reason) } : null;
}

export function parsePost(v: unknown): Post | null {
  const o = obj(v);
  if (!o || !str(o.id) || !STATUSES.includes(str(o.status))) return null;
  return {
    id: str(o.id),
    title: str(o.title),
    summary: str(o.summary),
    slug: str(o.slug),
    coverUrl: str(o.coverUrl),
    status: str(o.status) as PostStatus,
    latestVersion: num(o.latestVersion),
    publishedVersion: num(o.publishedVersion),
    doc: parseDoc(o.doc),
    check: o.check ? parseCheck(o.check) : null,
    updatedAt: str(o.updatedAt),
    takedown: parseTakedown(o.takedown),
  };
}

export function parseSummary(v: unknown): PostSummary | null {
  const o = obj(v);
  if (!o || !str(o.id) || !STATUSES.includes(str(o.status))) return null;
  return { id: str(o.id), title: str(o.title), status: str(o.status) as PostStatus, slug: str(o.slug), updatedAt: str(o.updatedAt), publishedAt: str(o.publishedAt), interactive: o.interactive === true, takedown: parseTakedown(o.takedown) };
}

export function parseLimits(v: unknown): Limits | null {
  const o = obj(v);
  if (!o) return null;
  const l = { checkBytes: num(o.checkBytes), docBytes: num(o.docBytes), imagesPerPost: num(o.imagesPerPost), imageBytes: num(o.imageBytes), imageBytesTotal: num(o.imageBytesTotal), blocksPerCheck: num(o.blocksPerCheck), sourcesPerBlock: num(o.sourcesPerBlock), checksPer24h: num(o.checksPer24h) };
  // A number the server did not send must not become "0 allowed" on the page: no limits at all is the honest answer.
  return Object.values(l).every((n) => n > 0) ? l : null;
}

export function parseCapabilities(v: unknown): Capabilities | null {
  const o = obj(v);
  return o ? { writing: o.writing === true, check: o.check === true, webSearch: o.webSearch === true } : null;
}

export function parsePublicSummary(v: unknown): PublicSummary | null {
  const o = obj(v);
  return o && str(o.slug) ? { slug: str(o.slug), title: str(o.title), summary: str(o.summary), coverUrl: str(o.coverUrl), authorName: str(o.authorName), publishedAt: str(o.publishedAt) } : null;
}

export function parsePublicPost(v: unknown): PublicPost | null {
  const o = obj(v);
  if (!o || !str(o.title)) return null;
  return {
    title: str(o.title),
    summary: str(o.summary),
    coverUrl: str(o.coverUrl),
    doc: parseDoc(o.doc),
    blocks: arr(o.blocks)
      .map((b) => obj(b))
      .filter((b): b is Record<string, unknown> => b !== null)
      .map((b): PublicBlock => ({ anchor: num(b.anchor), markup: str(b.markup), fallback: str(b.fallback), sources: sourcesOf(b.sources) })),
    authorName: str(o.authorName),
    publishedAt: str(o.publishedAt),
  };
}

async function request<T>(method: string, path: string, parse: (b: unknown) => T | null, body?: unknown, signal?: AbortSignal): Promise<BlogResult<T>> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch {
    return { ok: false, status: 0, code: "NETWORK" };
  }
  let json: unknown = null;
  try {
    json = await res.json();
  } catch {
    // no body, or not JSON: handled below
  }
  if (!res.ok) {
    if (res.status === 401) {
      purgeUserState();
      purgeDrafts();
    }
    const e = obj(obj(json)?.error);
    const latest = res.status === 409 ? parsePost(obj(json)?.latest) : null;
    return { ok: false, status: res.status, code: str(e?.code) || "ERROR", ...(latest ? { latest } : {}) };
  }
  const value = parse(json);
  // A success we cannot read is a failure, never a guessed value.
  return value === null ? { ok: false, status: res.status, code: "UNREADABLE" } : { ok: true, value };
}

const enc = encodeURIComponent;
const nothing = () => true as const;
const P = "/api/blog/posts";
const list = <T>(item: (v: unknown) => T | null) => (b: unknown) => (Array.isArray(b) ? b.map(item).filter((x): x is T => x !== null) : null);

export const listPosts = (signal?: AbortSignal) => request("GET", P, list(parseSummary), undefined, signal);
export const createPost = () => request("POST", P, parsePost, {});
export const getPost = (id: string, signal?: AbortSignal) => request("GET", `${P}/${enc(id)}`, parsePost, undefined, signal);
export const autosave = (id: string, baseVersion: number, change: { title?: string; summary?: string; doc?: DocNode }) => request("PUT", `${P}/${enc(id)}`, parsePost, { baseVersion, ...change });
export const saveSettings = (id: string, s: { slug?: string; summary?: string; coverAssetId?: string }) => request("PATCH", `${P}/${enc(id)}/settings`, parsePost, s);
export const deletePost = (id: string) => request("DELETE", `${P}/${enc(id)}`, nothing);
export const duplicatePost = (id: string) => request("POST", `${P}/${enc(id)}/duplicate`, parsePost, {});
export const startCheck = (id: string) => request("POST", `${P}/${enc(id)}/check`, parseCheck, {});
export const getCheck = (id: string, signal?: AbortSignal) => request("GET", `${P}/${enc(id)}/check`, parseCheck, undefined, signal);
export const cancelCheck = (id: string) => request("POST", `${P}/${enc(id)}/check/cancel`, parseCheck, {});
export const decideBlock = (id: string, blockId: string, decision: "accepted" | "rejected", ack?: true) => request("PUT", `${P}/${enc(id)}/blocks/${enc(blockId)}`, parseBlock, { decision, ...(ack ? { ack } : {}) });
export const publish = (id: string, mode: "enriched" | "original") => request("POST", `${P}/${enc(id)}/publish`, parsePost, { mode });
export const unpublish = (id: string) => request("POST", `${P}/${enc(id)}/unpublish`, parsePost, {});
export function parseNotices(v: unknown): TakedownNotice[] | null {
  const o = obj(v);
  if (!o || !Array.isArray(o.notices)) return null;
  const out: TakedownNotice[] = [];
  for (const n of o.notices) {
    const x = obj(n);
    if (!x || !str(x.postId)) return null;
    out.push({ postId: str(x.postId), title: str(x.title), reason: str(x.reason), at: str(x.at) });
  }
  return out;
}
/** Takedowns this author has not yet seen (the one-time banner). */
export const listNotices = (signal?: AbortSignal) => request("GET", "/api/blog/notices", parseNotices, undefined, signal);
export const markNoticeSeen = (postId: string) => request("POST", `/api/blog/notices/${enc(postId)}/seen`, nothing, {});
export const getLimits = (signal?: AbortSignal) => request("GET", "/api/blog/limits", parseLimits, undefined, signal);
export const getCapabilities = (signal?: AbortSignal) => request("GET", "/api/blog/capabilities", parseCapabilities, undefined, signal);

export async function uploadImage(id: string, file: File): Promise<BlogResult<{ url: string; assetId: string }>> {
  let res: Response;
  try {
    res = await fetch(`${P}/${enc(id)}/assets`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": file.type, Accept: "application/json" }, body: file });
  } catch {
    return { ok: false, status: 0, code: "NETWORK" };
  }
  const o = res.ok ? obj(await res.json().catch(() => null)) : null;
  return o && str(o.assetId) && str(o.url) ? { ok: true, value: { url: str(o.url), assetId: str(o.assetId) } } : { ok: false, status: res.status, code: res.ok ? "UNREADABLE" : "ERROR" };
}

// Public: no session, nothing but the published post.
const PUB = "/api/public/blog/posts";
/** One page of the public list: `limit` is the page size (the server allows 1-20), `q` an optional search over title, summary and author. */
export const listPublic = (opts: { page?: number; limit?: number; q?: string } = {}, signal?: AbortSignal) => {
  const q = new URLSearchParams();
  if (opts.page && opts.page > 1) q.set("page", String(opts.page));
  if (opts.limit) q.set("limit", String(opts.limit));
  const text = (opts.q ?? "").trim();
  if (text) q.set("q", text);
  const qs = q.toString();
  return request("GET", qs ? `${PUB}?${qs}` : PUB, list(parsePublicSummary), undefined, signal);
};

/** The addresses of the signed-in reader's own published posts, for the "Yours" label. Any failure (a visitor, a pending account, a network error) is an empty set: no label, nothing else changes. */
export async function listMyPublishedSlugs(signal?: AbortSignal): Promise<Set<string>> {
  const r = await listPosts(signal);
  return new Set(r.ok ? r.value.filter((p) => p.status === "published" && p.slug).map((p) => p.slug) : []);
}
export const getPublic = (slug: string, signal?: AbortSignal) => request("GET", `${PUB}/${enc(slug)}`, parsePublicPost, undefined, signal);

/** Words shown for a failed call. The server's own message is never displayed. */
export function describeBlogError(r: { status: number; code: string }, what = "do that"): string {
  if (r.code === "NETWORK") return "Could not reach Antares. Check your connection.";
  if (r.status === 401) return "You are signed out. Sign in again.";
  if (r.status === 404) return "That post no longer exists.";
  if (r.status === 409 && r.code === "TAKEN_DOWN") return "An administrator took this post down. You can keep editing it, but it cannot be published until it is restored.";
  if (r.status === 409) return "This post changed somewhere else.";
  if (r.status === 413) return "That is too large.";
  if (r.status === 429) return "Too many requests. Wait a minute and try again.";
  return `Could not ${what}. Try again.`;
}
