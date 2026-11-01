// An answer as a picture (#250): the question and the reply drawn as text on a canvas, in the browser. Nothing is
// sent anywhere and no page markup is rasterised, so the picture holds only the words, the date and the app's name.

import { splitMedia } from "./mediaTags";

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

const W = 1080;
const PAD = 64;
const BOX = 40; // inside a card
const MAX_H = 4800; // well inside every browser's canvas limit
const FONT = '"Space Grotesk Variable", "Space Grotesk Fallback", system-ui, sans-serif';
const MONO = 'ui-monospace, "SF Mono", Menlo, Consolas, monospace';
const C = { canvas: "#f4f1e8", card: "#ffffff", ink: "#0b0b0b", ink2: "#4a463d", mark: "#ff6fae", cobalt: "#2d4bff" };

function card(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number) {
  const path = () => {
    ctx.beginPath();
    ctx.roundRect(x, y, w, h, 20);
  };
  ctx.fillStyle = C.ink;
  ctx.save();
  ctx.translate(8, 8);
  path();
  ctx.fill();
  ctx.restore();
  ctx.fillStyle = C.card;
  path();
  ctx.fill();
  ctx.lineWidth = 4;
  ctx.strokeStyle = C.ink;
  ctx.stroke();
}

function pill(ctx: CanvasRenderingContext2D, x: number, y: number, text: string, bg: string, fg: string) {
  ctx.font = `700 22px ${MONO}`;
  const w = ctx.measureText(text).width + 32;
  ctx.beginPath();
  ctx.roundRect(x, y, w, 40, 20);
  ctx.fillStyle = bg;
  ctx.fill();
  ctx.lineWidth = 3;
  ctx.strokeStyle = C.ink;
  ctx.stroke();
  ctx.fillStyle = fg;
  ctx.textBaseline = "middle";
  ctx.fillText(text, x + 16, y + 21);
  ctx.textBaseline = "alphabetic";
}

/** The picture of one exchange as a PNG, or null when this browser cannot draw it. */
export async function renderShareImage(question: string, answer: string, date: Date): Promise<Blob | null> {
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx || typeof ctx.roundRect !== "function") return null;
  try {
    await document.fonts?.load(`400 32px ${FONT}`);
  } catch {
    // the fallback face draws the same words
  }
  const inner = W - 2 * PAD - 2 * BOX;
  const qFont = `600 34px ${FONT}`;
  const aFont = `400 31px ${FONT}`;
  const qLH = 46;
  const aLH = 44;
  const cardH = (lines: number, lh: number) => BOX + 56 + lines * lh + BOX - 12;
  const chrome = PAD + 100 + 48 + 110; // title row, the gap between the cards, the footer
  ctx.font = qFont;
  const q = wrap(plainText(question), inner, (s) => ctx.measureText(s).width, 6);
  const qH = cardH(q.lines.length, qLH);
  ctx.font = aFont;
  const a = wrap(plainText(answer), inner, (s) => ctx.measureText(s).width, Math.max(4, Math.floor((MAX_H - chrome - qH - cardH(0, aLH)) / aLH)));
  const aH = cardH(a.lines.length, aLH);
  const H = chrome + qH + aH;
  canvas.width = W;
  canvas.height = H;

  ctx.fillStyle = C.canvas;
  ctx.fillRect(0, 0, W, H);
  ctx.fillStyle = C.ink;
  ctx.font = `700 40px ${FONT}`;
  ctx.fillText("Antares", PAD, PAD + 40);
  ctx.font = `400 22px ${MONO}`;
  ctx.fillStyle = C.ink2;
  const day = date.toISOString().slice(0, 10);
  ctx.fillText(day, W - PAD - ctx.measureText(day).width, PAD + 36);

  let y = PAD + 100;
  const cw = W - 2 * PAD - 8;
  card(ctx, PAD, y, cw, qH);
  pill(ctx, PAD + BOX, y + BOX - 8, "YOU", C.mark, C.ink);
  ctx.font = qFont;
  ctx.fillStyle = C.ink;
  q.lines.forEach((l, i) => ctx.fillText(l, PAD + BOX, y + BOX + 56 + 30 + i * qLH));

  y += qH + 48;
  card(ctx, PAD, y, cw, aH);
  pill(ctx, PAD + BOX, y + BOX - 8, "ASSISTANT", C.cobalt, "#ffffff");
  ctx.font = aFont;
  ctx.fillStyle = C.ink;
  a.lines.forEach((l, i) => ctx.fillText(l, PAD + BOX, y + BOX + 56 + 28 + i * aLH));

  ctx.font = `400 22px ${MONO}`;
  ctx.fillStyle = C.ink2;
  ctx.fillText(a.cut || q.cut ? "Shortened to fit · shared from Antares" : "Shared from Antares", PAD, H - PAD + 10);
  return await new Promise<Blob | null>((done) => canvas.toBlob(done, "image/png"));
}
