// The browser's whole view of the chat backend. It sends cookies and message
// text only: never a profile, key, route prefix or session id (the server picks
// those). Every failure comes back as a code from the fixed API vocabulary.

export interface ChatSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  activeRunId: string;
}

export interface Turn {
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

export function parseTurns(v: unknown): Turn[] | null {
  const o = obj(v);
  if (!o || !Array.isArray(o.messages)) return null;
  const out: Turn[] = [];
  for (const m of o.messages) {
    const mo = obj(m);
    if (mo && (mo.role === "user" || mo.role === "assistant")) out.push({ role: mo.role, text: str(mo.text) });
  }
  return out;
}

export const listChats = () =>
  call("GET", "/api/chats", (b) => {
    const o = obj(b);
    if (!o || !Array.isArray(o.chats)) return null;
    return o.chats.map(parseChat).filter((c): c is ChatSummary => c !== null);
  });

export const createChat = () => call("POST", "/api/chats", parseChat, {});

export const getMessages = (chatId: string) => call("GET", `/api/chats/${encodeURIComponent(chatId)}/messages`, parseTurns);

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
