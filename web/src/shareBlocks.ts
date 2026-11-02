import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { splitMedia } from "./mediaTags";

// An answer's Markdown as the blocks the share image draws (#250): headings, paragraphs with bold, code and links,
// lists, tables, quotes and code. Loaded on demand by shareImage.ts, so the Markdown parser is fetched only when a
// picture is made. Images and media are left out.

export type Run = { text: string; bold?: boolean; code?: boolean; link?: boolean };
export type Item = { runs: Run[]; depth: number };
export type Block =
  | { kind: "heading"; level: 1 | 2; text: string }
  | { kind: "label"; text: string }
  | { kind: "para"; runs: Run[]; muted?: boolean }
  | { kind: "list"; ordered: boolean; items: Item[] }
  | { kind: "rows"; rows: [string, string][] }
  | { kind: "table"; head: string[]; rows: string[][] }
  | { kind: "code"; text: string }
  | { kind: "quote"; title: string; runs: Run[] }
  | { kind: "chips"; items: string[] }
  | { kind: "rule" };

interface MdNode {
  type: string;
  value?: string;
  depth?: number;
  ordered?: boolean;
  children?: MdNode[];
}

const markdown = unified().use(remarkParse).use(remarkGfm);

function runs(nodes: MdNode[] = [], style: Omit<Run, "text"> = {}): Run[] {
  return nodes.flatMap((n): Run[] => {
    switch (n.type) {
      case "text":
        return [{ text: (n.value ?? "").replace(/\n/g, " "), ...style }];
      case "strong":
        return runs(n.children, { ...style, bold: true });
      case "inlineCode":
        return [{ text: n.value ?? "", ...style, code: true }];
      case "link":
      case "linkReference":
        return runs(n.children, { ...style, link: true });
      case "break":
        return [{ text: "\n", ...style }];
      case "image":
      case "imageReference":
        return [];
      default:
        return n.children ? runs(n.children, style) : n.value ? [{ text: n.value, ...style }] : [];
    }
  });
}

const flat = (n: MdNode) =>
  runs([n])
    .map((r) => r.text)
    .join("")
    .trim();
const blank = (rs: Run[]) => rs.every((r) => r.text.trim() === "");

function items(list: MdNode, depth: number): Item[] {
  return (list.children ?? []).flatMap((li) => {
    const own: Run[] = [];
    const nested: Item[] = [];
    for (const c of li.children ?? []) {
      if (c.type === "list") nested.push(...items(c, depth + 1));
      else own.push(...(own.length > 0 ? [{ text: " " }] : []), ...(c.type === "code" ? [{ text: c.value ?? "", code: true }] : runs(c.children)));
    }
    return blank(own) ? nested : [{ runs: own, depth }, ...nested];
  });
}

function blocks(nodes: MdNode[] = []): Block[] {
  return nodes.flatMap((n): Block[] => {
    switch (n.type) {
      case "heading":
        return (n.depth ?? 1) <= 2 ? [{ kind: "heading", level: n.depth === 1 ? 1 : 2, text: flat(n) }] : [{ kind: "label", text: flat(n) }];
      case "paragraph":
      case "html": {
        const r = n.type === "html" ? [{ text: n.value ?? "" }] : runs(n.children);
        return blank(r) ? [] : [{ kind: "para", runs: r }];
      }
      case "list":
        return [{ kind: "list", ordered: !!n.ordered, items: items(n, 0) }];
      case "code":
        return [{ kind: "code", text: n.value ?? "" }];
      case "blockquote":
        return [{ kind: "quote", title: "", runs: (n.children ?? []).flatMap((c, i) => [...(i > 0 ? [{ text: "\n" }] : []), ...runs(c.children)]) }];
      case "table": {
        const [head, ...rest] = (n.children ?? []).map((row) => (row.children ?? []).map(flat));
        return head ? [{ kind: "table", head, rows: rest }] : [];
      }
      case "thematicBreak":
        return [{ kind: "rule" }];
      default:
        return [];
    }
  });
}

/** An answer's Markdown as blocks to draw. */
export function mdBlocks(text: string): Block[] {
  return blocks((markdown.runSync(markdown.parse(splitMedia(text).text)) as MdNode).children);
}
