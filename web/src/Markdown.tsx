import { memo, useState, type ReactNode } from "react";

// A small Markdown subset rendered straight to React elements. There is no HTML
// parsing and no dangerouslySetInnerHTML anywhere: every character of model
// output reaches the DOM as text, so markup in a reply is shown, not run.
// Anything that cannot be parsed falls back to plain text.

type Block =
  | { t: "p"; text: string }
  | { t: "h"; level: number; text: string }
  | { t: "code"; text: string; lang: string }
  | { t: "table"; head: string[]; rows: string[][] }
  | { t: "ul" | "ol"; items: string[] }
  | { t: "quote"; text: string };

const MAX_PARSE = 200_000;
const TABLE_RULE = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

function cells(line: string): string[] {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") {
      i++;
      continue;
    }
    const fence = /^```[ \t]*([^\s`]*)/.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) body.push(lines[i++]);
      i++; // closing fence (or end of input: an unterminated fence is still code)
      out.push({ t: "code", text: body.join("\n"), lang: /^[\w+#.-]{1,32}$/.test(fence[1]) ? fence[1].toLowerCase() : "" });
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      out.push({ t: "h", level: h[1].length, text: h[2] });
      i++;
      continue;
    }
    if (/^>\s?/.test(line)) {
      const q: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) q.push(lines[i++].replace(/^>\s?/, ""));
      out.push({ t: "quote", text: q.join("\n") });
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && TABLE_RULE.test(lines[i + 1])) {
      const head = cells(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && lines[i].trim() !== "" && lines[i].includes("|")) rows.push(cells(lines[i++]));
      out.push({ t: "table", head, rows });
      continue;
    }
    const ul = /^\s*[-*+]\s+/;
    const ol = /^\s*\d+[.)]\s+/;
    if (ul.test(line) || ol.test(line)) {
      const re = ul.test(line) ? ul : ol;
      const items: string[] = [];
      while (i < lines.length && re.test(lines[i])) items.push(lines[i++].replace(re, ""));
      out.push({ t: re === ul ? "ul" : "ol", items });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && !/^(```|#{1,6}\s|>\s?)/.test(lines[i]) && !ul.test(lines[i]) && !ol.test(lines[i])) para.push(lines[i++]);
    out.push({ t: "p", text: para.join("\n") });
  }
  return out;
}

/** A link target is kept only if it is an absolute http(s) or mailto URL with no control characters. */
export function safeHref(raw: string): string | null {
  // eslint-disable-next-line no-control-regex
  if (/[\u0000- \u007f-\u009f]/.test(raw) || raw.length > 2048) return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" || u.protocol === "mailto:" ? u.href : null;
  } catch {
    return null;
  }
}

const INLINE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|(\*[^*\n]+\*)|(\[[^\]\n]+\]\([^)\s]*\))/;

export function inline(text: string, key = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let n = 0;
  while (rest !== "") {
    const m = INLINE.exec(rest);
    if (!m) {
      out.push(rest);
      break;
    }
    if (m.index > 0) out.push(rest.slice(0, m.index));
    const tok = m[0];
    const k = `${key}-${n++}`;
    if (m[1]) out.push(<code key={k}>{tok.slice(1, -1)}</code>);
    else if (m[2]) out.push(<strong key={k}>{inline(tok.slice(2, -2), k)}</strong>);
    else if (m[3]) out.push(<em key={k}>{inline(tok.slice(1, -1), k)}</em>);
    else {
      const lm = /^\[([^\]]+)\]\(([^)]*)\)$/.exec(tok);
      const href = lm ? safeHref(lm[2]) : null;
      // An unsafe or malformed link is shown as its literal text, not as a link.
      out.push(
        lm && href ? (
          <a key={k} href={href} target="_blank" rel="noopener noreferrer nofollow">
            {lm[1]}
          </a>
        ) : (
          tok
        ),
      );
    }
    rest = rest.slice(m.index + tok.length);
  }
  return out;
}

function lines(text: string, key: string): ReactNode[] {
  return text.split("\n").flatMap((l, i) => (i === 0 ? inline(l, `${key}-0`) : [<br key={`${key}-br${i}`} />, ...inline(l, `${key}-${i}`)]));
}

function CodeBlock({ text, lang }: { text: string; lang: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const [seen, setSeen] = useState(text);
  if (seen !== text) {
    // The block grew while streaming: a "Copied" label would describe text that is gone.
    setSeen(text);
    setState("idle");
  }
  const copy = () => {
    navigator.clipboard.writeText(text).then(
      () => setState("copied"),
      () => setState("failed"),
    );
  };
  return (
    <div className="codeblock">
      <div className="codehead">
        <span className="label">{lang || "text"}</span>
        <button type="button" className="quiet" onClick={copy}>
          {state === "copied" ? "Copied" : state === "failed" ? "Copy failed" : "Copy"}
        </button>
      </div>
      <pre>
        <code data-lang={lang || undefined}>
          {text}
        </code>
      </pre>
    </div>
  );
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

function same(a: Block, b: Block): boolean {
  switch (a.t) {
    case "code":
      return b.t === "code" && a.text === b.text && a.lang === b.lang;
    case "table":
      return b.t === "table" && sameList(a.head, b.head) && a.rows.length === b.rows.length && a.rows.every((r, i) => sameList(r, b.rows[i]));
    case "ul":
    case "ol":
      return b.t === a.t && sameList(a.items, b.items);
    case "h":
      return b.t === "h" && a.level === b.level && a.text === b.text;
    default:
      return b.t === a.t && a.text === (b as typeof a).text;
  }
}

// While a reply streams, only the last block changes. Each block is memoised on its parsed
// content, so finished blocks are not re-rendered per token. Comparison walks the block's
// strings directly; it allocates nothing. (The line scan itself is linear and cheap;
// React reconciliation was the cost.)
const BlockView = memo(function BlockView({ b, id }: { b: Block; id: string }) {
  return renderBlock(b, id);
}, (p, n) => p.id === n.id && same(p.b, n.b));

function render(blocks: Block[]): ReactNode[] {
  return blocks.map((b, i) => <BlockView key={`b${i}`} b={b} id={`b${i}`} />);
}

function renderBlock(b: Block, k: string): ReactNode {
  switch (b.t) {
    case "p":
      return <p>{lines(b.text, k)}</p>;
    case "h": {
      const Tag = `h${Math.min(6, Math.max(3, b.level + 2))}` as "h3" | "h4" | "h5" | "h6";
      return <Tag>{inline(b.text, k)}</Tag>;
    }
    case "code":
      return <CodeBlock text={b.text} lang={b.lang} />;
    case "table":
      return (
        <div className="tablewrap">
          <table>
            <thead>
              <tr>
                {b.head.map((c, j) => (
                  <th key={j}>{inline(c, `${k}-h${j}`)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((r, j) => (
                <tr key={j}>
                  {r.map((c, m) => (
                    <td key={m}>{inline(c, `${k}-${j}-${m}`)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case "quote":
      return <blockquote>{lines(b.text, k)}</blockquote>;
    case "ul":
    case "ol": {
      const Tag = b.t;
      return (
        <Tag>
          {b.items.map((it, j) => (
            <li key={j}>{inline(it, `${k}-${j}`)}</li>
          ))}
        </Tag>
      );
    }
  }
}

/** Renders model or user text. On any problem, or for very large input, shows it as plain text. */
export function Markdown({ text }: { text: string }) {
  if (text.length > MAX_PARSE) return <pre className="plain">{text}</pre>;
  try {
    return <div className="md">{render(parseBlocks(text))}</div>;
  } catch {
    return <pre className="plain">{text}</pre>;
  }
}
