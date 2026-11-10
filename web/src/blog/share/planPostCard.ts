import { PAD, STORY_H, W } from "../../cardKit";

// Where everything goes on a post's story picture (spec 002 FR-005, FR-021, FR-022). Pure: it is told how wide a piece of text
// is (measure) and answers with lines and positions, so the layout can be tested without a canvas. The canvas is always
// exactly 1080 x 1920; a long post is condensed to fit, never continued.

export type Measure = (font: string, text: string, spacing?: number) => number;

export interface PostCardInput {
  title: string;
  summary: string;
  highlights: string[];
  authorName: string;
  hasCover: boolean;
}

export interface PostCardPlan {
  /** Right-to-left text: lines are drawn from the right edge, bullets on the right. */
  rtl: boolean;
  title: { lines: string[]; size: number; lh: number; y: number };
  barY: number;
  authorY: number;
  panel: { y: number; h: number };
  cover: boolean;
  summary: { lines: string[]; size: number; lh: number; y: number; cut: boolean };
  highlights: { items: { lines: string[]; y: number }[]; size: number; lh: number };
  /** The lowest y any content of the card reaches. */
  contentBottom: number;
}

export const SANS_FAMILY = '"Space Grotesk Variable", "Space Grotesk Fallback", system-ui, sans-serif';
const TITLE_Y = 290;
const PANEL_BOTTOM = 1778;
const STRIP = 96;
const TOP_PAD = 52;
const BOTTOM_PAD = 56;
const PANEL_X = PAD;
const PANEL_W = W - 2 * PAD - 12;
export const INNER_X = PANEL_X + 56;
export const INNER_W = PANEL_W - 112;
export const COVER_H = 440;
const COVER_GAP = 48;
const BLOCK_GAP = 36;
const MIN_SIZE = 36;
const BULLET_INDENT = 50;
const ITEM_GAP = 20;
export const TITLE_W = W - 2 * PAD;

const RTL = /[֐-ࣿיִ-﷿ﹰ-﻿]/;
const LETTER = /\p{L}/u;
/** The direction of a text is the direction of its first letter. */
export function isRtl(text: string): boolean {
  for (const ch of text) if (LETTER.test(ch)) return RTL.test(ch);
  return false;
}

const segments = (s: string): string[] =>
  typeof Intl !== "undefined" && "Segmenter" in Intl ? Array.from(new Intl.Segmenter(undefined, { granularity: "word" }).segment(s), (x) => x.segment) : s.split(/(\s+)/);

/**
 * Lines of at most `width`, broken between words (Intl.Segmenter, so a language written without spaces breaks between words)
 * and, for one word wider than a line, inside it. At most `max` lines: when the text is longer the last one ends with an
 * ellipsis and `cut` is true.
 */
export function breakLines(text: string, width: number, fit: (s: string) => number, max: number): { lines: string[]; cut: boolean } {
  const lines: string[] = [];
  let cur = "";
  const push = () => {
    const l = cur.trimEnd();
    if (l) lines.push(l);
    cur = "";
  };
  for (const seg of segments(text.replace(/\s+/g, " ").trim())) {
    if (cur === "" && seg.trim() === "") continue;
    if (fit((cur + seg).trimEnd()) <= width) {
      cur += seg;
      continue;
    }
    if (cur !== "") {
      push();
      if (seg.trim() === "") continue;
    }
    cur = seg;
    // one word wider than a line: break it inside
    while (fit(cur) > width && cur.length > 1) {
      let n = cur.length - 1;
      while (n > 1 && fit(cur.slice(0, n)) > width) n--;
      lines.push(cur.slice(0, n));
      cur = cur.slice(n);
    }
  }
  push();
  if (lines.length <= max) return { lines, cut: false };
  const kept = lines.slice(0, max);
  let last = kept[max - 1];
  while (last && fit(`${last}…`) > width) last = last.slice(0, -1);
  kept[max - 1] = `${last.trimEnd()}…`;
  return { lines: kept, cut: true };
}

const TITLE_STEPS: { size: number; lh: number; max: number }[] = [
  { size: 60, lh: 70, max: 2 },
  { size: 52, lh: 62, max: 3 },
  { size: 46, lh: 54, max: 4 },
];
const SUMMARY_SIZES = [56, 54, 52, 50, 48, 46, 44, 42, 40, 38, 36];

export function planPostCard(input: PostCardInput, measure: Measure): PostCardPlan {
  const rtl = isRtl(input.title);
  // The title: the largest size that keeps it to its lines; the last step clamps with an ellipsis.
  let title = { lines: [] as string[], size: 0, lh: 0, y: TITLE_Y };
  for (const [i, st] of TITLE_STEPS.entries()) {
    const r = breakLines(input.title, TITLE_W, (s) => measure(`700 ${st.size}px ${SANS_FAMILY}`, s, -1.5), st.max);
    title = { lines: r.lines, size: st.size, lh: st.lh, y: TITLE_Y };
    if (!r.cut || i === TITLE_STEPS.length - 1) break;
  }
  const titleBottom = TITLE_Y + title.lines.length * title.lh;
  const barY = titleBottom + 6;
  const authorY = titleBottom + 32;
  const panelTop = authorY + 40;
  const panelH = PANEL_BOTTOM - panelTop;
  const avail = panelH - STRIP - TOP_PAD - BOTTOM_PAD;
  const contentTop = panelTop + STRIP + TOP_PAD;

  const nHi = input.highlights.length;
  const build = (cover: boolean, k: number, size: number, maxLines: number) => {
    const lh = Math.round(size * 1.38);
    const sum = input.summary
      ? breakLines(input.summary, INNER_W, (s) => measure(`400 ${size}px ${SANS_FAMILY}`, s), maxLines)
      : { lines: [] as string[], cut: false };
    const hs = Math.min(42, size);
    const hlh = Math.round(hs * 1.33);
    const items = input.highlights.slice(0, k).map((t) => breakLines(t, INNER_W - BULLET_INDENT, (s) => measure(`700 ${hs}px ${SANS_FAMILY}`, s), 2).lines);
    const summaryH = sum.lines.length * lh;
    const hiH = items.reduce((n, l) => n + l.length * hlh, 0) + Math.max(0, items.length - 1) * ITEM_GAP;
    const total = (cover ? COVER_H + (summaryH || hiH ? COVER_GAP : 0) : 0) + summaryH + (items.length && summaryH ? BLOCK_GAP : 0) + hiH;
    return { cover, size, lh, sum, hs, hlh, items, summaryH, total };
  };

  let pick: ReturnType<typeof build> | null = null;
  // Type steps down first (to MIN_SIZE); only then are parts dropped: the last highlights, then the cover, then the summary's end.
  search: for (const cover of input.hasCover ? [true, false] : [false]) {
    for (let k = nHi; k >= 0; k--) {
      const cap = cover ? (k > 0 ? 46 : 54) : 56;
      for (const size of SUMMARY_SIZES.filter((s) => s <= cap)) {
        const b = build(cover, k, size, 1000);
        if (b.total <= avail) {
          pick = b;
          break search;
        }
      }
    }
  }
  if (!pick) {
    const lh = Math.round(MIN_SIZE * 1.38);
    pick = build(false, 0, MIN_SIZE, Math.max(1, Math.floor(avail / lh)));
  }

  let y = contentTop;
  const coverOn = pick.cover;
  if (coverOn) y += COVER_H + COVER_GAP;
  const summary = { lines: pick.sum.lines, size: pick.size, lh: pick.lh, y, cut: pick.sum.cut };
  y += pick.summaryH + (pick.items.length && pick.summaryH ? BLOCK_GAP : 0);
  const items = pick.items.map((lines) => {
    const it = { lines, y };
    y += lines.length * pick.hlh + ITEM_GAP;
    return it;
  });
  const contentBottom = items.length ? y - ITEM_GAP : coverOn || summary.lines.length ? contentTop + pick.total : contentTop;
  return { rtl, title, barY, authorY, panel: { y: panelTop, h: panelH }, cover: coverOn, summary, highlights: { items, size: pick.hs, lh: pick.hlh }, contentBottom };
}

export { STORY_H, PANEL_BOTTOM, PANEL_W, PANEL_X, STRIP, BOTTOM_PAD };
