import type { Node as PMNode } from "@tiptap/pm/model";

// Anchors (specs/006-ai-editor-blog, research R6): the text of a part's first text block, whitespace collapsed, at most 120 characters. A suggestion keeps the
// anchor of the part it was made for, and is found again in the draft as it is now. The same rule is written in internal/blog/anchors.go;
// internal/blog/testdata/anchors.json is read by both tests so the two cannot drift apart.

export const ANCHOR_RUNES = 120;
const TEXT_BLOCKS = new Set(["paragraph", "heading", "codeBlock"]);

function firstTextBlock(n: PMNode): string | null {
  if (TEXT_BLOCKS.has(n.type.name)) return n.textContent;
  let found: string | null = null;
  n.forEach((c) => {
    if (found === null) found = firstTextBlock(c);
  });
  return found;
}

export function anchorOfNode(n: PMNode): string {
  const collapsed = (firstTextBlock(n) ?? "").split(/\s+/).filter(Boolean).join(" ");
  const runes = Array.from(collapsed);
  return runes.length > ANCHOR_RUNES ? runes.slice(0, ANCHOR_RUNES).join("").trim() : collapsed;
}

export function partAnchors(doc: PMNode): string[] {
  const out: string[] = [];
  doc.forEach((n) => out.push(anchorOfNode(n)));
  return out;
}

/** The part with this anchor nearest to `hint` (the earlier on a tie), or -1. An empty anchor stays where it was when a part with no text is still there. */
export function relocate(anchors: string[], anchor: string, hint: number): number {
  if (anchor === "") return hint >= 0 && hint < anchors.length && anchors[hint] === "" ? hint : -1;
  let best = -1;
  let bestDist = 0;
  anchors.forEach((a, i) => {
    if (a !== anchor) return;
    const d = Math.abs(i - hint);
    if (best < 0 || d < bestDist) {
      best = i;
      bestDist = d;
    }
  });
  return best;
}
