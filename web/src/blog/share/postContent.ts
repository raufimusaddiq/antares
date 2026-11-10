import type { DocNode } from "../types";

// What the story picture says about a post (spec 002 FR-021): the author's own summary, or the post's opening text; and the
// post's own section headings as highlights. Every word comes from the post; nothing is written for it.

/** The text of a node, markup gone: its words and nothing else. */
export function plainOf(n: DocNode): string {
  if (n.type === "text") return n.text ?? "";
  if (n.type === "hardBreak") return " ";
  return (n.content ?? []).map(plainOf).join("");
}

const words = (s: string): string[] => {
  if (typeof Intl !== "undefined" && "Segmenter" in Intl) {
    return Array.from(new Intl.Segmenter(undefined, { granularity: "word" }).segment(s), (x) => x.segment);
  }
  return s.split(/(\s+)/);
};

/**
 * `text` cut to at most `max` characters, at a sentence end when one falls late enough in it, otherwise at a word boundary,
 * and ended with an ellipsis. A text that already fits comes back whole. Words are found with Intl.Segmenter, so a language
 * written without spaces still breaks between words and never in the middle of one.
 */
export function cutAt(text: string, max: number): string {
  const t = text.replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  const head = t.slice(0, max);
  const sentence = Math.max(head.lastIndexOf(". "), head.lastIndexOf("! "), head.lastIndexOf("? "), head.lastIndexOf("。"));
  if (sentence >= max * 0.5) return head.slice(0, sentence + 1).trimEnd();
  let out = "";
  for (const w of words(t)) {
    if ((out + w).length > max - 1) break;
    out += w;
  }
  out = out.trimEnd();
  if (out === "") out = head.slice(0, max - 1).trimEnd(); // one word longer than the whole allowance
  return out + "…";
}

/** The post's own summary when it has one, otherwise its opening text, cut to what a story picture can hold. */
export function summaryText(summary: string, doc: DocNode, max = 260): string {
  const own = summary.trim();
  if (own) return cutAt(own, max);
  for (const n of doc.content ?? []) {
    if (n.type !== "paragraph") continue;
    const p = plainOf(n).trim();
    if (p) return cutAt(p, max);
  }
  return "";
}

/** The post's section headings, in reading order: at most four, each shortened to fit two lines of the card. */
export function highlights(doc: DocNode, max = 4, width = 70): string[] {
  const out: string[] = [];
  const walk = (n: DocNode) => {
    if (out.length >= max) return;
    if (n.type === "heading") {
      const h = plainOf(n).replace(/\s+/g, " ").trim();
      if (h) out.push(cutAt(h, width));
      return;
    }
    for (const c of n.content ?? []) walk(c);
  };
  walk(doc);
  return out;
}
