import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { outsideUrl } from "./actions";

// Gate in front of the renderer for a finished reply (PRD §7.4), over OpenUI's own chat library. The parser
// checks every component and argument against the library's schema; on top of that a block is rendered only if
// it parses with no error, has the library's root, holds no query, mutation or state (no tools are wired to it), and
// stays within the size limits below. Anything else is shown as its source (Block.tsx). A finished program that is
// only structurally short (cut off, a reference to a part never written, a part nothing uses) is still rendered, with
// a note (`degraded`): the safety checks above are the same, and the whole answer as source hid every part that was fine.
// While the reply streams the program is partial, so completeness, parse errors and dangling references wait for
// the end; everything else (root, no query/mutation/state, known components, every limit) is checked on each update
// before anything is rendered. Those failures only grow with more text, so a partial program that fails one is
// final: it is shown as its source at once.

export const MAX_NODES = 600;
export const MAX_DEPTH = 12;
export const MAX_STATEMENTS = 300;
export const MAX_ARRAY = 200;
export const MAX_TEXT = 20_000;

// A component with an image address makes the browser fetch it with the reader's cookies (CSP allows same-origin
// images), so no image is accepted: no image field (src, bgImageSrc, a gallery's or list item's image) and no Markdown
// image. Every address the reader can follow must pass outsideUrl: absolute http(s), no user or password, never
// Antares itself. That covers url/href fields (a Card's sources) and every Markdown link, found by parsing each string
// the way the library's renderer does (remark + GFM), so inline, reference, <autolink> and bare-URL links are all seen;
// a mailto: link is also allowed there. While streaming, an address that is still only a prefix of "https://" is let
// through until more of it arrives.
const IMAGE_FIELD = /^src$|Src$/;
const URL_FIELD = /^url$|Url$|^href$/;
const markdown = unified().use(remarkParse).use(remarkGfm);
// Only text with one of these can hold a Markdown link or image; anything else skips the parse.
const MAY_LINK = /[[<:]|www\./i;

function urlOk(url: string, partial: boolean): boolean {
  const u = url.toLowerCase();
  if (partial && ("https://".startsWith(u) || "http://".startsWith(u))) return true;
  return outsideUrl(url) !== null;
}

interface MdNode {
  type: string;
  url?: string;
  children?: MdNode[];
}

/** The first thing wrong with the links and images of a piece of Markdown, or null. */
function markdownProblem(text: string, partial: boolean): string | null {
  if (!MAY_LINK.test(text)) return null;
  const walk = (n: MdNode): string | null => {
    if (n.type === "image" || n.type === "imageReference") return "images are not allowed";
    if ((n.type === "link" || n.type === "definition") && typeof n.url === "string") {
      if (!/^mailto:/i.test(n.url) && !urlOk(n.url, partial)) return "links must be absolute http(s) addresses outside Antares";
    }
    for (const c of n.children ?? []) {
      const bad = walk(c);
      if (bad) return bad;
    }
    return null;
  };
  return walk(markdown.runSync(markdown.parse(text)) as MdNode);
}

/** Parser errors about one argument's shape, which OpenUI handles by leaving that value out. */
const TOLERATED = new Set(["type-mismatch", "excess-args"]);

export type Verdict = { ok: true; degraded?: string } | { ok: false; reason: string };

let parser: ReturnType<typeof createParser> | undefined;

interface Node {
  type?: unknown;
  typeName?: unknown;
  props?: Record<string, unknown>;
}

// A model often writes a Card's sources as a statement of their own (`sources = [{title, sourceName, url}, …]`) and
// then lists it among the Card's children or never uses it, instead of passing it as the Card's second argument. The
// library drops it from the children (the sources vanish) and the gate refuses an unused one (the whole answer is shown
// as source). attachSources moves that statement into the second argument. It is kept only if the result parses as a
// Card that has those sources and no error the original did not have; otherwise the original is returned unchanged.
const STATEMENT = /^[A-Za-z_]\w*\s*=/m;

export function attachSources(source: string): string {
  const start = source.search(/^root\s*=\s*Card\(/m);
  if (start < 0) return source;
  const line = source.indexOf("\n", start) + 1 || source.length; // the next statement starts on a later line
  const after = source.slice(line).search(STATEMENT);
  const end = after < 0 ? source.length : line + after;
  const root = /^root\s*=\s*Card\(\s*\[([\s\S]*)\]\s*\)\s*$/.exec(source.slice(start, end).trimEnd() + "\n");
  if (!root) return source; // not a one-argument Card([...])
  const def = /^([A-Za-z_]\w*)\s*=\s*\[\s*\{/m.exec(source);
  if (!def) return source;
  const name = def[1];
  // Used only by its own definition, and at most once more among the root's children.
  const uses = source.match(new RegExp(`\\b${name}\\b`, "g"))?.length ?? 0;
  const inner = root[1].trimEnd().replace(/,\s*$/, "");
  const children = inner.replace(new RegExp(`(^|,)\\s*${name}\\s*(?=,|$)`), "").replace(/^\s*,/, "").trimEnd();
  const listed = children !== inner;
  if (uses !== (listed ? 2 : 1)) return source;
  const fixed = source.slice(0, start) + `root = Card([${children}\n], ${name})\n` + source.slice(end);
  parser ??= createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root);
  try {
    const was = parser.parse(source).meta.errors.length;
    const r = parser.parse(fixed);
    if (!Array.isArray(r.root?.props?.sources) || r.meta.errors.length > was || r.meta.orphaned.length > 0) return source;
  } catch {
    return source;
  }
  return fixed;
}

// A model sometimes names a statement one thing and refers to it by another (`s5t = …` listed as `z5t`), or writes a
// statement no parent lists. repair re-links them: when the references that resolve to nothing and the outermost unused
// statements are equal in number, each reference is renamed to the statement in the same order; statements still
// unused are appended to the root Card (`repaired`, which Block.tsx tells the reader). A step is kept only if it leaves fewer such problems and no new parse error.
// A cut-off program is left alone: its missing parts were never written.
export function repair(source: string): { program: string; repaired: boolean } {
  const attached = attachSources(source); // not a repair the reader needs told about: nothing is moved or renamed
  let program = attached;
  parser ??= createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root);
  const p = parser;
  const read = (src: string) => {
    try {
      const m = p.parse(src).meta;
      return { m, n: m.unresolved.length + m.orphaned.length };
    } catch {
      return null;
    }
  };
  let best = read(program);
  if (!best || best.n === 0 || best.m.incomplete) return { program, repaired: false };
  const step = (next: string | null) => {
    const r = next === null ? null : read(next);
    if (r && best && r.n < best.n && r.m.errors.length <= best.m.errors.length) [best, program] = [r, next!];
  };
  const dangling = [...best.m.unresolved].sort((a, b) => firstUse(program, a) - firstUse(program, b));
  const loose = outermost(program, best.m.orphaned);
  if (dangling.length > 0 && dangling.length === loose.length) step(rename(program, new Map(dangling.map((d, i) => [d, loose[i]]))));
  const left = outermost(program, best.m.orphaned);
  if (left.length > 0) step(appendToRoot(program, left));
  return { program, repaired: program !== attached };
}

const LITERAL = /"(?:[^"\\\n]|\\.)*"/g;
/** The program with every string literal emptied, so a name inside text is never taken for a reference. */
const code = (src: string) => src.replace(LITERAL, '""');
const firstUse = (src: string, name: string) => code(src).search(new RegExp(`\\b${name}\\b`));

/** The text of statement `name`, up to the next statement. */
function statement(src: string, name: string): { at: number; text: string } {
  const m = new RegExp(`^${name}\\s*=`, "m").exec(src);
  if (!m) return { at: -1, text: "" };
  const rest = src.slice(m.index + m[0].length);
  const next = rest.search(STATEMENT);
  return { at: m.index, text: next < 0 ? rest : rest.slice(0, next) };
}

/** The unused statements no other unused statement refers to, in the order they were written. */
function outermost(src: string, orphaned: readonly string[]): string[] {
  const bodies = orphaned.map((n) => code(statement(src, n).text));
  return orphaned
    .filter((n) => !bodies.some((b) => new RegExp(`\\b${n}\\b`).test(b)))
    .sort((a, b) => statement(src, a).at - statement(src, b).at);
}

/** Renames references outside string literals. */
function rename(src: string, names: Map<string, string>): string {
  const ref = new RegExp(`\\b(${[...names.keys()].join("|")})\\b`, "g");
  let out = "";
  let last = 0;
  for (const m of src.matchAll(LITERAL)) {
    out += src.slice(last, m.index).replace(ref, (n) => names.get(n)!) + m[0];
    last = m.index + m[0].length;
  }
  return out + src.slice(last).replace(ref, (n) => names.get(n)!);
}

/** Adds names to the end of the root Card's children, or null if the root is not `Card([...]`. */
function appendToRoot(src: string, names: string[]): string | null {
  const open = /^root\s*=\s*Card\(\s*\[/m.exec(src);
  if (!open) return null;
  let depth = 1;
  for (let i = open.index + open[0].length; i < src.length; i++) {
    const c = src[i];
    if (c === '"') {
      LITERAL.lastIndex = i;
      const lit = LITERAL.exec(src);
      if (!lit || lit.index !== i) return null;
      i += lit[0].length - 1;
    } else if (c === "[") depth++;
    else if (c === "]" && --depth === 0) {
      const before = src.slice(0, i).trimEnd();
      const sep = before.endsWith("[") || before.endsWith(",") ? " " : ", ";
      return before + sep + names.join(", ") + src.slice(i);
    }
  }
  return null;
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
  let degraded: string | undefined;
  if (!r.root) return partial ? { ok: true } : { ok: false, reason: "no root" };
  if (r.root.typeName !== openuiChatLibrary.root) return { ok: false, reason: `root must be ${openuiChatLibrary.root}` };
  if (r.rootExpression) return { ok: false, reason: "computed root" };
  // An unknown component is dropped by the parser and only reported here; more text cannot fix it.
  const unknown = m.errors.find((e) => e.code === "unknown-component");
  if (unknown) return { ok: false, reason: "invalid: unknown-component" };
  if (!partial) {
    // An argument of the wrong type, or one too many, is dropped by OpenUI and the rest renders; showing the whole
    // answer as source for that hid good content (a model's SectionItem with one mistyped field). Any other parse error
    // (an unknown function, no root) still falls back to source.
    const fatal = m.errors.find((e) => !TOLERATED.has(e.code));
    if (fatal) return { ok: false, reason: `invalid: ${fatal.code}` };
    // What the renderer leaves out here is named in the note, and the source is one click away (Block.tsx).
    if (m.incomplete) degraded = "the reply was cut off";
    else if (m.unresolved.length > 0) degraded = "it refers to a part that was never written";
    else if (m.orphaned.length > 0) degraded = "a part was not placed in the card";
  }
  if (m.statementCount > MAX_STATEMENTS) return { ok: false, reason: "too many statements" };
  if (r.queryStatements.length > 0 || r.mutationStatements.length > 0) return { ok: false, reason: "queries and mutations are not allowed" };
  if (Object.keys(r.stateDeclarations).length > 0) return { ok: false, reason: "state is not allowed" };

  const known = new Set(Object.keys(openuiChatLibrary.components));
  let nodes = 0;
  const visit = (v: unknown, depth: number, key = ""): string | null => {
    if (typeof v === "string") {
      if (v.length > MAX_TEXT) return "text too long";
      if (IMAGE_FIELD.test(key) && v.trim() !== "") return "images are not allowed";
      if (URL_FIELD.test(key) && !urlOk(v, partial)) return "links must be absolute http(s) addresses outside Antares";
      const bad = markdownProblem(v, partial);
      if (bad) return bad;
      return null;
    }
    if (Array.isArray(v)) {
      if (v.length > MAX_ARRAY) return "list too long";
      for (const x of v) {
        const bad = visit(x, depth, key);
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
      for (const [k, val] of Object.entries(n.props ?? {})) {
        const inner = visit(val, depth + 1, k);
        if (inner) return inner;
      }
      return null;
    }
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      const inner = visit(val, depth, k);
      if (inner) return inner;
    }
    return null;
  };
  const bad = visit(r.root, 1);
  if (bad) return { ok: false, reason: bad };
  return degraded ? { ok: true, degraded } : { ok: true };
}
