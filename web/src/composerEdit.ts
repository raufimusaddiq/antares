// Markdown shortcuts for the composer: pure text edits, no editor engine.

export type Edit = { value: string; start: number; end: number };

const WRAP: Record<string, string> = { b: "**", i: "*", e: "`" };

/** Ctrl/Cmd+B, I or E: wrap the selection (or an empty pair around the caret) in Markdown markers. */
export function wrapSelection(value: string, start: number, end: number, key: string): Edit | null {
  const m = WRAP[key.toLowerCase()];
  if (!m) return null;
  const sel = value.slice(start, end);
  return { value: value.slice(0, start) + m + sel + m + value.slice(end), start: start + m.length, end: end + m.length };
}

/** True when the caret sits inside an unclosed ``` fence. */
export function inFence(value: string, pos: number): boolean {
  return (value.slice(0, pos).match(/^```/gm) ?? []).length % 2 === 1;
}

/**
 * Tab inside a fenced code block indents by two spaces; elsewhere Tab keeps its normal focus job.
 * With a selection it does nothing: replacing the range would delete the selected text.
 */
export function indentInFence(value: string, start: number, end: number): Edit | null {
  if (start !== end || !inFence(value, start)) return null;
  return { value: value.slice(0, start) + "  " + value.slice(start), start: start + 2, end: start + 2 };
}

/** Start of the line holding `pos`, and the end of the line holding `pos`. */
const lineStart = (value: string, pos: number) => value.lastIndexOf("\n", pos - 1) + 1;
const lineEnd = (value: string, pos: number) => {
  const i = value.indexOf("\n", pos);
  return i === -1 ? value.length : i;
};

/**
 * Ctrl/Cmd+Shift+8: toggle a "- " bullet on every line the selection touches. If all of them already
 * have one it is removed. Not offered inside a code fence, where a bullet would be code text.
 */
export function toggleList(value: string, start: number, end: number): Edit | null {
  if (inFence(value, start)) return null;
  const from = lineStart(value, start);
  // A selection ending at the start of a line does not touch that line.
  const to = lineEnd(value, end > start && value[end - 1] === "\n" ? end - 1 : end);
  const lines = value.slice(from, to).split("\n");
  const all = lines.every((l) => l.startsWith("- "));
  const out = lines.map((l) => (all ? l.slice(2) : "- " + l));
  const delta = all ? -2 : 2;
  return {
    value: value.slice(0, from) + out.join("\n") + value.slice(to),
    start: Math.max(from, start + delta),
    end: Math.max(from, end + delta * lines.length),
  };
}

/**
 * Ctrl/Cmd+Shift+E: put the selection (or an empty block, caret inside) in a ``` fence on its own lines.
 * Not offered inside a fence: a nested fence would close the block it sits in.
 */
export function fenceSelection(value: string, start: number, end: number): Edit | null {
  if (inFence(value, start)) return null;
  const open = (start === 0 || value[start - 1] === "\n" ? "" : "\n") + "```\n";
  const close = "\n```" + (end >= value.length || value[end] === "\n" ? "" : "\n");
  const at = start + open.length;
  return { value: value.slice(0, start) + open + value.slice(start, end) + close + value.slice(end), start: at, end: at + (end - start) };
}
