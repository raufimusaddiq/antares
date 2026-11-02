// An answer as a picture (#250): the question and the reply drawn on a canvas, in the browser, in the app's look. The
// reply keeps its shape (headings, lists, rows, code, tags), but nothing is sent anywhere and no page markup is
// rasterised, so the picture holds only the words, the date and the app's name.

import { splitMedia } from "./mediaTags";
import { splitSegments } from "./openui/segments";
import type { Block, Item, Run } from "./shareBlocks";

/** Markdown read as plain text: the words stay, the markup goes, and a link keeps its address in brackets. */
export function plainText(md: string): string {
  return splitMedia(md)
    .text.replace(/\r\n?/g, "\n")
    .replace(/^```[^\n]*\n?/gm, "")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label: string, href: string) => (label === href ? href : `${label} (${href})`))
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^>\s?/gm, "")
    .replace(/^(\s*)[-*+]\s+/gm, "$1• ")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(/`([^`\n]+)`/g, "$1")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

/** Lines of at most `width`, breaking at spaces and, for a word wider than a line, inside it. At most `max` lines:
 * when the text is longer, the last one ends with an ellipsis and `cut` is true. */
export function wrap(text: string, width: number, measure: (s: string) => number, max: number): { lines: string[]; cut: boolean } {
  const lines: string[] = [];
  for (const para of text.split("\n")) {
    let line = "";
    for (const word of para.split(/ +/)) {
      const next = line ? `${line} ${word}` : word;
      if (measure(next) <= width) {
        line = next;
        continue;
      }
      if (line) lines.push(line);
      line = "";
      for (const ch of word) {
        if (line && measure(line + ch) > width) {
          lines.push(line);
          line = "";
        }
        line += ch;
      }
    }
    lines.push(line);
  }
  if (lines.length <= max) return { lines, cut: false };
  const kept = lines.slice(0, max);
  let last = kept[max - 1];
  while (last && measure(`${last}…`) > width) last = last.slice(0, -1);
  kept[max - 1] = `${last.trimEnd()}…`;
  return { lines: kept, cut: true };
}

/** An answer as blocks to draw; an OpenUI block that does not parse is drawn as its source. */
export async function answerBlocks(answer: string): Promise<Block[]> {
  const segments = splitSegments(answer);
  const { mdBlocks } = await import("./shareBlocks");
  const ou = segments.some((s) => s.kind === "openui") ? await import("./openui/shareBlocks") : null;
  return segments.flatMap((s) => (s.kind === "md" ? mdBlocks(s.text) : (ou?.openuiBlocks(s.source) ?? [{ kind: "code", text: s.source }])));
}

const W = 1080;
const PAD = 72;
const MAX_H = 5400; // well inside every browser's canvas limit
const SANS = '"Space Grotesk Variable", "Space Grotesk Fallback", system-ui, sans-serif';
const MONO = 'ui-monospace, "SF Mono", Menlo, Consolas, monospace';
const C = {
  paper: "#f4f1e8",
  card: "#ffffff",
  ink: "#0b0b0b",
  ink2: "#4f4a40",
  hair: "#8f8878",
  cobalt: "#2d4bff",
  mark: "#ff6fae",
  peri: "#b3c1ff",
  tint: "#e6eaff",
  mint: "#8ef0c0",
  blush: "#ffb199",
};
const LOGO = "M28 5h8l24 54H49L32 20 15 59H4z"; // the A of public/icons/mark-light.svg; its dot is drawn as an arc

type Ctx = CanvasRenderingContext2D;
type Piece = Run & { w: number };
/** One slice of the answer: a line of text, a row of a table, a band of a code block. The picture is cut between slices. */
type Unit = { h: number; draw: (y: number) => void; gap?: boolean };

function spaced(ctx: Ctx, px: number) {
  if ("letterSpacing" in ctx) ctx.letterSpacing = `${px}px`;
}

const runFont = (r: Run, size: number, weight: number) => (r.code ? `500 ${Math.round(size * 0.84)}px ${MONO}` : `${r.bold ? 700 : weight} ${size}px ${SANS}`);
const same = (a: Run, b: Run) => !!a.bold === !!b.bold && !!a.code === !!b.code && !!a.link === !!b.link;

/** Styled text broken into lines of at most `width`, at spaces and, for a word wider than a line, inside it. */
function layout(ctx: Ctx, runs: Run[], width: number, size: number, weight: number): Piece[][] {
  const lines: Piece[][] = [[]];
  let x = 0;
  const add = (r: Run, text: string, w: number) => {
    const line = lines[lines.length - 1];
    const last = line[line.length - 1];
    if (last && same(last, r)) {
      last.text += text;
      last.w += w;
    } else line.push({ ...r, text, w });
    x += w;
  };
  const breakLine = () => {
    const line = lines[lines.length - 1];
    const last = line[line.length - 1];
    if (last?.text.endsWith(" ")) {
      ctx.font = runFont(last, size, weight);
      last.text = last.text.trimEnd();
      last.w = ctx.measureText(last.text).width;
    }
    lines.push([]);
    x = 0;
  };
  for (const r of runs) {
    ctx.font = runFont(r, size, weight);
    for (const tok of r.text.split(/(\n| +)/)) {
      if (!tok) continue;
      if (tok === "\n") breakLine();
      else if (tok.trim() === "") {
        if (x > 0) add(r, " ", ctx.measureText(" ").width);
      } else {
        let word = tok;
        if (x > 0 && x + ctx.measureText(word).width > width) breakLine();
        while (ctx.measureText(word).width > width - x) {
          let n = 1;
          while (n < word.length && ctx.measureText(word.slice(0, n + 1)).width <= width - x) n++;
          add(r, word.slice(0, n), ctx.measureText(word.slice(0, n)).width);
          breakLine();
          word = word.slice(n);
        }
        if (word) add(r, word, ctx.measureText(word).width);
      }
    }
  }
  breakLine();
  lines.pop();
  return lines.filter((l, i) => l.length > 0 || (i > 0 && i < lines.length - 1));
}

function drawLine(ctx: Ctx, line: Piece[], x: number, base: number, size: number, weight: number, color: string) {
  for (const p of line) {
    ctx.font = runFont(p, size, weight);
    if (p.code) {
      ctx.fillStyle = C.tint;
      ctx.beginPath();
      ctx.roundRect(x - 5, base - size * 0.8, p.w + 10, size * 1.04, 7);
      ctx.fill();
    }
    ctx.fillStyle = p.link ? C.cobalt : color;
    ctx.fillText(p.text, x, base);
    if (p.link) ctx.fillRect(x, base + 5, p.w, 2.5);
    x += p.w;
  }
}

/** Text as one unit per line. */
function textUnits(ctx: Ctx, runs: Run[], x: number, width: number, size: number, lh: number, weight = 400, color = C.ink): Unit[] {
  return layout(ctx, runs, width, size, weight).map((line) => ({ h: lh, draw: (y) => drawLine(ctx, line, x, y + lh * 0.5 + size * 0.36, size, weight, color) }));
}

function pill(ctx: Ctx, x: number, y: number, h: number, text: string, font: string, bg: string, fg: string, shadow = 3): number {
  ctx.font = font;
  const w = ctx.measureText(text).width + h * 0.9;
  if (shadow) {
    ctx.fillStyle = C.ink;
    ctx.beginPath();
    ctx.roundRect(x + shadow, y + shadow, w, h, h / 2);
    ctx.fill();
  }
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, h / 2);
  ctx.fillStyle = bg;
  ctx.fill();
  ctx.lineWidth = 2.5;
  ctx.strokeStyle = C.ink;
  ctx.stroke();
  ctx.fillStyle = fg;
  ctx.textBaseline = "middle";
  ctx.fillText(text, x + h * 0.45, y + h / 2 + 1);
  ctx.textBaseline = "alphabetic";
  return w;
}

/** The app's mark: a white A on a cobalt tile with a hard shadow. */
function badge(ctx: Ctx, x: number, y: number, size: number) {
  const r = size * 0.25;
  ctx.fillStyle = C.ink;
  ctx.beginPath();
  ctx.roundRect(x + 4, y + 4, size, size, r);
  ctx.fill();
  ctx.fillStyle = C.cobalt;
  ctx.beginPath();
  ctx.roundRect(x, y, size, size, r);
  ctx.fill();
  ctx.lineWidth = 3;
  ctx.strokeStyle = C.ink;
  ctx.stroke();
  ctx.save();
  ctx.translate(x + size * 0.19, y + size * 0.17);
  ctx.scale((size * 0.62) / 64, (size * 0.62) / 64);
  ctx.fillStyle = "#ffffff";
  ctx.fill(new Path2D(LOGO));
  ctx.beginPath();
  ctx.arc(32, 45, 6.5, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

function dots(ctx: Ctx, x: number, y: number, w: number, h: number, step: number, alpha: number) {
  ctx.fillStyle = `rgba(11, 11, 11, ${alpha})`;
  ctx.beginPath();
  for (let i = x + step / 2; i < x + w; i += step) {
    for (let j = y + step / 2; j < y + h; j += step) {
      ctx.moveTo(i + 1.6, j);
      ctx.arc(i, j, 1.6, 0, Math.PI * 2);
    }
  }
  ctx.fill();
}

const gap = (h: number): Unit => ({ h, draw: () => {}, gap: true });

/** A label: a cobalt square, the words in spaced capitals, and a hairline to the edge. */
function labelUnit(ctx: Ctx, text: string, x: number, width: number): Unit {
  const font = `700 20px ${MONO}`;
  ctx.font = font;
  spaced(ctx, 3);
  let t = text.toUpperCase();
  while (t.length > 1 && ctx.measureText(t).width > width - 120) t = t.slice(0, -2) + "…";
  const tw = ctx.measureText(t).width;
  spaced(ctx, 0);
  return {
    h: 52,
    draw: (y) => {
      const mid = y + 30;
      ctx.fillStyle = C.cobalt;
      ctx.fillRect(x, mid - 7, 14, 14);
      ctx.font = font;
      spaced(ctx, 3);
      ctx.fillStyle = C.ink;
      ctx.textBaseline = "middle";
      ctx.fillText(t, x + 28, mid + 1);
      ctx.textBaseline = "alphabetic";
      spaced(ctx, 0);
      ctx.fillStyle = C.hair;
      ctx.fillRect(x + 28 + tw + 20, mid - 1, Math.max(0, width - tw - 48), 2);
    },
  };
}

function listUnits(ctx: Ctx, ordered: boolean, items: Item[], x: number, width: number): Unit[] {
  const out: Unit[] = [];
  let n = 0;
  for (const item of items) {
    const indent = 52 + item.depth * 44;
    const num = item.depth === 0 ? ++n : 0;
    const lines = textUnits(ctx, item.runs, x + indent, width - indent, 30, 46);
    if (lines.length === 0) continue;
    const first = lines[0];
    const cx = x + indent - 30;
    lines[0] = {
      h: first.h,
      draw: (y) => {
        const cy = y + 23;
        if (ordered && num) {
          ctx.beginPath();
          ctx.arc(cx, cy, 17, 0, Math.PI * 2);
          ctx.fillStyle = C.cobalt;
          ctx.fill();
          ctx.lineWidth = 2.5;
          ctx.strokeStyle = C.ink;
          ctx.stroke();
          ctx.font = `700 18px ${MONO}`;
          ctx.fillStyle = "#ffffff";
          ctx.textAlign = "center";
          ctx.textBaseline = "middle";
          ctx.fillText(String(num), cx, cy + 1);
          ctx.textAlign = "left";
          ctx.textBaseline = "alphabetic";
        } else {
          ctx.save();
          ctx.translate(cx, cy);
          ctx.rotate(Math.PI / 4);
          const s = item.depth === 0 ? 7 : 5;
          ctx.fillStyle = item.depth === 0 ? C.cobalt : C.card;
          ctx.fillRect(-s, -s, 2 * s, 2 * s);
          ctx.lineWidth = 2.5;
          ctx.strokeStyle = C.ink;
          ctx.strokeRect(-s, -s, 2 * s, 2 * s);
          ctx.restore();
        }
        first.draw(y);
      },
    };
    out.push(...lines);
    out.push(gap(10));
  }
  out.pop();
  return out;
}

/** Rows between two heavy rules, split by hairlines: a key in spaced capitals beside its value, or table cells. */
function gridUnits(ctx: Ctx, head: string[] | null, rows: string[][], x: number, width: number, keyed: boolean): Unit[] {
  const out: Unit[] = [{ h: 3, draw: (y) => ((ctx.fillStyle = C.ink), ctx.fillRect(x, y, width, 3)) }];
  const cols = Math.min(keyed ? 2 : 4, Math.max(head?.length ?? 0, ...rows.map((r) => r.length)));
  const widths = keyed ? [Math.round(width * 0.34), width - Math.round(width * 0.34)] : Array.from({ length: cols }, () => width / cols);
  const lefts = widths.map((_, i) => x + widths.slice(0, i).reduce((a, b) => a + b, 0));
  const row = (cells: string[], small: boolean, last: boolean): Unit => {
    const laid = cells.slice(0, cols).map((text, i) => {
      const span = keyed && i === 0 && !text ? 0 : widths[i] - 24;
      const isKey = small || (keyed && i === 0);
      if (isKey) spaced(ctx, 1.5);
      const lines = layout(ctx, [{ text: isKey ? text.toUpperCase() : text, bold: keyed && i === 1 }], keyed && !cells[0] && i === 1 ? width : span, isKey ? 19 : 28, isKey ? 700 : 400);
      spaced(ctx, 0);
      return { lines, isKey };
    });
    if (keyed && !cells[0]) laid[0].lines = [];
    const h = Math.max(...laid.map((c) => c.lines.length * (c.isKey ? 30 : 42)), 30) + 34;
    return {
      h,
      draw: (y) => {
        laid.forEach((c, i) => {
          const left = keyed && !cells[0] ? x : lefts[i];
          c.lines.forEach((line, j) => {
            if (c.isKey) {
              ctx.font = `700 19px ${MONO}`;
              spaced(ctx, 1.5);
              ctx.fillStyle = C.ink2;
              ctx.fillText(line.map((p) => p.text).join(""), left, y + 17 + 28 + j * 30);
              spaced(ctx, 0);
            } else drawLine(ctx, line, left, y + 17 + 30 + j * 42, 28, 400, C.ink);
          });
        });
        if (!last) {
          ctx.fillStyle = C.hair;
          ctx.fillRect(x, y + h - 1, width, 1.5);
        }
      },
    };
  };
  if (head && head.some(Boolean)) out.push(row(head, true, false));
  rows.forEach((r, i) => out.push(row(r, false, i === rows.length - 1)));
  out.push({ h: 3, draw: (y) => ((ctx.fillStyle = C.ink), ctx.fillRect(x, y, width, 3)) });
  return out;
}

function codeUnits(ctx: Ctx, text: string, x: number, width: number): Unit[] {
  const font = `500 23px ${MONO}`;
  ctx.font = font;
  // Code keeps its own line breaks and spaces; a line wider than the block wraps at the edge.
  const raw = text.replace(/\s+$/, "").replace(/\t/g, "  ").split("\n");
  const wrapped: string[] = [];
  for (const l of raw) {
    let rest = l;
    do {
      let n = rest.length;
      while (n > 1 && ctx.measureText(rest.slice(0, n)).width > width - 56) n--;
      wrapped.push(rest.slice(0, n));
      rest = rest.slice(n);
    } while (rest);
  }
  const shown = wrapped.slice(0, 40);
  return shown.map((l, i) => {
    const first = i === 0;
    const last = i === shown.length - 1;
    const h = 34 + (first ? 26 : 0) + (last ? 26 : 0);
    return {
      h,
      draw: (y) => {
        ctx.fillStyle = C.ink;
        ctx.beginPath();
        ctx.roundRect(x, y, width, h + (last ? 0 : 1), [first ? 16 : 0, first ? 16 : 0, last ? 16 : 0, last ? 16 : 0]);
        ctx.fill();
        ctx.font = font;
        ctx.fillStyle = C.paper;
        ctx.fillText(l, x + 28, y + (first ? 26 : 0) + 25);
      },
    };
  });
}

function quoteUnit(ctx: Ctx, title: string, runs: Run[], x: number, width: number): Unit {
  const inner = width - 64;
  const head = title ? layout(ctx, [{ text: title, bold: true }], inner, 29, 700) : [];
  const body = layout(ctx, runs, inner, 28, 400).slice(0, 14);
  const h = 56 + head.length * 42 + body.length * 42 + (head.length && body.length ? 6 : 0);
  return {
    h: h + 6,
    draw: (y) => {
      ctx.fillStyle = C.ink;
      ctx.beginPath();
      ctx.roundRect(x + 5, y + 5, width - 5, h, 18);
      ctx.fill();
      ctx.fillStyle = C.tint;
      ctx.beginPath();
      ctx.roundRect(x, y, width - 5, h, 18);
      ctx.fill();
      ctx.lineWidth = 3;
      ctx.strokeStyle = C.ink;
      ctx.stroke();
      let ty = y + 28;
      head.forEach((l) => (drawLine(ctx, l, x + 32, ty + 31, 29, 700, C.ink), (ty += 42)));
      if (head.length) ty += 6;
      body.forEach((l) => (drawLine(ctx, l, x + 32, ty + 30, 28, 400, C.ink), (ty += 42)));
    },
  };
}

function chipUnits(ctx: Ctx, items: string[], x: number, width: number): Unit[] {
  const font = `600 24px ${SANS}`;
  const fills = [C.mint, C.peri, C.blush, C.card];
  ctx.font = font;
  const rows: { text: string; x: number; fill: string }[][] = [[]];
  let cx = 0;
  items.forEach((text, i) => {
    let t = text;
    while (t.length > 1 && ctx.measureText(t).width + 44 > width) t = t.slice(0, -2) + "…";
    const w = ctx.measureText(t).width + 44;
    if (cx > 0 && cx + w > width) {
      rows.push([]);
      cx = 0;
    }
    rows[rows.length - 1].push({ text: t, x: cx, fill: fills[i % fills.length] });
    cx += w + 14;
  });
  return rows.map((row) => ({ h: 64, draw: (y) => row.forEach((c) => pill(ctx, x + c.x, y + 6, 50, c.text, font, c.fill, C.ink)) }));
}

function blockUnits(ctx: Ctx, b: Block, x: number, width: number): Unit[] {
  switch (b.kind) {
    case "heading": {
      const size = b.level === 1 ? 46 : 38;
      const lh = b.level === 1 ? 58 : 50;
      const lines = layout(ctx, [{ text: b.text, bold: true }], width, size, 700);
      // A short pink bar under the last line, clear of every letter.
      return lines.map((line, i) => ({
        h: i === lines.length - 1 ? lh + 18 : lh,
        draw: (y) => {
          const base = y + lh * 0.5 + size * 0.36;
          drawLine(ctx, line, x, base, size, 700, C.ink);
          if (i < lines.length - 1) return;
          ctx.fillStyle = C.mark;
          ctx.beginPath();
          ctx.roundRect(x, base + size * 0.34, 96, 8, 4);
          ctx.fill();
        },
      }));
    }
    case "label":
      return [labelUnit(ctx, b.text, x, width)];
    case "para":
      return b.muted ? textUnits(ctx, b.runs, x, width, 26, 40, 400, C.ink2) : textUnits(ctx, b.runs, x, width, 30, 46);
    case "list":
      return listUnits(ctx, b.ordered, b.items, x, width);
    case "rows":
      return gridUnits(ctx, null, b.rows, x, width, true);
    case "table":
      return gridUnits(ctx, b.head, b.rows, x, width, false);
    case "code":
      return codeUnits(ctx, b.text, x, width);
    case "quote":
      return [quoteUnit(ctx, b.title, b.runs, x, width)];
    case "chips":
      return chipUnits(ctx, b.items, x, width);
    case "rule":
      return [{ h: 20, draw: (y) => ((ctx.fillStyle = C.hair), ctx.fillRect(x, y + 9, width, 2)) }];
  }
}

/** The picture of one exchange as a PNG, or null when this browser cannot draw it. */
export async function renderShareImage(question: string, answer: string, date: Date): Promise<Blob | null> {
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx || typeof ctx.roundRect !== "function") return null;
  try {
    await Promise.all([document.fonts?.load(`400 30px ${SANS}`), document.fonts?.load(`700 30px ${SANS}`)]);
  } catch {
    // the fallback face draws the same words
  }
  const blocks = await answerBlocks(answer);

  const cardX = PAD;
  const cardW = W - 2 * PAD - 12;
  const inX = cardX + 56;
  const inW = cardW - 112;
  const qFont = `700 54px ${SANS}`;
  ctx.font = qFont;
  spaced(ctx, -1.5);
  const q = wrap(plainText(question), W - 2 * PAD - 12, (s) => ctx.measureText(s).width, 5);
  spaced(ctx, 0);

  const units: Unit[] = [];
  blocks.forEach((b) => {
    const u = blockUnits(ctx, b, inX, inW);
    if (u.length === 0) return;
    if (units.length > 0) units.push(gap(b.kind === "heading" || b.kind === "label" ? 34 : 24));
    units.push(...u);
  });
  if (units.length === 0) units.push(...textUnits(ctx, [{ text: "(no text)" }], inX, inW, 30, 46, 400, C.ink2));

  const top = PAD + 64 + 72; // the brand row
  const qH = 50 + 28 + q.lines.length * 64;
  const strip = 96;
  const chrome = top + qH + 64 + strip + 52 + 60 + 52 + 30 + PAD;
  let room = MAX_H - chrome;
  const shown: Unit[] = [];
  let cut = q.cut;
  for (const u of units) {
    if (u.h > room) {
      cut = true;
      break;
    }
    shown.push(u);
    room -= u.h;
  }
  while (shown.length > 0 && shown[shown.length - 1].gap) shown.pop();
  const bodyH = shown.reduce((n, u) => n + u.h, 0) + (cut ? 40 : 0);
  const cardH = strip + 52 + bodyH + 60;
  const H = top + qH + 64 + cardH + 12 + 52 + 30 + PAD - 12;
  canvas.width = W;
  canvas.height = H;

  ctx.fillStyle = C.paper;
  ctx.fillRect(0, 0, W, H);
  dots(ctx, 0, 0, W, H, 28, 0.1);

  // Brand row: the mark, the name, the date.
  badge(ctx, PAD, PAD, 64);
  ctx.font = `700 44px ${SANS}`;
  spaced(ctx, -1);
  ctx.fillStyle = C.ink;
  ctx.fillText("Antares", PAD + 88, PAD + 47);
  spaced(ctx, 0);
  const day = date.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
  ctx.font = `700 21px ${MONO}`;
  const dw = ctx.measureText(day).width + 44;
  pill(ctx, W - PAD - dw - 3, PAD + 10, 44, day, `700 21px ${MONO}`, C.card, C.ink);

  // The question: a tilted sticker, then the words as a headline.
  let y = top;
  ctx.save();
  ctx.translate(PAD + 6, y + 22);
  ctx.rotate(-0.05);
  pill(ctx, 0, -22, 44, "ASKED", `700 20px ${MONO}`, C.mark, C.ink);
  ctx.restore();
  y += 50 + 28;
  ctx.font = qFont;
  spaced(ctx, -1.5);
  q.lines.forEach((l, i) => {
    const base = y + i * 64 + 48;
    ctx.fillStyle = C.ink;
    ctx.fillText(l, PAD, base);
  });
  spaced(ctx, 0);
  y += q.lines.length * 64 + 64;

  // The answer card: a dotted periwinkle strip with the mark, then the answer.
  ctx.fillStyle = C.ink;
  ctx.beginPath();
  ctx.roundRect(cardX + 12, y + 12, cardW, cardH, 28);
  ctx.fill();
  ctx.fillStyle = C.card;
  ctx.beginPath();
  ctx.roundRect(cardX, y, cardW, cardH, 28);
  ctx.fill();
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(cardX, y, cardW, strip, [28, 28, 0, 0]);
  ctx.clip();
  ctx.fillStyle = C.peri;
  ctx.fillRect(cardX, y, cardW, strip);
  dots(ctx, cardX, y, cardW, strip, 14, 0.16);
  ctx.restore();
  ctx.fillStyle = C.ink;
  ctx.fillRect(cardX, y + strip - 1.5, cardW, 3);
  ctx.beginPath();
  ctx.roundRect(cardX, y, cardW, cardH, 28);
  ctx.lineWidth = 3.5;
  ctx.strokeStyle = C.ink;
  ctx.stroke();
  badge(ctx, cardX + 32, y + 24, 46);
  ctx.font = `700 22px ${MONO}`;
  spaced(ctx, 4);
  ctx.fillStyle = C.ink;
  ctx.fillText("ANSWER", cardX + 32 + 46 + 22, y + 56);
  spaced(ctx, 0);

  let by = y + strip + 52;
  for (const u of shown) {
    u.draw(by);
    by += u.h;
  }
  if (cut) {
    const fade = ctx.createLinearGradient(0, by - 140, 0, by + 30);
    fade.addColorStop(0, "rgba(255, 255, 255, 0)");
    fade.addColorStop(1, C.card);
    ctx.fillStyle = fade;
    ctx.fillRect(cardX + 4, by - 140, cardW - 8, 170);
    ctx.font = `700 19px ${MONO}`;
    spaced(ctx, 2);
    ctx.fillStyle = C.ink2;
    ctx.fillText("SHORTENED TO FIT", inX, by + 30);
    spaced(ctx, 0);
  }

  ctx.font = `400 21px ${MONO}`;
  ctx.fillStyle = C.ink2;
  ctx.fillText("Shared from Antares", PAD, H - PAD);
  return await new Promise<Blob | null>((done) => canvas.toBlob(done, "image/png"));
}
