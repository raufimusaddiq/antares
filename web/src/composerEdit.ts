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
