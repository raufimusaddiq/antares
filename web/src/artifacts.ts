// HTML/SVG artifacts in an assistant turn (PRD §21.8). An artifact is a closed
// fenced block tagged `html` or `svg`, named by (chat, turn seq, index in the turn).
// The server finds them the same way (internal/chat/artifacts.go) and serves the
// bytes; the browser only ever points a sandboxed iframe or a download link at them.

export interface ArtifactRef {
  kind: "html" | "svg";
  index: number;
}

// Space and tab only, never \s: it must agree with the Go parser on NBSP, em space, \v, \f
// (internal/chat/testdata/artifact_cases.json is run by both suites).
const OPEN = /^[ \t]{0,3}(`{3,})[ \t]*([A-Za-z0-9_-]*)[ \t]*$/;
const MAX_BYTES = 512 << 10;
const MAX_ARTIFACTS = 32;

export function findArtifacts(text: string): ArtifactRef[] {
  if (!/```\s*(html|svg)/i.test(text)) return [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  const out: ArtifactRef[] = [];
  for (let i = 0; i < lines.length; ) {
    const m = OPEN.exec(lines[i]);
    if (!m) {
      i++;
      continue;
    }
    const close = new RegExp("^[ \\t]{0,3}`{" + m[1].length + ",}[ \\t]*$");
    let j = i + 1;
    while (j < lines.length && !close.test(lines[j])) j++;
    if (j >= lines.length) break; // still streaming
    const kind = m[2].toLowerCase();
    const size = new TextEncoder().encode(lines.slice(i + 1, j).join("\n")).length;
    if ((kind === "html" || kind === "svg") && out.length < MAX_ARTIFACTS && size <= MAX_BYTES) out.push({ kind, index: out.length });
    i = j + 1;
  }
  return out;
}

/** `ref` is the turn's hash: a seq alone shifts under compaction, and the server refuses a stale pair. */
export const artifactUrl = (chatId: string, seq: number, ref: string, index: number, preview = false) =>
  `/api/chats/${encodeURIComponent(chatId)}/messages/${seq}/artifacts/${index}${preview ? "/preview" : ""}?ref=${encodeURIComponent(ref)}`;
