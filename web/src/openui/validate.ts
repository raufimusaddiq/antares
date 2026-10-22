import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { ALLOWED_COMPONENTS, library, MAX_TEXT } from "./library";

// Strict gate in front of the renderer (PRD §7.4). The OpenUI parser is
// permissive by design: it drops what it cannot place and renders the rest. For
// model output that is the wrong default, so a block is rendered only if it
// parses cleanly, is complete, and contains nothing outside the allowlist.
// Anything else is shown as inert source (see Block.tsx).

export const MAX_NODES = 300;
export const MAX_DEPTH = 6;
export const MAX_STATEMENTS = 100;
export const MAX_CHILDREN = 40;
export const MAX_ARRAY = 100;

export type Verdict = { ok: true } | { ok: false; reason: string };

// Which components may sit inside which (containers only; everything else is a leaf).
const CONTAINS: Record<string, readonly string[]> = {
  Stack: ["Text", "Heading", "Badge", "Metric", "BulletList", "Table", "Divider", "Card"],
  Card: ["Text", "Heading", "Badge", "Metric", "BulletList", "Table", "Divider"],
};

let parser: ReturnType<typeof createParser> | undefined;

interface Node {
  type?: unknown;
  typeName?: unknown;
  props?: Record<string, unknown>;
  hasDynamicProps?: unknown;
}

export function check(source: string): Verdict {
  parser ??= createParser(library.toJSONSchema(), library.root);
  let r;
  try {
    r = parser.parse(source);
  } catch {
    return { ok: false, reason: "parse error" };
  }
  const m = r.meta;
  if (!r.root) return { ok: false, reason: "no root" };
  if (r.root.typeName !== library.root) return { ok: false, reason: `root must be ${library.root}` };
  if (r.rootExpression) return { ok: false, reason: "computed root" };
  if (m.incomplete) return { ok: false, reason: "incomplete" };
  if (m.errors.length > 0) return { ok: false, reason: `invalid: ${m.errors[0].code}` };
  if (m.unresolved.length > 0 || m.orphaned.length > 0) return { ok: false, reason: "dangling reference" };
  if (m.statementCount > MAX_STATEMENTS) return { ok: false, reason: "too many statements" };
  if (r.queryStatements.length > 0 || r.mutationStatements.length > 0) return { ok: false, reason: "queries and mutations are not allowed" };
  if (Object.keys(r.stateDeclarations).length > 0) return { ok: false, reason: "state is not allowed" };

  let nodes = 0;
  const visit = (v: unknown, parent: string | null, depth: number): string | null => {
    if (typeof v === "string") return v.length > MAX_TEXT ? "text too long" : null;
    if (Array.isArray(v)) {
      if (v.length > MAX_ARRAY) return "list too long";
      if (parent !== null && v.length > MAX_CHILDREN && v.some((x) => typeof x === "object" && x !== null && (x as Node).type === "element")) return "too many children";
      for (const x of v) {
        const bad = visit(x, parent, depth);
        if (bad) return bad;
      }
      return null;
    }
    if (typeof v !== "object" || v === null) return null;
    const n = v as Node;
    if (n.type !== "element") return "unexpected value";
    const name = String(n.typeName);
    if (!ALLOWED_COMPONENTS.includes(name)) return `component ${name}`;
    if (parent !== null && !CONTAINS[parent]?.includes(name)) return `${name} inside ${parent}`;
    if (n.hasDynamicProps) return "dynamic props";
    if (++nodes > MAX_NODES) return "too many components";
    if (depth > MAX_DEPTH) return "nested too deeply";
    for (const val of Object.values(n.props ?? {})) {
      const bad = visit(val, name, depth + 1);
      if (bad) return bad;
    }
    return null;
  };
  const bad = visit(r.root, null, 1);
  return bad ? { ok: false, reason: bad } : { ok: true };
}
