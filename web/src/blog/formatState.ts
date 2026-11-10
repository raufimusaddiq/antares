import { useEditorState } from "@tiptap/react";
import type { Editor } from "@tiptap/react";
import type { Node as PMNode } from "@tiptap/pm/model";

// What the formatting buttons show (specs/006-ai-editor-blog, US1): for each effect, whether the whole selection has it (on), none of it (off), or
// part of it (mixed). The buttons read this through useEditorState, which re-renders on every selection or content change; reading
// editor.isActive() while rendering, as before, left the buttons showing the state of the last render.

export type Tri = "on" | "off" | "mixed";

export interface FormatState {
  bold: Tri;
  italic: Tri;
  heading2: Tri;
  heading3: Tri;
  bullets: Tri;
  numbers: Tri;
  quote: Tri;
  code: Tri;
  link: Tri;
}

const OFF: FormatState = { bold: "off", italic: "off", heading2: "off", heading3: "off", bullets: "off", numbers: "off", quote: "off", code: "off", link: "off" };

const tri = (have: number, total: number): Tri => (total === 0 || have === 0 ? "off" : have === total ? "on" : "mixed");

/** Marks: over the text of the selection (or the stored marks / the marks at a cursor). */
function markState(editor: Editor, name: string): Tri {
  const { state } = editor;
  const type = state.schema.marks[name];
  if (!type) return "off";
  const { from, to, empty, $from } = state.selection;
  if (empty) return (state.storedMarks ?? $from.marks()).some((m) => m.type === type) ? "on" : "off";
  let total = 0;
  let have = 0;
  state.doc.nodesBetween(from, to, (node: PMNode) => {
    if (node.isText && node.text) {
      total++;
      if (node.marks.some((m) => m.type === type)) have++;
    }
  });
  return tri(have, total);
}

/** Block types: over the text blocks the selection touches (a cursor touches one). */
function blockState(editor: Editor, test: (textblock: PMNode, ancestors: PMNode[]) => boolean): Tri {
  const { state } = editor;
  const { from, to } = state.selection;
  let total = 0;
  let have = 0;
  state.doc.nodesBetween(from, to, (node: PMNode, pos: number) => {
    if (!node.isTextblock) return true;
    total++;
    const $p = state.doc.resolve(pos + 1);
    const ancestors: PMNode[] = [];
    for (let d = $p.depth; d >= 0; d--) ancestors.push($p.node(d));
    if (test(node, ancestors)) have++;
    return false;
  });
  return tri(have, total);
}

export function computeFormat(editor: Editor | null): FormatState {
  if (!editor || editor.isDestroyed) return OFF;
  const inside = (name: string) => (_t: PMNode, a: PMNode[]) => a.some((n) => n.type.name === name);
  return {
    bold: markState(editor, "bold"),
    italic: markState(editor, "italic"),
    link: markState(editor, "link"),
    code: (() => {
      const block = blockState(editor, (t) => t.type.name === "codeBlock");
      return block !== "off" ? block : markState(editor, "code");
    })(),
    heading2: blockState(editor, (t) => t.type.name === "heading" && t.attrs.level === 2),
    heading3: blockState(editor, (t) => t.type.name === "heading" && t.attrs.level === 3),
    bullets: blockState(editor, inside("bulletList")),
    numbers: blockState(editor, inside("orderedList")),
    quote: blockState(editor, inside("blockquote")),
  };
}

const same = (a: FormatState, b: FormatState | null) => b !== null && (Object.keys(a) as (keyof FormatState)[]).every((k) => a[k] === b[k]);

/** The toolbar's state, re-read after every transaction (selection moves, typing, commands). */
export function useFormatState(editor: Editor | null): FormatState {
  return useEditorState({ editor, selector: ({ editor: e }) => computeFormat(e), equalityFn: same }) ?? OFF;
}

/** aria-pressed for a toggle button. */
export const pressed = (t: Tri): boolean | "mixed" => (t === "mixed" ? "mixed" : t === "on");
