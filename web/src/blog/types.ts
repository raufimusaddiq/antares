// Shapes of the blog API (specs/001-blog-cms-openui-publish/contracts/http-api.md). The server decides every
// state; nothing here computes eligibility or invents a value the server did not send.

export type PostStatus = "draft" | "checking" | "ready" | "published" | "unpublished";

/** A document node: the editor's JSON, never HTML. Only the allowlisted node and mark types are drawn. */
export interface DocNode {
  type: string;
  attrs?: Record<string, unknown>;
  content?: DocNode[];
  text?: string;
  marks?: { type: string; attrs?: Record<string, unknown> }[];
}

export const EMPTY_DOC: DocNode = { type: "doc", content: [{ type: "paragraph" }] };

export interface PostSummary {
  id: string;
  title: string;
  status: PostStatus;
  slug: string;
  updatedAt: string;
  publishedAt: string;
  /** The published post has AI-added blocks. */
  interactive: boolean;
}

export interface Source {
  url: string;
  title: string;
  retrievedAt: string;
  /** Set only by the server, when the address appeared in the run's observed search results. */
  verified: boolean;
}

export type Decision = "proposed" | "accepted" | "rejected";

export interface CheckBlock {
  id: string;
  kind: string;
  afterNode: number;
  markup: string;
  fallback: string;
  decision: Decision;
  /** Acceptance must be explicit because a source is unverified. */
  needsAck: boolean;
  sources: Source[];
}

export type CheckState = "running" | "ready" | "failed" | "cancelled" | "timed_out" | "out_of_date";

export interface CheckNote {
  kind: "clarity" | "fact" | "missing-context" | "unsourced";
  text: string;
  afterNode: number;
}

export interface Check {
  id: string;
  state: CheckState;
  /** failed only: "disconnected" and "unusable" are told apart from a plain failure. */
  reason: "" | "error" | "disconnected" | "unusable";
  notes: CheckNote[];
  /** One plain sentence per block the server dropped. */
  notices: string[];
  blocks: CheckBlock[];
}

export interface Post {
  id: string;
  title: string;
  summary: string;
  slug: string;
  coverUrl: string;
  status: PostStatus;
  latestVersion: number;
  publishedVersion: number;
  doc: DocNode;
  check: Check | null;
  updatedAt: string;
}

export interface Capabilities {
  writing: boolean;
  check: boolean;
  webSearch: boolean;
}

export interface PublicSummary {
  slug: string;
  title: string;
  summary: string;
  coverUrl: string;
  authorName: string;
  publishedAt: string;
}

export interface PublicBlock {
  anchor: number;
  markup: string;
  fallback: string;
  sources: Source[];
}

export interface PublicPost {
  title: string;
  summary: string;
  coverUrl: string;
  doc: DocNode;
  blocks: PublicBlock[];
  authorName: string;
  publishedAt: string;
}
