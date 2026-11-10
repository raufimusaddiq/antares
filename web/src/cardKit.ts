// The drawing kit behind the pictures Antares makes of a chat answer (#250, #261) and of a blog post (spec 002): the canvas
// text layout, the pieces of the app's look (the mark, pills, dotted fields) and a palette. Everything that depends on a colour
// is made from a Palette by makeKit, so the chat picture and the post picture share one set of shapes and differ in colour.

import type { Run } from "./shareBlocks";

export const W = 1080;
export const PAD = 72;
export const STORY_H = 1920; // 9:16, the shape a phone shows whole
export const SANS = '"Space Grotesk Variable", "Space Grotesk Fallback", system-ui, sans-serif';
export const MONO = 'ui-monospace, "SF Mono", Menlo, Consolas, monospace';
export const LOGO = "M28 5h8l24 54H49L32 20 15 59H4z"; // the A of public/icons/mark-light.svg; its dot is drawn as an arc

export type Palette = {
  paper: string;
  card: string;
  ink: string;
  ink2: string;
  hair: string;
  cobalt: string;
  mark: string;
  peri: string;
  tint: string;
  mint: string;
  blush: string;
  /** The mark's tile and the A on it. */
  badge: string;
  badgeInk: string;
  /** The colour of a link in drawn text. */
  link: string;
};

/** The chat picture's colours (#250): paper, with cobalt and pink accents. Its pixels must never change. */
export const CHAT_PALETTE: Palette = {
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
  badge: "#2d4bff",
  badgeInk: "#ffffff",
  link: "#2d4bff",
};


export type Ctx = CanvasRenderingContext2D;
export type Piece = Run & { w: number };
/** One slice of the answer: a line of text, a row of a table, a band of a code block. The picture is cut between slices. */
export type Unit = { h: number; draw: (y: number) => void; gap?: boolean };

export function spaced(ctx: Ctx, px: number) {
  if ("letterSpacing" in ctx) ctx.letterSpacing = `${px}px`;
}

export const runFont = (r: Run, size: number, weight: number) => (r.code ? `500 ${Math.round(size * 0.84)}px ${MONO}` : `${r.bold ? 700 : weight} ${size}px ${SANS}`);
const same = (a: Run, b: Run) => !!a.bold === !!b.bold && !!a.code === !!b.code && !!a.link === !!b.link;

/** Styled text broken into lines of at most `width`, at spaces and, for a word wider than a line, inside it. */
export function layout(ctx: Ctx, runs: Run[], width: number, size: number, weight: number): Piece[][] {
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

export const gap = (h: number): Unit => ({ h, draw: () => {}, gap: true });

/**
 * The blog post picture's colours (spec 002, research R4; drawn in the design file's "Blog share" page): a warm beige
 * background, a white card with a mint strip, pink for the mark and the bullets, cobalt for the "POST" sticker and the bar
 * under the title. All of them are colours of the Antares variable set, and the picture is always light.
 */
export const BLOG_PALETTE: Palette = {
  paper: "#e9e4d4",
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
  badge: "#ff6fae",
  badgeInk: "#0b0b0b",
  link: "#2d4bff",
};

/** The shapes that carry a colour, made for one palette. */
export function makeKit(pal: Palette) {
  function drawLine(ctx: Ctx, line: Piece[], x: number, base: number, size: number, weight: number, color: string) {
    for (const p of line) {
      ctx.font = runFont(p, size, weight);
      if (p.code) {
        ctx.fillStyle = pal.tint;
        ctx.beginPath();
        ctx.roundRect(x - 5, base - size * 0.8, ctx.measureText(p.text.trimEnd()).width + 10, size * 1.04, 7);
        ctx.fill();
      }
      ctx.fillStyle = p.link ? pal.link : color;
      ctx.fillText(p.text, x, base);
      if (p.link) ctx.fillRect(x, base + 5, p.w, 2.5);
      x += p.w;
    }
  }

  /** Text as one unit per line. */
  function textUnits(ctx: Ctx, runs: Run[], x: number, width: number, size: number, lh: number, weight = 400, color = pal.ink): Unit[] {
    return layout(ctx, runs, width, size, weight).map((line) => ({ h: lh, draw: (y) => drawLine(ctx, line, x, y + lh * 0.5 + size * 0.36, size, weight, color) }));
  }

  function pill(ctx: Ctx, x: number, y: number, h: number, text: string, font: string, bg: string, fg: string, shadow = 3): number {
    ctx.font = font;
    const w = ctx.measureText(text).width + h * 0.9;
    if (shadow) {
      ctx.fillStyle = pal.ink;
      ctx.beginPath();
      ctx.roundRect(x + shadow, y + shadow, w, h, h / 2);
      ctx.fill();
    }
    ctx.beginPath();
    ctx.roundRect(x, y, w, h, h / 2);
    ctx.fillStyle = bg;
    ctx.fill();
    ctx.lineWidth = 2.5;
    ctx.strokeStyle = pal.ink;
    ctx.stroke();
    ctx.fillStyle = fg;
    ctx.textBaseline = "middle";
    ctx.fillText(text, x + h * 0.45, y + h / 2 + 1);
    ctx.textBaseline = "alphabetic";
    return w;
  }

  /** The app's mark: the A on a tile with a hard shadow, in the palette's colours. */
  function badge(ctx: Ctx, x: number, y: number, size: number) {
    const r = size * 0.25;
    ctx.fillStyle = pal.ink;
    ctx.beginPath();
    ctx.roundRect(x + 4, y + 4, size, size, r);
    ctx.fill();
    ctx.fillStyle = pal.badge;
    ctx.beginPath();
    ctx.roundRect(x, y, size, size, r);
    ctx.fill();
    ctx.lineWidth = 3;
    ctx.strokeStyle = pal.ink;
    ctx.stroke();
    ctx.save();
    ctx.translate(x + size * 0.19, y + size * 0.17);
    ctx.scale((size * 0.62) / 64, (size * 0.62) / 64);
    ctx.fillStyle = pal.badgeInk;
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

  return { drawLine, textUnits, pill, badge, dots };
}
