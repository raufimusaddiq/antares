import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { outsideUrl } from "./actions";

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

// A component with an image address makes the browser fetch it with the reader's cookies (CSP allows same-origin
// images), so no image is accepted: no image field (src, bgImageSrc, a gallery's or list item's image) and no Markdown
// image. Every address the reader can follow (a url or href field, such as a Card's sources, and every Markdown link)
// must pass outsideUrl: absolute http(s), no credentials, never Antares itself. While streaming, an address that is
// still only a prefix of "https://" is let through until more of it arrives.
const IMAGE_FIELD = /^src$|Src$/;
const URL_FIELD = /^url$|Url$|^href$/;
const MD_IMAGE = /!\[/;
const MD_LINK_TARGET = /\]\(\s*<?([^)\s>]*)|^\s{0,3}\[[^\]]+\]:\s*<?(\S*)/gm;
function urlOk(url: string, partial: boolean): boolean {
  if (partial && ("https://".startsWith(url.toLowerCase()) || "http://".startsWith(url.toLowerCase()))) return true;
  return outsideUrl(url) !== null;
}
function badLink(text: string, partial: boolean): boolean {
  for (const m of text.matchAll(MD_LINK_TARGET)) if (!urlOk(m[1] ?? m[2] ?? "", partial)) return true;
  return false;
}

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
  const visit = (v: unknown, depth: number, key = ""): string | null => {
    if (typeof v === "string") {
      if (v.length > MAX_TEXT) return "text too long";
      if ((IMAGE_FIELD.test(key) && v.trim() !== "") || MD_IMAGE.test(v)) return "images are not allowed";
      if ((URL_FIELD.test(key) && !urlOk(v, partial)) || badLink(v, partial)) return "links must be absolute http(s) addresses outside Antares";
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
  return bad ? { ok: false, reason: bad } : { ok: true };
}
