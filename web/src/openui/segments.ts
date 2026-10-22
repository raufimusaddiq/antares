// Splits assistant text into Markdown and OpenUI Lang segments (PRD §7.2). This
// file is deliberately tiny and dependency-free: it is in the main bundle, and
// the OpenUI runtime (react-lang, zod, the component library) is only fetched
// when a message actually contains an OpenUI block (PRD PERF-001 item 4).
//
// An OpenUI block is a fenced block whose info string is `openui` or
// `openui-lang`. Anything else, including an `openui` fence nested in another
// code fence, is ordinary Markdown.

export type Segment = { kind: "md"; text: string } | { kind: "openui"; source: string; closed: boolean };

export const MAX_BLOCK_CHARS = 20_000;
export const MAX_BLOCKS = 8;

const OPEN = /^(\s{0,3})(`{3,})\s*([A-Za-z0-9_-]*)\s*$/;

export function splitSegments(text: string): Segment[] {
  if (!/```\s*openui/i.test(text)) return [{ kind: "md", text }];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  const out: Segment[] = [];
  let md: string[] = [];
  const flush = () => {
    if (md.length > 0 && md.join("\n").trim() !== "") out.push({ kind: "md", text: md.join("\n") });
    md = [];
  };
  let blocks = 0;
  let i = 0;
  while (i < lines.length) {
    const m = OPEN.exec(lines[i]);
    if (!m) {
      md.push(lines[i++]);
      continue;
    }
    const fence = m[2];
    const info = m[3].toLowerCase();
    const close = new RegExp("^\\s{0,3}`{" + fence.length + ",}\\s*$");
    let j = i + 1;
    while (j < lines.length && !close.test(lines[j])) j++;
    const body = lines.slice(i + 1, j);
    const closed = j < lines.length;
    const isUI = info === "openui" || info === "openui-lang";
    const source = body.join("\n");
    if (isUI && blocks < MAX_BLOCKS && source.length <= MAX_BLOCK_CHARS) {
      blocks++;
      flush();
      out.push({ kind: "openui", source, closed });
    } else {
      // Ordinary code, or an OpenUI block over the limits: kept as Markdown, so
      // it is shown as inert text.
      md.push(...lines.slice(i, closed ? j + 1 : j));
    }
    i = closed ? j + 1 : j;
  }
  flush();
  return out;
}
