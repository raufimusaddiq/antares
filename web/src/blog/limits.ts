import type { DocNode } from "./types";

/** The numbers the author is held to, as GET /api/blog/limits states them. */
export interface Limits {
  checkBytes: number;
  docBytes: number;
  imagesPerPost: number;
  imageBytes: number;
  imageBytesTotal: number;
  blocksPerCheck: number;
  sourcesPerBlock: number;
  checksPer24h: number;
}

// The text of one top-level part as the check numbers it for the assistant (the same shape as the server's nodeText).
function partText(n: DocNode): string {
  switch (n.type) {
    case "text":
      return n.text ?? "";
    case "hardBreak":
      return "\n";
    case "image": {
      const alt = n.attrs?.alt;
      return typeof alt === "string" && alt !== "" ? `(image: ${alt})` : "(image)";
    }
    case "horizontalRule":
      return "---";
  }
  const sep = n.type === "bulletList" || n.type === "orderedList" || n.type === "blockquote" || n.type === "listItem" ? "\n" : "";
  const t = (n.content ?? []).map(partText).join(sep);
  return n.type === "heading" ? `## ${t}` : n.type === "listItem" ? `- ${t}` : t;
}

/**
 * About how many bytes of text a check would read for this draft: the title and each top-level part, numbered, as the
 * server builds it. An estimate to tell the author early; the server still has the last word and says so itself.
 * (JS trim() and the server's strings.TrimSpace differ on a few characters such as U+FEFF, so it can be a few bytes off
 * at the very edge of the limit; the button only disables, and the server refuses by itself.)
 */
export function checkBytes(title: string, doc: DocNode): number {
  let s = `TITLE: ${title.trim()}\n\n`;
  (doc.content ?? []).forEach((n, i) => {
    s += `[${i}] ${partText(n).trim()}\n\n`;
  });
  return new TextEncoder().encode(s).length;
}

/** How many images the draft holds. */
export function imageCount(doc: DocNode): number {
  return (doc.type === "image" ? 1 : 0) + (doc.content ?? []).reduce((n, c) => n + imageCount(c), 0);
}

/** "38 KB", "1.2 MB": sizes as the author reads them. */
export function sizeWords(bytes: number): string {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1).replace(/\.0$/, "")} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}
