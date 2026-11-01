import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { mdBlocks, type Block, type Run } from "../shareBlocks";
import { repair } from "./validate";

// An OpenUI reply as the blocks the share image draws (#250): a header becomes a heading, an entity list a table of
// rows, tags pills, a callout a box, and so on. Buttons, forms, images and follow-up suggestions are left out, and a
// chart is named, not drawn. Loaded on demand, like Block.tsx.

interface Node {
  type?: unknown;
  typeName?: unknown;
  props?: Record<string, unknown>;
}

const SKIP = new Set(["FollowUpBlock", "Buttons", "Button", "IconButton", "Form", "Image", "ImageBlock", "ImageGallery", "Icon"]);
// Fields that hold an id, an action or a picture rather than words.
const NOT_WORDS = /^(id|value|name|action|actionLabel|image|alt|src|url|href|rules|defaultValue|visible|disabled|bold)$|(Src|Alt|Color|Url)$/;

let parser: ReturnType<typeof createParser> | undefined;
let enums: Map<string, Set<string>> | undefined;

const isNode = (v: unknown): v is Node => typeof v === "object" && v !== null && (v as Node).type === "element";
const str = (v: unknown) => (typeof v === "string" ? v.trim() : typeof v === "number" ? String(v) : "");
const list = (v: unknown) => (Array.isArray(v) ? v : []);
const propsOf = (v: unknown) => (isNode(v) ? (v.props ?? {}) : typeof v === "object" && v !== null ? (v as Record<string, unknown>) : {});
const para = (text: string, bold = false): Block[] => (text ? [{ kind: "para", runs: [{ text, bold }] }] : []);
const muted = (text: string): Block[] => (text ? [{ kind: "para", runs: [{ text }], muted: true }] : []);
const label = (text: string): Block[] => (text ? [{ kind: "label", text }] : []);
const titled = (title: string, rest: string): Run[] => [...(title ? [{ text: title, bold: true }] : []), ...(title && rest ? [{ text: " — " }] : []), ...(rest ? [{ text: rest }] : [])];

function node(v: unknown): Block[] {
  if (Array.isArray(v)) return v.flatMap(node);
  if (!isNode(v)) return [];
  const t = String(v.typeName);
  const p = v.props ?? {};
  if (SKIP.has(t)) return [];
  if (t.endsWith("Chart")) return muted("A chart is in the chat; it is not drawn here.");
  switch (t) {
    case "Card":
      return node(p.children);
    case "CardHeader":
      return [...(str(p.title) ? [{ kind: "heading", level: 1, text: str(p.title) } as Block] : []), ...muted(str(p.subtitle))];
    case "MarkDownRenderer":
      return mdBlocks(str(p.textMarkdown));
    case "TextContent":
      return mdBlocks(str(p.text));
    case "Text":
    case "BoldText":
      return [...para(str(p.value), t === "BoldText"), ...muted(str(p.subtext))];
    case "IconText":
      return [...para(str(p.title), p.bold === true), ...muted(str(p.subtitle))];
    case "InlineHeader":
      return [...label(str(p.heading)), ...muted(str(p.description))];
    case "Callout":
    case "TextCallout":
      return [{ kind: "quote", title: str(p.title), runs: [{ text: str(p.description) }] }];
    case "CodeBlock":
      return str(p.codeString) ? [{ kind: "code", text: String(p.codeString) }] : [];
    case "EntityList": {
      const rows = list(p.rows).map((r): [string, string] => [str(propsOf(r).left), str(propsOf(r).right)]);
      return rows.length > 0 ? [{ kind: "rows", rows }] : [];
    }
    case "ListBlock":
    case "Steps": {
      const items = list(p.items).map((i) => ({ runs: titled(str(propsOf(i).title), str(propsOf(i).subtitle) || str(propsOf(i).details)), depth: 0 }));
      return items.length > 0 ? [{ kind: "list", ordered: t === "Steps", items }] : [];
    }
    case "TagBlock":
    case "Chips": {
      const chips = list(t === "TagBlock" ? p.tags : p.items)
        .map((x) => str(x) || str(propsOf(x).text) || str(propsOf(x).label))
        .filter(Boolean);
      return chips.length > 0 ? [{ kind: "chips", items: chips }] : [];
    }
    case "Table": {
      const cols = list(p.columns).map(propsOf);
      const n = Math.max(0, ...cols.map((c) => list(c.data).length));
      const rows = Array.from({ length: n }, (_, i) => cols.map((c) => str(list(c.data)[i])));
      return cols.length > 0 ? [{ kind: "table", head: cols.map((c) => str(c.label)), rows }] : [];
    }
    case "SectionBlock":
    case "Tabs":
      return list(t === "Tabs" ? p.items : p.sections).flatMap((s) => [...label(str(propsOf(s).trigger)), ...node(propsOf(s).content)]);
    case "Separator":
      return [{ kind: "rule" }];
  }
  // Any other component: its words in the order it lists them.
  const skip = enums?.get(t);
  return Object.entries(p).flatMap(([k, x]) => (NOT_WORDS.test(k) || skip?.has(k) ? [] : typeof x === "string" ? para(x.trim()) : node(x)));
}

/** The blocks of an OpenUI program, or null when it does not parse. */
export function openuiBlocks(source: string): Block[] | null {
  if (!parser) {
    const raw = openuiChatLibrary.toJSONSchema();
    parser = createParser(raw, openuiChatLibrary.root);
    const schema = raw as unknown as { $defs?: Record<string, { properties?: Record<string, { enum?: unknown }> }> };
    // A field whose value is one of a fixed set (variant, size…) is a style, not words.
    enums = new Map(
      Object.entries(schema.$defs ?? {}).map(([name, d]) => [
        name,
        new Set(
          Object.entries(d.properties ?? {})
            .filter(([, f]) => f.enum)
            .map(([k]) => k),
        ),
      ]),
    );
  }
  try {
    const root = parser.parse(repair(source).program).root;
    return root ? node(root) : null;
  } catch {
    return null;
  }
}
