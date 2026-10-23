// The browser's whole view of the chat backend. It sends cookies and message
// text only: never a profile, key, route prefix or session id (the server picks
// those). Every failure comes back as a code from the fixed API vocabulary.

import { purgeUserState } from "./pwa";

export interface ChatSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  activeRunId: string;
}

export interface Turn {
  /** Position in the conversation (0 = oldest): the turn's identity across pages. */
  seq: number;
  role: "user" | "assistant";
  text: string;
}

export type ApiResult<T> = { ok: true; value: T } | { ok: false; code: string; status: number };

const CODES = [
  "UNAUTHENTICATED",
  "ACCOUNT_PENDING",
  "ACCOUNT_REJECTED",
  "ACCOUNT_SUSPENDED",
  "TENANT_NOT_READY",
  "NOT_FOUND",
  "CAPABILITY_UNAVAILABLE",
  "RATE_LIMITED",
  "HERMES_UNAVAILABLE",
  "RUN_CONFLICT",
  "UPSTREAM_TIMEOUT",
  "INVALID_REQUEST",
  "FORBIDDEN",
  "HISTORY_CHANGED",
  "SEARCH_TIMEOUT",
  "BUDGET_REACHED",
] as const;

/** Words shown for a code. The server's own message is never displayed. */
export function describeCode(code: string): string {
  switch (code) {
    case "UNAUTHENTICATED":
      return "You are signed out. Sign in again.";
    case "ACCOUNT_PENDING":
    case "ACCOUNT_REJECTED":
    case "ACCOUNT_SUSPENDED":
    case "TENANT_NOT_READY":
      return "Your workspace is not available right now.";
    case "NOT_FOUND":
      return "That conversation no longer exists.";
    case "CAPABILITY_UNAVAILABLE":
      return "The assistant does not support this.";
    case "RATE_LIMITED":
      return "Too many requests. Wait a moment and try again.";
    case "HERMES_UNAVAILABLE":
      return "The assistant is unavailable. Try again shortly.";
    case "UPSTREAM_TIMEOUT":
      return "The assistant did not answer in time.";
    case "RUN_CONFLICT":
      return "This conversation is already working on a reply.";
    case "INVALID_REQUEST":
      return "That message could not be sent.";
    case "HISTORY_CHANGED":
      return "This conversation changed while you were reading it.";
    case "SEARCH_TIMEOUT":
      return "The search took too long. Try more specific words.";
    case "BUDGET_REACHED":
      return "This conversation has reached its token budget, so it takes no new messages. Start a new chat to continue.";
    case "NETWORK":
      return "Could not reach Antares. Check your connection.";
    default:
      return "Something went wrong.";
  }
}

async function call<T>(method: string, path: string, parse: (b: unknown) => T | null, body?: unknown, headers?: Record<string, string>): Promise<ApiResult<T>> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    return { ok: false, code: "NETWORK", status: 0 };
  }
  let json: unknown = null;
  try {
    json = await res.json();
  } catch {
    // not JSON: handled below
  }
  if (!res.ok) {
    const code = codeOf(json);
    if (code === "ACCOUNT_SUSPENDED" || res.status === 401) purgeUserState();
    return { ok: false, code: code ?? "HERMES_UNAVAILABLE", status: res.status };
  }
  const value = parse(json);
  // A success we cannot read is a failure, never a guessed value.
  return value === null ? { ok: false, code: "HERMES_UNAVAILABLE", status: res.status } : { ok: true, value };
}

function codeOf(json: unknown): string | null {
  if (typeof json !== "object" || json === null) return null;
  const e = (json as Record<string, unknown>).error;
  if (typeof e !== "object" || e === null) return null;
  const c = (e as Record<string, unknown>).code;
  return typeof c === "string" && (CODES as readonly string[]).includes(c) ? c : null;
}

const str = (v: unknown): string => (typeof v === "string" ? v : "");
const obj = (v: unknown): Record<string, unknown> | null => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null);

export function parseChat(v: unknown): ChatSummary | null {
  const o = obj(v);
  if (!o || !str(o.id)) return null;
  return { id: str(o.id), title: str(o.title), createdAt: str(o.createdAt), updatedAt: str(o.updatedAt), activeRunId: str(o.activeRunId) };
}

export interface HistoryPage {
  turns: Turn[];
  /** Empty when this page reaches the start of the conversation. */
  nextCursor: string;
  total: number;
}

export function parseHistory(v: unknown): HistoryPage | null {
  const o = obj(v);
  if (!o || !Array.isArray(o.messages)) return null;
  const turns: Turn[] = [];
  for (const m of o.messages) {
    const mo = obj(m);
    if (!mo || (mo.role !== "user" && mo.role !== "assistant")) continue;
    // A turn without a usable position cannot be placed: the page is refused, not guessed at.
    if (typeof mo.seq !== "number" || !Number.isInteger(mo.seq) || mo.seq < 0) return null;
    turns.push({ seq: mo.seq, role: mo.role, text: str(mo.text) });
  }
  for (let i = 1; i < turns.length; i++) if (turns[i].seq <= turns[i - 1].seq) return null;
  return { turns, nextCursor: str(o.nextCursor), total: typeof o.total === "number" ? o.total : turns.length };
}

export interface ChatList {
  chats: ChatSummary[];
  nextCursor: string;
}

const q = (cursor?: string) => (cursor ? `?cursor=${encodeURIComponent(cursor)}` : "");

export const listChats = (cursor?: string) =>
  call("GET", `/api/chats${q(cursor)}`, (b): ChatList | null => {
    const o = obj(b);
    if (!o || !Array.isArray(o.chats)) return null;
    return { chats: o.chats.map(parseChat).filter((c): c is ChatSummary => c !== null), nextCursor: str(o.nextCursor) };
  });

export const getChat = (chatId: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}`, parseChat);

export const createChat = () => call("POST", "/api/chats", parseChat, {});

/** The newest page, or the page before `cursor`. The page size is the server's. */
export const getMessages = (chatId: string, cursor?: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}/messages${q(cursor)}`, parseHistory);

export interface RunRef {
  runId: string;
  status: string;
}

const parseRunRef = (b: unknown): RunRef | null => {
  const o = obj(b);
  return o && str(o.runId) ? { runId: str(o.runId), status: str(o.status) } : null;
};

/** key is chosen by the caller so a retry of the same send repeats it. */
export const sendMessage = (chatId: string, input: string, key: string) =>
  call("POST", `/api/chats/${encodeURIComponent(chatId)}/messages`, parseRunRef, { input }, { "Idempotency-Key": key });

export interface CancelResult extends RunRef {
  upstreamStopped: boolean;
}

export const cancelRun = (runId: string) =>
  call("POST", `/api/runs/${encodeURIComponent(runId)}/cancel`, (b): CancelResult | null => {
    const r = parseRunRef(b);
    return r ? { ...r, upstreamStopped: obj(b)?.upstreamStopped === true } : null;
  });

export const getRun = (runId: string) => call("GET", `/api/runs/${encodeURIComponent(runId)}`, parseRunRef);

export function newIdempotencyKey(): string {
  return `web-${crypto.randomUUID()}`;
}

export const runEventsUrl = (runId: string) => `/api/runs/${encodeURIComponent(runId)}/events`;

// ---- search (UXP-003) ------------------------------------------------------------

/** A run of snippet text. Always rendered as text; `hit` marks the matched span. */
export interface Segment {
  text: string;
  hit: boolean;
}

export interface SearchResult {
  chatId: string;
  chatTitle: string;
  kind: "title" | "message";
  /** The turn's seq for a message hit (the anchor); null for a title hit. */
  turn: number | null;
  role: "title" | "user" | "assistant";
  /** The turn's timestamp (a title hit: when the chat was created); empty when the turn has no timestamp. */
  createdAt: string;
  snippet: Segment[];
}

export interface SearchPage {
  results: SearchResult[];
  nextCursor: string;
  /** How much of the account is searchable; incomplete means results may be missing. */
  index: { complete: boolean; chats: number; pending: number; failed: number; partial: number };
}

export function parseSearch(v: unknown): SearchPage | null {
  const o = obj(v);
  const ix = obj(o?.index);
  if (!o || !ix || !Array.isArray(o.results) || typeof ix.complete !== "boolean") return null;
  const results: SearchResult[] = [];
  for (const r of o.results) {
    const ro = obj(r);
    if (!ro || !str(ro.chatId) || !Array.isArray(ro.snippet)) return null;
    if (ro.kind !== "title" && ro.kind !== "message") return null;
    if (ro.role !== "title" && ro.role !== "user" && ro.role !== "assistant") return null;
    let turn: number | null = null;
    if (ro.kind === "message") {
      // A message hit without a usable anchor cannot be opened at its turn: refused, not guessed at.
      if (typeof ro.turn !== "number" || !Number.isInteger(ro.turn) || ro.turn < 0) return null;
      turn = ro.turn;
    }
    const snippet: Segment[] = [];
    for (const sg of ro.snippet) {
      const so = obj(sg);
      if (!so || typeof so.text !== "string") return null;
      snippet.push({ text: so.text, hit: so.hit === true });
    }
    results.push({ chatId: str(ro.chatId), chatTitle: str(ro.chatTitle), kind: ro.kind, turn, role: ro.role, createdAt: str(ro.createdAt), snippet });
  }
  const num = (n: unknown) => (typeof n === "number" && Number.isFinite(n) ? n : 0);
  return { results, nextCursor: str(o.nextCursor), index: { complete: ix.complete, chats: num(ix.chats), pending: num(ix.pending), failed: num(ix.failed), partial: num(ix.partial) } };
}

export interface SearchOptions {
  /** Only this conversation ("find in this chat"). */
  chatId?: string;
  role?: "user" | "assistant";
  cursor?: string;
}

export function searchChats(text: string, o: SearchOptions = {}) {
  const p = new URLSearchParams({ q: text });
  if (o.role) p.set("role", o.role);
  if (o.cursor) p.set("cursor", o.cursor);
  const path = o.chatId ? `/api/chats/${encodeURIComponent(o.chatId)}/search` : "/api/search/chats";
  return call("GET", `${path}?${p.toString()}`, parseSearch);
}

/** Per-chat usage as Hermes reported it. null means Hermes reported nothing, which is not zero. */
export interface ChatUsage {
  tokens: { input: number; output: number; cacheRead: number; total: number } | null;
  finishedRuns: number;
  reportedRuns: number;
  costIDR: number | null;
  unpricedRuns: number;
  priceListDate: string;
  windowTokens: number | null;
  budget: { limit: number; used: number; reached: boolean };
}

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);

export function parseUsage(v: unknown): ChatUsage | null {
  const o = obj(v);
  const runs = obj(o?.runs);
  const cost = obj(o?.cost);
  const ctx = obj(o?.context);
  const b = obj(o?.budget);
  if (!o || !runs || !cost || !ctx || !b) return null;
  const limit = num(b.limitTokens);
  const used = num(b.usedTokens);
  const finished = num(runs.finished);
  const reported = num(runs.reported);
  if (limit === null || used === null || finished === null || reported === null) return null;
  const t = obj(o.tokens);
  const tokens = t && num(t.input) !== null && num(t.output) !== null && num(t.total) !== null ? { input: num(t.input)!, output: num(t.output)!, cacheRead: num(t.cacheRead) ?? 0, total: num(t.total)! } : null;
  return {
    tokens,
    finishedRuns: finished,
    reportedRuns: reported,
    costIDR: num(cost.amount),
    unpricedRuns: num(cost.unpricedRuns) ?? 0,
    priceListDate: str(cost.priceListDate),
    windowTokens: num(ctx.windowTokens),
    budget: { limit, used, reached: b.reached === true },
  };
}

export const getUsage = (chatId: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}/usage`, parseUsage);

const fmt = (n: number) => n.toLocaleString("en-US");

/** The words shown for a chat's usage. Unknown is "—", cost is always labelled an estimate, the budget behaviour is stated. */
export function describeUsage(u: ChatUsage): string[] {
  const lines: string[] = [];
  lines.push(u.tokens ? `Tokens: ${fmt(u.tokens.total)} (${fmt(u.tokens.input)} in, ${fmt(u.tokens.output)} out)` : "Tokens: — (Hermes reported none)");
  lines.push(u.costIDR !== null ? `Estimated cost: about Rp ${fmt(Math.round(u.costIDR))}, from the provider price list of ${u.priceListDate}; not a bill` : "Estimated cost: — (no price for the model)");
  lines.push(`Context in use: — (Hermes does not report it)${u.windowTokens !== null ? `; model window ${fmt(u.windowTokens)} tokens` : ""}`);
  if (u.finishedRuns > u.reportedRuns) lines.push(`${u.finishedRuns - u.reportedRuns} finished ${u.finishedRuns - u.reportedRuns === 1 ? "reply" : "replies"} reported no usage and ${u.finishedRuns - u.reportedRuns === 1 ? "is" : "are"} not counted`);
  lines.push(
    u.budget.reached
      ? `Budget reached (${fmt(u.budget.used)} of ${fmt(u.budget.limit)} tokens): this chat takes no new messages`
      : `Budget: ${fmt(u.budget.used)} of ${fmt(u.budget.limit)} tokens; at the limit the chat stops taking new messages (a reply already running finishes)`,
  );
  return lines;
}
