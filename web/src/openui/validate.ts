import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";

// Gate in front of the renderer for a finished reply (PRD §7.4), over OpenUI's own chat library. The parser
// checks every component and argument against the library's schema; on top of that a block is rendered only if
// it parses with no error, is complete, has the library's root, holds no query, mutation or state (no tools are
// wired to it), and stays within the size limits below. Anything else is shown as its source (Block.tsx).
// While the reply streams the program is partial, so completeness, parse errors and dangling references wait for
// the end; everything else (root, no query/mutation/state, known components, every limit) is checked on each update
// before anything is rendered. Those failures only grow with more text, so a partial program that fails one is
// final: it is shown as its source at once.

export const MAX_NODES = 600;
export const MAX_DEPTH = 12;
export const MAX_STATEMENTS = 300;
export const MAX_ARRAY = 200;
export const MAX_TEXT = 20_000;

export type Verdict = { ok: true } | { ok: false; reason: string };

let parser: ReturnType<typeof createParser> | undefined;

interface Node {
  type?: unknown;
  typeName?: unknown;
  props?: Record<string, unknown>;
}

export function check(source: string, partial = false): Verdict {
  // Antares' own first component set (v1, before native OpenUI) had a Stack root, which this library lacks (the
  // parser drops it); such a block is shown as source, labelled as such.
  if (/^\s*root\s*=\s*Stack\s*\(/.test(source)) return { ok: false, reason: "made by an earlier version of rich replies" };
  parser ??= createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root);
  let r;
  try {
    r = parser.parse(source);
  } catch {
    return { ok: false, reason: "parse error" };
  }
  const m = r.meta;
  if (!r.root) return partial ? { ok: true } : { ok: false, reason: "no root" };
  if (r.root.typeName !== openuiChatLibrary.root) return { ok: false, reason: `root must be ${openuiChatLibrary.root}` };
  if (r.rootExpression) return { ok: false, reason: "computed root" };
  // An unknown component is dropped by the parser and only reported here; more text cannot fix it.
  const unknown = m.errors.find((e) => e.code === "unknown-component");
  if (unknown) return { ok: false, reason: "invalid: unknown-component" };
  if (!partial) {
    if (m.incomplete) return { ok: false, reason: "incomplete" };
    if (m.errors.length > 0) return { ok: false, reason: `invalid: ${m.errors[0].code}` };
    if (m.unresolved.length > 0) return { ok: false, reason: "dangling reference" };
  }
  if (m.statementCount > MAX_STATEMENTS) return { ok: false, reason: "too many statements" };
  if (r.queryStatements.length > 0 || r.mutationStatements.length > 0) return { ok: false, reason: "queries and mutations are not allowed" };
  if (Object.keys(r.stateDeclarations).length > 0) return { ok: false, reason: "state is not allowed" };

  const known = new Set(Object.keys(openuiChatLibrary.components));
  let nodes = 0;
  const visit = (v: unknown, depth: number): string | null => {
    if (typeof v === "string") return v.length > MAX_TEXT ? "text too long" : null;
    if (Array.isArray(v)) {
      if (v.length > MAX_ARRAY) return "list too long";
      for (const x of v) {
        const bad = visit(x, depth);
        if (bad) return bad;
      }
      return null;
    }
    if (typeof v !== "object" || v === null) return null;
    const n = v as Node;
    if (n.type === "element") {
      if (!known.has(String(n.typeName))) return `component ${String(n.typeName)}`;
      if (++nodes > MAX_NODES) return "too many components";
      if (depth > MAX_DEPTH) return "nested too deeply";
      for (const val of Object.values(n.props ?? {})) {
        const inner = visit(val, depth + 1);
        if (inner) return inner;
      }
      return null;
    }
    for (const val of Object.values(v as Record<string, unknown>)) {
      const inner = visit(val, depth);
      if (inner) return inner;
    }
    return null;
  };
  const bad = visit(r.root, 1);
  return bad ? { ok: false, reason: bad } : { ok: true };
}
