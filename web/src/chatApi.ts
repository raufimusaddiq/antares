// The browser's whole view of the chat backend. It sends cookies and message
// text only: never a profile, key, route prefix or session id (the server picks
// those). Every failure comes back as a code from the fixed API vocabulary.

import { purgeUserState } from "./pwa";
import type { AttachmentRef } from "./MediaPreview";

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
  /** The server's hash of this turn; quoted back to name it (artifact links). Absent on turns not read from history. */
  ref?: string;
  /** Images the user sent with this turn (owner assets; available is false once deleted). */
  attachments?: AttachmentRef[];
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
  "MODEL_UNAVAILABLE",
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
    case "PROMPT_STALE":
      return "That question is no longer waiting for an answer.";
    case "RUN_CONFLICT":
      return "This conversation is already working on a reply.";
    case "INVALID_REQUEST":
      return "That message could not be sent.";
    case "HISTORY_CHANGED":
      return "This conversation changed while you were reading it.";
    case "SEARCH_TIMEOUT":
      return "The search took too long. Try more specific words.";
    case "BUDGET_REACHED":
      return "This conversation has reached the token limit you set, so it takes no new messages. Raise or remove the limit under Usage to continue.";
    case "MODEL_UNAVAILABLE":
      return "That model or option is not available right now. Nothing was changed.";
    case "NETWORK":
      return "Could not reach Antares. Check your connection.";
    default:
      return "Something went wrong.";
  }
}

async function call<T>(method: string, path: string, parse: (b: unknown) => T | null, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal): Promise<ApiResult<T>> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
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

/** Removes exactly n trailing "[image]" transport placeholders (as the server's link check does); a "[image]" the user typed stays. */
export function stripImageMarkers(text: string, n: number): string {
  let t = text;
  for (let i = 0; i < n; i++) {
    const trimmed = t.replace(/\s+$/, "");
    if (!trimmed.endsWith("[image]")) return text; // not what the server linked: keep it as received
    t = trimmed.slice(0, -"[image]".length);
  }
  return t.replace(/\s+$/, "");
}

function parseAttachments(v: unknown): AttachmentRef[] {
  if (!Array.isArray(v)) return [];
  const out: AttachmentRef[] = [];
  for (const a of v) {
    const o = obj(a);
    if (o && /^[0-9a-f]{32}$/.test(str(o.assetId)) && str(o.mediaType).startsWith("image/")) out.push({ assetId: str(o.assetId), mediaType: str(o.mediaType), available: o.available === true });
  }
  return out;
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
    const attachments = parseAttachments(mo.attachments);
    // The history read renders each image as "[image]"; when the images are shown as thumbnails the markers go.
    const text = attachments.length > 0 ? stripImageMarkers(str(mo.text), attachments.length) : str(mo.text);
    turns.push({ seq: mo.seq, role: mo.role, text, ref: str(mo.ref) || undefined, ...(attachments.length > 0 ? { attachments } : {}) });
  }
  for (let i = 1; i < turns.length; i++) if (turns[i].seq <= turns[i - 1].seq) return null;
  return { turns, nextCursor: str(o.nextCursor), total: typeof o.total === "number" ? o.total : turns.length };
}

export interface ChatList {
  chats: ChatSummary[];
  nextCursor: string;
}

const q = (cursor?: string, limit?: number) => {
  const p = [cursor ? `cursor=${encodeURIComponent(cursor)}` : "", limit ? `limit=${limit}` : ""].filter(Boolean);
  return p.length ? `?${p.join("&")}` : "";
};

export const listChats = (cursor?: string) =>
  call("GET", `/api/chats${q(cursor)}`, (b): ChatList | null => {
    const o = obj(b);
    if (!o || !Array.isArray(o.chats)) return null;
    return { chats: o.chats.map(parseChat).filter((c): c is ChatSummary => c !== null), nextCursor: str(o.nextCursor) };
  });

export const getChat = (chatId: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}`, parseChat);

export const createChat = () => call("POST", "/api/chats", parseChat, {});

/** The newest page, or the page before `cursor`. The page size is the server's. */
export const getMessages = (chatId: string, cursor?: string, limit?: number) =>
  call("GET", `/api/chats/${encodeURIComponent(chatId)}/messages${q(cursor, limit)}`, parseHistory);

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

/** A decision the assistant is waiting for on a run (approval or question). Read from the server, never held only in the browser. */
export interface RunPrompt {
  promptId: string;
  kind: "approval" | "clarify";
  text: string;
  detail: string;
  choices: string[];
}

const parsePrompt = (v: unknown): RunPrompt | null => {
  const o = obj(v);
  if (!o || !str(o.promptId) || (o.kind !== "approval" && o.kind !== "clarify")) return null;
  const choices = Array.isArray(o.choices) ? o.choices.filter((c): c is string => typeof c === "string") : [];
  return { promptId: str(o.promptId), kind: o.kind, text: str(o.text), detail: str(o.detail), choices };
};

export const getRunPrompts = (runId: string) =>
  call("GET", `/api/runs/${encodeURIComponent(runId)}`, (b): RunPrompt[] | null => {
    const o = obj(b);
    if (!o) return null;
    if (o.prompts === undefined) return [];
    return Array.isArray(o.prompts) ? o.prompts.map(parsePrompt).filter((p): p is RunPrompt => p !== null) : null;
  });

const accepted = (b: unknown) => (obj(b)?.accepted === true ? true : null);

/** Only "once" and "deny" exist: the server refuses a standing grant. */
export const approveRun = (runId: string, promptId: string, choice: "once" | "deny") =>
  call("POST", `/api/runs/${encodeURIComponent(runId)}/approve`, accepted, { promptId, choice });

export const answerRun = (runId: string, promptId: string, answer: string) =>
  call("POST", `/api/runs/${encodeURIComponent(runId)}/clarify`, accepted, { promptId, answer });

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

/** Per-chat usage as the assistant backend reported it. null means it reported nothing, which is not zero. */
export interface ChatUsage {
  tokens: { input: number; output: number; cacheRead: number; cacheWrite: number; total: number } | null;
  finishedRuns: number;
  reportedRuns: number;
  costIDR: number | null;
  unpricedRuns: number;
  /** Replies whose model the assistant did not name (recovered by polling): no cost can be estimated for them. */
  unknownModelRuns: number;
  /** Per-reply usage, oldest first; tokens null means that reply reported none. */
  turns: { runId: string; status: string; tokens: { input: number; output: number; total: number } | null }[];
  turnsTruncated: boolean;
  priceListDate: string;
  windowTokens: number | null;
  /** limit null: the user set none. used null: no finished reply reported usage (unknown, not zero). enforced: a limit is set and usage is known. */
  budget: { limit: number | null; used: number | null; enforced: boolean; reached: boolean; basis: string };
}

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);

function parseTurns(v: unknown): ChatUsage["turns"] {
  if (!Array.isArray(v)) return [];
  const out: ChatUsage["turns"] = [];
  for (const raw of v) {
    const t = obj(raw);
    if (!t) continue;
    const k = obj(t.tokens);
    const input = k ? num(k.input) : null;
    const output = k ? num(k.output) : null;
    const total = k ? num(k.total) : null;
    out.push({ runId: str(t.runId), status: str(t.status), tokens: input !== null && output !== null && total !== null ? { input, output, total } : null });
  }
  return out;
}

export function parseUsage(v: unknown): ChatUsage | null {
  const o = obj(v);
  const runs = obj(o?.runs);
  const cost = obj(o?.cost);
  const ctx = obj(o?.context);
  const b = obj(o?.budget);
  if (!o || !runs || !cost || !ctx || !b) return null;
  const limit = b.limitTokens === null ? null : num(b.limitTokens);
  const used = b.usedTokens === null ? null : num(b.usedTokens);
  if ((b.limitTokens !== null && limit === null) || (b.usedTokens !== null && used === null)) return null;
  const finished = num(runs.finished);
  const reported = num(runs.reported);
  if (finished === null || reported === null) return null;
  const t = obj(o.tokens);
  const tokens = t && num(t.input) !== null && num(t.output) !== null && num(t.total) !== null ? { input: num(t.input)!, output: num(t.output)!, cacheRead: num(t.cacheRead) ?? 0, cacheWrite: num(t.cacheWrite) ?? 0, total: num(t.total)! } : null;
  return {
    tokens,
    finishedRuns: finished,
    reportedRuns: reported,
    costIDR: num(cost.amount),
    unpricedRuns: num(cost.unpricedRuns) ?? 0,
    unknownModelRuns: num(cost.unknownModelRuns) ?? 0,
    turns: parseTurns(o.turns),
    turnsTruncated: o.turnsTruncated === true,
    priceListDate: str(cost.priceListDate),
    windowTokens: num(ctx.windowTokens),
    budget: { limit, used, enforced: b.enforced === true, reached: b.reached === true, basis: str(b.basis) },
  };
}

export const getUsage = (chatId: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}/usage`, parseUsage);

const fmt = (n: number) => n.toLocaleString("en-US");

/** The words shown for a chat's usage. Unknown is "—", cost is always labelled an estimate, the budget behaviour is stated. */
export function describeUsage(u: ChatUsage): string[] {
  const lines: string[] = [];
  lines.push(u.tokens ? `Tokens: ${fmt(u.tokens.total)} (${fmt(u.tokens.input)} in, ${fmt(u.tokens.output)} out)` : "Tokens: — (none reported)");
  lines.push(u.costIDR !== null ? `Estimated cost: about Rp ${fmt(Math.round(u.costIDR))}, from the provider price list of ${u.priceListDate}; not a bill` : "Estimated cost: — (no price for the model)");
  lines.push(`Context in use: — (not reported)${u.windowTokens !== null ? `; model window ${fmt(u.windowTokens)} tokens` : ""}`);
  if (u.unknownModelRuns > 0) lines.push(`${u.unknownModelRuns} ${u.unknownModelRuns === 1 ? "reply" : "replies"} did not report which model answered (recovered after a disconnect), so no cost is estimated for ${u.unknownModelRuns === 1 ? "it" : "them"}`);
  if (u.unpricedRuns > u.unknownModelRuns) lines.push(`${u.unpricedRuns - u.unknownModelRuns} ${u.unpricedRuns - u.unknownModelRuns === 1 ? "reply was" : "replies were"} from a model with no known price and ${u.unpricedRuns - u.unknownModelRuns === 1 ? "is" : "are"} left out of the estimate`);
  if (u.finishedRuns > u.reportedRuns) lines.push(`${u.finishedRuns - u.reportedRuns} finished ${u.finishedRuns - u.reportedRuns === 1 ? "reply" : "replies"} reported no usage and ${u.finishedRuns - u.reportedRuns === 1 ? "is" : "are"} not counted`);
  lines.push(...describeBudget(u));
  return lines;
}

const BASIS = "counts input + output tokens reported across all your chats, and input includes cache reads";

/** What the budget line says. No limit is the default; an unknown usage is never printed as zero. */
export function describeBudget(u: ChatUsage): string[] {
  const { limit, used, reached } = u.budget;
  if (limit === null) return ["Token limit: none set. You can set one below; it is opt-in and counts all your chats"];
  if (used === null) return [`Token limit: ${fmt(limit)} across all your chats, but usage is unknown (nothing reported yet), so the limit is not being applied`];
  const partial = u.finishedRuns > u.reportedRuns ? "; replies that reported no usage are not counted, so the limit may be reached later than shown" : "";
  if (reached) return [`Token limit reached (${fmt(used)} of ${fmt(limit)}): this chat takes no new messages until you raise or remove the limit. It ${BASIS}`];
  return [`Token limit: ${fmt(used)} of ${fmt(limit)} across all your chats (a new chat does not reset it). It ${BASIS}. At the limit the chat stops taking new messages (a reply already running finishes)${partial}`];
}

const parseBudget = (v: unknown): { limit: number | null; min: number; max: number } | null => {
  const o = obj(v);
  if (!o) return null;
  const limit = o.limitTokens === null ? null : num(o.limitTokens);
  const min = num(o.minTokens);
  const max = num(o.maxTokens);
  if ((o.limitTokens !== null && limit === null) || min === null || max === null) return null;
  return { limit, min, max };
};

export const putBudget = (limitTokens: number | null) => call("PUT", "/api/me/budget", parseBudget, { limitTokens });

export const MIN_BUDGET = 10_000;
export const MAX_BUDGET = 1_000_000_000;

/** Reads the limit field: "" is no limit (null); a whole number in range is that number; anything else is undefined and is never sent. */
export function parseLimitInput(text: string): number | null | undefined {
  const t = text.replace(/[,\s]/g, "");
  if (t === "") return null;
  if (!/^\d+$/.test(t)) return undefined;
  const n = Number(t);
  return Number.isSafeInteger(n) && n >= MIN_BUDGET && n <= MAX_BUDGET ? n : undefined;
}

/** One line per reply: its own tokens, or a dash when it reported none. */
export function describeTurns(u: ChatUsage): string[] {
  return u.turns.map((t, i) => `Reply ${i + 1}: ${t.tokens ? `${fmt(t.tokens.total)} tokens (${fmt(t.tokens.input)} in, ${fmt(t.tokens.output)} out)` : "— (no usage reported)"}`);
}

// ---- model picker and provider quotas (LLM-*, CTX-004) -------------------------------

export interface ModelChoice {
  provider: string;
  model: string;
  reasoningEffort: string;
  serviceTier: string;
}

export interface ModelEntry {
  id: string;
  available: boolean;
  /** null = the assistant did not say. Never read as false. */
  reasoning: boolean | null;
  priority: boolean | null;
  price: { input: string; output: string; cache: string; free: boolean } | null;
}

export interface QuotaWindow {
  label: string;
  usedPercent: number;
  resetsAt: string;
  scope: string;
}

export interface ProviderEntry {
  slug: string;
  name: string;
  available: boolean;
  reason: string;
  models: ModelEntry[];
  /** null windows = PROVIDER_USAGE_UNAVAILABLE: shown as unavailable, not as 0%. */
  quota: QuotaWindow[] | null;
}

export interface ModelCatalog {
  fetchedAt: string;
  default: { provider: string; model: string } | null;
  providers: ProviderEntry[];
  reasoningEfforts: string[];
}

const bool = (v: unknown): boolean | null => (typeof v === "boolean" ? v : null);
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

export function parseModels(v: unknown): ModelCatalog | null {
  const o = obj(v);
  if (!o || !Array.isArray(o.providers)) return null;
  const d = obj(o.default);
  return {
    fetchedAt: str(o.fetchedAt),
    default: d && str(d.model) ? { provider: str(d.provider), model: str(d.model) } : null,
    reasoningEfforts: arr(o.reasoningEfforts).filter((x): x is string => typeof x === "string"),
    providers: arr(o.providers).flatMap((p) => {
      const po = obj(p);
      if (!po || !str(po.slug)) return [];
      const q = obj(po.quota);
      const windows = q && q.state === "AVAILABLE" ? arr(q.windows).flatMap((w) => {
        const wo = obj(w);
        return wo && typeof wo.usedPercent === "number" ? [{ label: str(wo.label), usedPercent: wo.usedPercent, resetsAt: str(wo.resetsAt), scope: str(wo.scope) }] : [];
      }) : [];
      return [{
        slug: str(po.slug),
        name: str(po.name) || str(po.slug),
        available: po.available === true,
        reason: str(po.reason),
        quota: windows.length > 0 ? windows : null,
        models: arr(po.models).flatMap((m) => {
          const mo = obj(m);
          if (!mo || !str(mo.id)) return [];
          const pr = obj(mo.price);
          return [{
            id: str(mo.id),
            available: po.available === true && mo.available === true,
            reasoning: bool(mo.reasoning),
            priority: bool(mo.serviceTier),
            price: pr ? { input: str(pr.input), output: str(pr.output), cache: str(pr.cache), free: pr.free === true } : null,
          }];
        }),
      }];
    }),
  };
}

export const getModels = (refresh = false) => call("GET", refresh ? "/api/models?refresh=1" : "/api/models", parseModels);

export function parseModelChoice(v: unknown): ModelChoice | null {
  const o = obj(v);
  return o && str(o.model) ? { provider: str(o.provider), model: str(o.model), reasoningEffort: str(o.reasoningEffort), serviceTier: str(o.serviceTier) } : null;
}

/** The chat's saved choice, or null for the profile default. A failed read is an error, not "default". */
export const getChatModel = (chatId: string) =>
  call("GET", `/api/chats/${encodeURIComponent(chatId)}`, (b): { choice: ModelChoice | null } | null => {
    const o = obj(b);
    return o ? { choice: parseModelChoice(o.model) } : null;
  });

export const putChatModel = (chatId: string, body: { reset: true } | { provider: string; model: string; reasoningEffort?: string; serviceTier?: string }) =>
  call("PUT", `/api/chats/${encodeURIComponent(chatId)}/model`, (b): { choice: ModelChoice | null } | null => {
    const o = obj(b);
    return o ? { choice: parseModelChoice(o.model) } : null;
  }, body);

/** Words for a provider's quota windows; absence is stated, never shown as 0%. */
export function describeQuota(p: ProviderEntry): string[] {
  if (!p.quota) return ["Provider usage: unavailable (the assistant reported none)"];
  return p.quota.map((w) => `${w.label || "Window"}${w.scope ? ` (${w.scope})` : ""}: ${Math.round(w.usedPercent * 10) / 10}% used${w.resetsAt ? `, resets ${w.resetsAt}` : ""}`);
}

// ---- capability discovery -------------------------------------------------------
// GET /api/capabilities is read live from the assistant backend. Only the exact word
// "available" ever means available: any other value, a missing field or a row we cannot
// read is shown as unsupported, so nothing the server did not confirm is offered.

export interface CapabilityRow {
  id: string;
  label: string;
  available: boolean;
  reason: string;
}

export interface ToolsetRow {
  name: string;
  available: boolean;
  reason: string;
  tools: string[];
}

export interface Inventory {
  capabilities: CapabilityRow[];
  toolsets: ToolsetRow[];
  providers: { slug: string; name: string; models: string[]; totalModels: number }[];
  skills: { name: string; description: string; category: string }[];
}

function rows<T>(v: unknown, one: (o: Record<string, unknown>) => T | null): T[] | null {
  if (!Array.isArray(v)) return null;
  const out: T[] = [];
  for (const item of v) {
    const o = obj(item);
    const r = o && one(o);
    if (!r) return null; // an unreadable row makes the answer unreadable, never a shorter list
    out.push(r);
  }
  return out;
}

const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

export function parseInventory(v: unknown): Inventory | null {
  const o = obj(v);
  if (!o) return null;
  const capabilities = rows(o.capabilities, (r) => (str(r.id) ? { id: str(r.id), label: str(r.label) || str(r.id), available: r.status === "available", reason: str(r.reason) } : null));
  const toolsets = rows(o.toolsets, (r) => (str(r.name) ? { name: str(r.name), available: r.status === "available", reason: str(r.reason), tools: strings(r.tools) } : null));
  const providers = rows(o.providers, (r) => (str(r.slug) ? { slug: str(r.slug), name: str(r.name) || str(r.slug), models: strings(r.models), totalModels: typeof r.totalModels === "number" ? r.totalModels : 0 } : null));
  const skills = rows(o.skills, (r) => (str(r.name) ? { name: str(r.name), description: str(r.description), category: str(r.category) } : null));
  return capabilities && toolsets && providers && skills ? { capabilities, toolsets, providers, skills } : null;
}

export const getCapabilities = (signal?: AbortSignal) => call("GET", "/api/capabilities", parseInventory, undefined, undefined, signal);

/** True only when the server reported this capability as available. Unknown ids are not offered. */
export const offers = (inv: Inventory | null, id: string): boolean => inv?.capabilities.find((c) => c.id === id)?.available === true;
