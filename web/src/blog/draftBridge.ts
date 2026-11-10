import type { Editor } from "@tiptap/react";
import { closeHistory } from "@tiptap/pm/history";
import type { Node as PMNode } from "@tiptap/pm/model";
import { partAnchors, relocate } from "./anchors";
import type { EditSuggestion } from "./types";

// The only place a suggestion changes the draft (specs/006-ai-editor-blog): the editor in the browser applies an accepted suggestion as one ordinary,
// undoable change, and autosave saves it like any typing, so the draft has one writer. The server records the decision only.

export type Applied = "applied" | "not_applicable";

export interface DraftBridge {
  /** Whether the passage the suggestion is about is still there. */
  applicable(e: EditSuggestion): boolean;
  /** Applies it as one change. "not_applicable" when the passage is gone or changed; nothing is touched then. */
  apply(e: EditSuggestion): Applied;
  /** Undoes the suggestion that was applied last, only while nothing else has changed since. */
  canUndo(editId: string): boolean;
  undo(editId: string): boolean;
}

type Target = { kind: "range"; from: number; to: number } | { kind: "after"; pos: number };

function partStart(doc: PMNode, index: number): number {
  let pos = 0;
  for (let i = 0; i < index; i++) pos += doc.child(i).nodeSize;
  return pos;
}

/** The first place in a text block where `needle` sits inside one run of text (a line break or an image ends a run). */
function findInTextblock(block: PMNode, blockPos: number, needle: string): { from: number; to: number } | null {
  const runs: { text: string; from: number }[] = [];
  let text = "";
  let from = -1;
  const flush = () => {
    if (from >= 0) runs.push({ text, from });
    text = "";
    from = -1;
  };
  block.forEach((child, offset) => {
    if (child.isText) {
      if (from < 0) from = blockPos + 1 + offset;
      text += child.text ?? "";
    } else flush();
  });
  flush();
  for (const r of runs) {
    const i = r.text.indexOf(needle);
    if (i >= 0) return { from: r.from + i, to: r.from + i + needle.length };
  }
  return null;
}

function target(doc: PMNode, e: EditSuggestion): Target | null {
  const index = relocate(partAnchors(doc), e.anchor, e.part);
  if (index < 0) return null;
  const start = partStart(doc, index);
  const top = doc.child(index);
  if (e.kind === "insert") return { kind: "after", pos: start + top.nodeSize };
  let hit: { from: number; to: number } | null = null;
  doc.nodesBetween(start, start + top.nodeSize, (node, pos) => {
    if (hit || !node.isTextblock) return !hit;
    hit = findInTextblock(node, pos, e.original);
    return false;
  });
  return hit ? { kind: "range", ...(hit as { from: number; to: number }) } : null;
}

export function makeBridge(editor: Editor): DraftBridge {
  let last: { id: string; doc: PMNode } | null = null;
  return {
    applicable: (e) => !editor.isDestroyed && target(editor.state.doc, e) !== null,
    apply(e) {
      if (editor.isDestroyed) return "not_applicable";
      const t = target(editor.state.doc, e);
      if (!t) return "not_applicable";
      const tr = editor.state.tr;
      closeHistory(tr); // one accept is one step of undo, apart from what was typed before and after
      if (t.kind === "range") tr.insertText(e.proposed, t.from, t.to);
      else {
        const { schema } = editor.state;
        const paragraphs = e.proposed.split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
        tr.insert(t.pos, paragraphs.map((p) => schema.nodes.paragraph.create(null, schema.text(p))));
      }
      editor.view.dispatch(tr);
      last = { id: e.id, doc: editor.state.doc };
      return "applied";
    },
    canUndo: (id) => !editor.isDestroyed && last !== null && last.id === id && editor.state.doc.eq(last.doc),
    undo(id) {
      if (editor.isDestroyed || !last || last.id !== id || !editor.state.doc.eq(last.doc)) return false;
      last = null;
      return editor.commands.undo();
    },
  };
}
