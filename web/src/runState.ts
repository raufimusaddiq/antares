// The state of one run as the browser shows it. Pure: events in, state out.
//
// The rule this file exists to keep: a run is shown as completed only when the
// server said it completed. Failed, cancelled and timed-out runs, and runs that
// lost their connection, are each shown as exactly that, and a terminal state
// never changes afterwards.

export type Phase = "streaming" | "reconnecting" | "completed" | "failed" | "cancelled" | "disconnected" | "detached";

export interface Tool {
  id: string;
  label: string;
  done: boolean;
  summary: string;
}

export interface LiveRun {
  runId: string;
  phase: Phase;
  text: string;
  tools: Tool[];
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
  | { type: "tool.completed"; id: string; summary: string }
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
  "run.completed",
  "run.failed",
  "run.cancelled",
  "run.disconnected",
] as const;

export function newRun(runId: string): LiveRun {
  return { runId, phase: "streaming", text: "", tools: [], code: "", timedOut: false, maybeStillRunning: false };
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
    case "tool.completed":
      return { type: "tool.completed", id: s("toolCallId"), summary: s("summary") };
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
      // The assistant may replay from the start: drop what we have; the final text replaces it.
      return { ...state, phase: "streaming", text: "" };
    case "assistant.delta":
      return state.text.length >= MAX_TEXT ? state : { ...state, phase: "streaming", text: state.text + ev.text };
    case "tool.started":
      return { ...state, tools: [...state.tools.filter((t) => t.id === "" || t.id !== ev.id), { id: ev.id, label: ev.label, done: false, summary: "" }] };
    case "tool.completed":
      return {
        ...state,
        tools: state.tools.map((t) => (t.id !== "" && t.id === ev.id ? { ...t, done: true, summary: ev.summary } : t)),
      };
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
