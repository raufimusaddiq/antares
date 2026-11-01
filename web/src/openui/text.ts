import "./zodConfig"; // must stay first: see the file
import { createParser } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";
import { repair } from "./validate";

// An OpenUI reply as the words a reader sees, for the share image (#250): every text field in the order the card shows
// it, a row of a list as "left: right", headings set apart by a blank line. Style choices (variant, size, icon…),
// addresses and buttons that only suggest a next message are left out. Loaded on demand, like Block.tsx.

const STYLE = /^(variant|size|type|color|icon|align|direction|layout|category|src|url|href)$|(Variant|Src|Url|Icon|Type)$/;
const SKIP = new Set(["FollowUpBlock", "Buttons", "Button", "IconButton"]);

interface Node {
  type?: unknown;
  typeName?: unknown;
  props?: Record<string, unknown>;
}

let parser: ReturnType<typeof createParser> | undefined;

function words(v: unknown, key = ""): string[] {
  if (typeof v === "string") return STYLE.test(key) || v.trim() === "" ? [] : [v.trim()];
  if (typeof v === "number") return key ? [] : [String(v)];
  if (Array.isArray(v)) {
    if (v.every((x) => typeof x !== "object" || x === null)) {
      const cells = v.flatMap((x) => words(x)).join(" · ");
      return cells ? [cells] : [];
    }
    return v.flatMap((x) => words(x, key));
  }
  if (typeof v !== "object" || v === null) return [];
  const n = v as Node;
  if (n.type === "element") {
    if (SKIP.has(String(n.typeName))) return [];
    const out = Object.entries(n.props ?? {}).flatMap(([k, x]) => words(x, k));
    return String(n.typeName).endsWith("Header") && out.length > 0 ? ["", ...out] : out;
  }
  const own = Object.entries(v as Record<string, unknown>).flatMap(([k, x]) => (typeof x === "object" && x !== null ? [] : words(x, k)));
  const nested = Object.entries(v as Record<string, unknown>).flatMap(([k, x]) => (typeof x === "object" && x !== null ? words(x, k) : []));
  return [...(own.length > 0 ? [own.join(": ")] : []), ...nested];
}

/** The words of an OpenUI program, as Markdown-ish text, or null when it does not parse. */
export function blockText(source: string): string | null {
  parser ??= createParser(openuiChatLibrary.toJSONSchema(), openuiChatLibrary.root);
  try {
    const root = parser.parse(repair(source).program).root;
    if (!root) return null;
    return words(root).join("\n").replace(/\n{3,}/g, "\n\n").trim();
  } catch {
    return null;
  }
}
