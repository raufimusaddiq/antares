// The administrator's view of published blog posts (specs/004-admin-full-control). Published content and takedown state only:
// the server never sends a draft, a later version, a check or anything about readers, and this file has no field for them.

import { call, type AdminResult } from "./adminApi";
import { parsePublicPost } from "./blog/blogApi";
import type { DocNode, PublicBlock } from "./blog/types";

export type PostState = "visible" | "hidden_account" | "taken_down";
export const POST_STATES: readonly PostState[] = ["visible", "hidden_account", "taken_down"];

export interface AdminTakedown {
  at: string;
  reason: string;
  byName: string;
}

export interface AdminPost {
  id: string;
  slug: string;
  title: string;
  summary: string;
  authorName: string;
  publishedAt: string;
  state: PostState;
  takedown: AdminTakedown | null;
}

export interface AdminPostView extends AdminPost {
  coverUrl: string;
  /** Empty for a post that is no longer published (taken down, then unpublished by its author). */
  doc: DocNode | null;
  blocks: PublicBlock[];
}

export interface AdminPostPage {
  items: AdminPost[];
  /** The cursor of the next page; empty on the last one. */
  next: string;
}

const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);
const str = (v: unknown): string => (typeof v === "string" ? v : "");

export function parseAdminPost(v: unknown): AdminPost | null {
  const o = obj(v);
  if (!o || !/^[0-9a-f]{32}$/.test(str(o.id)) || !POST_STATES.includes(str(o.state) as PostState)) return null;
  const t = obj(o.takedown);
  return {
    id: str(o.id),
    slug: str(o.slug),
    title: str(o.title),
    summary: str(o.summary),
    authorName: str(o.authorName),
    publishedAt: str(o.publishedAt),
    state: str(o.state) as PostState,
    takedown: t ? { at: str(t.at), reason: str(t.reason), byName: str(t.byName) } : null,
  };
}

export function parseAdminPostPage(v: unknown): AdminPostPage | null {
  const o = obj(v);
  if (!o || !Array.isArray(o.items)) return null;
  const items: AdminPost[] = [];
  for (const it of o.items) {
    const p = parseAdminPost(it);
    if (!p) return null; // a row we cannot read makes the page unreadable, never a shorter list
    items.push(p);
  }
  return { items, next: str(o.next) };
}

export function parseAdminPostView(v: unknown): AdminPostView | null {
  const p = parseAdminPost(v);
  const o = obj(v);
  if (!p || !o) return null;
  // The content is the public post's own shape (the published version), so the public parser is the one that reads it.
  const pub = o.doc ? parsePublicPost(o) : null;
  return { ...p, coverUrl: pub?.coverUrl ?? "", doc: pub?.doc ?? null, blocks: pub?.blocks ?? [] };
}

export interface PostFilter {
  state: PostState | "";
  q: string;
}

export function listAdminPosts(f: PostFilter, after: string, signal?: AbortSignal) {
  const p = new URLSearchParams();
  if (f.state) p.set("state", f.state);
  if (f.q) p.set("q", f.q);
  if (after) p.set("after", after);
  const qs = p.toString();
  return call("GET", `/api/admin/blog-posts${qs ? `?${qs}` : ""}`, parseAdminPostPage, undefined, signal);
}
export const getAdminPost = (id: string, signal?: AbortSignal) => call("GET", `/api/admin/blog-posts/${id}`, parseAdminPostView, undefined, signal);
export const takeDownPost = (id: string, reason: string) => call("POST", `/api/admin/blog-posts/${id}/take-down`, parseAdminPost, { reason });

/** A restore answers with the post, or, when the author had unpublished it meanwhile, with a note and no post. */
export type RestoreResult = { post: AdminPost } | { note: string };
export const restorePost = (id: string): Promise<AdminResult<RestoreResult>> =>
  call("POST", `/api/admin/blog-posts/${id}/restore`, (v) => {
    const o = obj(v);
    if (o && typeof o.note === "string" && !o.id) return { note: o.note };
    const p = parseAdminPost(v);
    return p ? { post: p } : null;
  });

export const MAX_REASON = 500;
