// The state of one run as the browser shows it. Pure: events in, state out.
//
// The rule this file exists to keep: a run is shown as completed only when the
// server said it completed. Failed, cancelled and timed-out runs, and runs that
// lost their connection, are each shown as exactly that, and a terminal state
// never changes afterwards.

export type Phase = "streaming" | "reconnecting" | "completed" | "failed" | "cancelled" | "disconnected" | "detached";

/**
 * One tool call as far as the server reported it. "untracked" is a start or an end the stream could not tie to
 * the other (no id, or an end with no matching start): its outcome is not known, so it is never shown as done.
 */
export type ToolState = "running" | "done" | "failed" | "untracked";

export interface Tool {
  id: string;
  label: string;
  state: ToolState;
  summary: string;
  durationMs: number | null;
}

/** At most this many tool rows are kept; older finished ones are folded into counts so a noisy stream stays bounded. */
export const MAX_TOOL_ROWS = 40;

export interface FoldedTools {
  count: number;
  failed: number;
  /** Folded rows whose outcome was never known: counted apart, never as finished. */
  untracked: number;
}

export interface LiveRun {
  runId: string;
  phase: Phase;
  text: string;
  tools: Tool[];
  /** Finished tool calls no longer listed one by one (MAX_TOOL_ROWS). */
  folded: FoldedTools;
  /** The assistant reported a reasoning stage and no answer text has arrived since. Never its content. */
  thinking: boolean;
  code: string; // failure code, when failed
  timedOut: boolean;
  /** Set after a cancel when the assistant did not confirm the stop. */
  maybeStillRunning: boolean;
}

export type RunEvent =
  | { type: "run.started" }
  | { type: "run.resumed" }
  | { type: "assistant.delta"; text: string }
  | { type: "tool.started"; id: string; label: string }
  | { type: "tool.completed"; id: string; summary: string; failed: boolean; durationMs: number | null }
  | { type: "run.thinking" }
  | { type: "run.completed"; output: string; status: string }
  | { type: "run.failed"; code: string; status: string }
  | { type: "run.cancelled" }
  | { type: "run.disconnected" }
  | { type: "stream.closed" }
  | { type: "stream.lost" }
  | { type: "stream.open" };

export const EVENT_NAMES = [
  "run.started",
  "run.resumed",
  "assistant.delta",
  "tool.started",
  "tool.completed",
  "run.thinking",
  "run.completed",
  "run.failed",
  "run.cancelled",
  "run.disconnected",
] as const;

export function newRun(runId: string): LiveRun {
  return { runId, phase: "streaming", text: "", tools: [], folded: { count: 0, failed: 0, untracked: 0 }, thinking: false, code: "", timedOut: false, maybeStillRunning: false };
}

export const isTerminal = (p: Phase) => p === "completed" || p === "failed" || p === "cancelled";

const MAX_TEXT = 1_000_000;

/** Parse one SSE frame. Anything not understood is null: dropped, never guessed. */
export function parseEvent(name: string, data: string): RunEvent | null {
  let d: Record<string, unknown> = {};
  try {
    const v: unknown = JSON.parse(data);
    if (typeof v === "object" && v !== null && !Array.isArray(v)) d = v as Record<string, unknown>;
  } catch {
    return null;
  }
  const s = (k: string) => (typeof d[k] === "string" ? (d[k] as string) : "");
  switch (name) {
    case "run.started":
      return { type: "run.started" };
    case "run.resumed":
      return { type: "run.resumed" };
    case "assistant.delta":
      return typeof d.text === "string" ? { type: "assistant.delta", text: d.text } : null;
    case "tool.started":
      return { type: "tool.started", id: s("toolCallId"), label: s("label") };
    case "tool.completed": {
      const ms = typeof d.durationMs === "number" && Number.isFinite(d.durationMs) && d.durationMs >= 0 ? d.durationMs : null;
      return { type: "tool.completed", id: s("toolCallId"), summary: s("summary"), failed: d.failed === true, durationMs: ms };
    }
    case "run.thinking":
      return { type: "run.thinking" };
    case "run.completed":
      // Only an explicit COMPLETED status counts as completion.
      return s("status") === "COMPLETED" ? { type: "run.completed", output: s("output"), status: s("status") } : null;
    case "run.failed":
      return { type: "run.failed", code: s("code"), status: s("status") };
    case "run.cancelled":
      return { type: "run.cancelled" };
    case "run.disconnected":
      return { type: "run.disconnected" };
  }
  return null;
}

export function reduce(state: LiveRun, ev: RunEvent): LiveRun {
  if (isTerminal(state.phase)) return state; // a finished run does not change
  switch (ev.type) {
    case "run.started":
    case "stream.open":
      return { ...state, phase: "streaming" };
    case "run.resumed":
      // The assistant may replay from the start: drop what we have, tools included, so a replay leaves no stale rows.
      return { ...state, phase: "streaming", text: "", tools: [], folded: { count: 0, failed: 0, untracked: 0 }, thinking: false };
    case "assistant.delta":
      return state.text.length >= MAX_TEXT ? state : { ...state, phase: "streaming", text: state.text + ev.text, thinking: false };
    case "run.thinking":
      return { ...state, thinking: true };
    case "tool.started": {
      // A start with no id can never be matched to its end: it is untracked from the first moment.
      const row: Tool = { id: ev.id, label: ev.label, state: ev.id === "" ? "untracked" : "running", summary: "", durationMs: null };
      // A second start for an id still running: which end belongs to which can no longer be told, so the earlier
      // row is untracked rather than silently replaced. Finished rows keep their outcome.
      const tools = state.tools.map((t): Tool => (ev.id !== "" && t.id === ev.id && t.state === "running" ? { ...t, state: "untracked" } : t));
      return bound({ ...state, thinking: false, tools: [...tools, row] });
    }
    case "tool.completed": {
      const i = ev.id === "" ? -1 : state.tools.findIndex((t) => t.id === ev.id && t.state === "running");
      const end: Pick<Tool, "summary" | "durationMs"> = { summary: ev.summary, durationMs: ev.durationMs };
      if (i < 0) {
        // An end with nothing to tie it to: shown, but as untracked, never as a finished call.
        return bound({ ...state, tools: [...state.tools, { id: ev.id, label: "", state: "untracked", ...end }] });
      }
      const tools = state.tools.slice();
      tools[i] = { ...tools[i], state: ev.failed ? "failed" : "done", ...end };
      return { ...state, tools };
    }
    case "run.completed":
      return { ...state, phase: "completed", text: ev.output !== "" ? ev.output : state.text };
    case "run.failed":
      return { ...state, phase: "failed", code: ev.code, timedOut: ev.status === "TIMED_OUT" };
    case "run.cancelled":
      return { ...state, phase: "cancelled" };
    case "run.disconnected":
      return { ...state, phase: "disconnected" };
    case "stream.closed":
      return { ...state, phase: "detached" };
    case "stream.lost":
      return { ...state, phase: "reconnecting" };
  }
}

/** Keeps at most MAX_TOOL_ROWS rows by folding the oldest finished ones into counts; running rows are never folded. */
function bound(state: LiveRun): LiveRun {
  if (state.tools.length <= MAX_TOOL_ROWS) return state;
  const tools = state.tools.slice();
  let { count, failed, untracked } = state.folded;
  while (tools.length > MAX_TOOL_ROWS) {
    const i = tools.findIndex((t) => t.state !== "running");
    if (i < 0) break;
    count++;
    if (tools[i].state === "failed") failed++;
    if (tools[i].state === "untracked") untracked++;
    tools.splice(i, 1);
  }
  return { ...state, tools, folded: { count, failed, untracked } };
}

/** The line standing for folded rows. Untracked ones are named as such, never counted as finished. */
export function describeFolded(f: FoldedTools): string {
  const known = f.count - f.untracked;
  const parts: string[] = [];
  if (known > 0) parts.push(`${known} earlier ${known === 1 ? "call" : "calls"} finished${f.failed > 0 ? ` (${f.failed} failed)` : ""}`);
  if (f.untracked > 0) parts.push(`${f.untracked} earlier ${f.untracked === 1 ? "call" : "calls"} not tracked`);
  return parts.join("; ");
}

/** What a tool row says. After the run ends, a call still marked running had no end reported. */
export function describeTool(t: Tool, phase: Phase): string {
  const took = t.durationMs === null ? "" : ` in ${t.durationMs < 1000 ? `${t.durationMs} ms` : `${(t.durationMs / 1000).toFixed(1)} s`}`;
  switch (t.state) {
    case "done":
      return `done${took}`;
    case "failed":
      return `failed${took}`;
    case "untracked":
      return "not tracked: its start and end could not be matched";
  }
  if (phase === "cancelled") return "may still be running: the reply was cancelled before it reported an end";
  if (isTerminal(phase)) return "no end reported";
  return "running";
}

/** The one line a run shows when it did not complete. Empty while streaming or completed. */
export function runNotice(r: LiveRun): string {
  switch (r.phase) {
    case "failed":
      if (r.timedOut) return "This response did not finish: the assistant timed out. It may or may not have started.";
      if (r.code === "CAPABILITY_UNAVAILABLE") return "This response did not finish: the assistant asked for an approval Antares cannot collect yet.";
      return "This response did not finish: the assistant reported an error.";
    case "cancelled":
      return r.maybeStillRunning
        ? "Cancelled. The assistant did not confirm it stopped and may still be working."
        : "Cancelled before the response finished.";
    case "detached":
      return "Live updates disconnected. The run is still active. Reconnect to resume updates.";
    case "disconnected":
      return "Connection to the assistant was lost. The response may still be in progress.";
    case "reconnecting":
      return "Reconnecting…";
    default:
      return "";
  }
}
