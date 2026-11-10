import { BLOG_PALETTE, MONO, PAD, SANS, STORY_H, W, makeKit, spaced } from "../../cardKit";
import { COVER_H, INNER_W, INNER_X, PANEL_W, PANEL_X, SANS_FAMILY, STRIP, TITLE_W, planPostCard, type PostCardInput } from "./planPostCard";

// A post as a 9:16 picture (spec 002): drawn in the reader's own browser from what the post page already shows, in the
// chat picture's style (cardKit) and the blog palette. Nothing is sent anywhere and nothing is stored.

export interface PostCardData extends PostCardInput {
  /** The cover's address (this site's own), or empty. */
  coverUrl: string;
  date: Date;
  /** The short address printed at the bottom: host and path. */
  address: string;
}

const P = BLOG_PALETTE;
const { pill, badge, dots } = makeKit(P);

/** The cover as a bitmap, or null when it cannot be loaded (the card then has none, and says nothing of it). */
async function loadCover(url: string): Promise<HTMLImageElement | null> {
  if (!url) return null;
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    return img.naturalWidth > 0 ? img : null;
  } catch {
    return null;
  }
}

/** The picture, or null when this browser cannot draw it. Always exactly 1080 x 1920. */
export async function renderPostCard(data: PostCardData): Promise<Blob | null> {
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx || typeof ctx.roundRect !== "function") return null;
  try {
    await Promise.all([document.fonts?.load(`400 30px ${SANS}`), document.fonts?.load(`700 30px ${SANS}`)]);
  } catch {
    // the fallback face draws the same words
  }
  const cover = await loadCover(data.coverUrl);
  canvas.width = W;
  canvas.height = STORY_H;
  const measure = (font: string, text: string, spacing = 0) => {
    ctx.font = font;
    spaced(ctx, spacing);
    const w = ctx.measureText(text).width;
    spaced(ctx, 0);
    return w;
  };
  const plan = planPostCard({ ...data, hasCover: cover !== null }, measure);
  const rtl = plan.rtl;
  const edgeL = PAD;
  const edgeR = W - PAD;
  ctx.direction = rtl ? "rtl" : "ltr";
  ctx.textAlign = rtl ? "right" : "left";
  const at = (left: number, width: number) => (rtl ? left + width : left);

  ctx.fillStyle = P.paper;
  ctx.fillRect(0, 0, W, STORY_H);
  dots(ctx, 0, 0, W, STORY_H, 28, 0.1);

  // Brand row: the mark, the name, the date.
  ctx.direction = "ltr";
  ctx.textAlign = "left";
  badge(ctx, PAD, PAD, 64);
  ctx.font = `700 44px ${SANS}`;
  spaced(ctx, -1);
  ctx.fillStyle = P.ink;
  ctx.fillText("Antares", PAD + 88, PAD + 47);
  spaced(ctx, 0);
  const day = data.date.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
  ctx.font = `700 21px ${MONO}`;
  const dw = ctx.measureText(day).width + 44;
  pill(ctx, W - PAD - dw - 3, PAD + 10, 44, day, `700 21px ${MONO}`, P.card, P.ink);

  // The sticker, tilted.
  ctx.save();
  ctx.translate(PAD + 6, 208 + 22);
  ctx.rotate(-0.05);
  pill(ctx, 0, -22, 44, "POST", `700 20px ${MONO}`, P.cobalt, "#ffffff");
  ctx.restore();

  // The title, then the bar under it and the author.
  ctx.direction = rtl ? "rtl" : "ltr";
  ctx.textAlign = rtl ? "right" : "left";
  ctx.font = `700 ${plan.title.size}px ${SANS}`;
  spaced(ctx, -1.5);
  ctx.fillStyle = P.ink;
  plan.title.lines.forEach((l, i) => ctx.fillText(l, rtl ? edgeR : edgeL, plan.title.y + i * plan.title.lh + plan.title.size * 0.9));
  spaced(ctx, 0);
  ctx.fillStyle = P.cobalt;
  ctx.beginPath();
  ctx.roundRect(rtl ? edgeR - 110 : edgeL, plan.barY, 110, 10, 5);
  ctx.fill();
  ctx.direction = "ltr";
  ctx.textAlign = rtl ? "right" : "left";
  ctx.font = `700 22px ${MONO}`;
  spaced(ctx, 3);
  ctx.fillStyle = P.ink;
  ctx.fillText(`BY ${data.authorName}`.toUpperCase(), rtl ? edgeR : edgeL, plan.authorY + 20);
  spaced(ctx, 0);

  // The card: hard shadow, white body, the dotted mint strip with the mark and its label.
  const { y: py, h: ph } = plan.panel;
  ctx.fillStyle = P.ink;
  ctx.beginPath();
  ctx.roundRect(PANEL_X + 12, py + 12, PANEL_W, ph, 28);
  ctx.fill();
  ctx.fillStyle = P.card;
  ctx.beginPath();
  ctx.roundRect(PANEL_X, py, PANEL_W, ph, 28);
  ctx.fill();
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(PANEL_X, py, PANEL_W, STRIP, [28, 28, 0, 0]);
  ctx.clip();
  ctx.fillStyle = P.mint;
  ctx.fillRect(PANEL_X, py, PANEL_W, STRIP);
  dots(ctx, PANEL_X, py, PANEL_W, STRIP, 14, 0.16);
  ctx.restore();
  ctx.fillStyle = P.ink;
  ctx.fillRect(PANEL_X, py + STRIP - 1.5, PANEL_W, 3);
  ctx.beginPath();
  ctx.roundRect(PANEL_X, py, PANEL_W, ph, 28);
  ctx.lineWidth = 3.5;
  ctx.strokeStyle = P.ink;
  ctx.stroke();
  badge(ctx, PANEL_X + 32, py + 24, 46);
  ctx.direction = "ltr";
  ctx.textAlign = "left";
  ctx.font = `700 22px ${MONO}`;
  spaced(ctx, 4);
  ctx.fillStyle = P.ink;
  ctx.fillText("SUMMARY", PANEL_X + 32 + 46 + 22, py + 56);
  spaced(ctx, 0);

  // The cover, in a thick frame, cropped to fill it.
  let y = py + STRIP + 52;
  if (plan.cover && cover) {
    ctx.save();
    ctx.beginPath();
    ctx.roundRect(INNER_X, y, INNER_W, COVER_H, 16);
    ctx.clip();
    const s = Math.max(INNER_W / cover.naturalWidth, COVER_H / cover.naturalHeight);
    const w = cover.naturalWidth * s;
    const h = cover.naturalHeight * s;
    ctx.drawImage(cover, INNER_X + (INNER_W - w) / 2, y + (COVER_H - h) / 2, w, h);
    ctx.restore();
    ctx.beginPath();
    ctx.roundRect(INNER_X, y, INNER_W, COVER_H, 16);
    ctx.lineWidth = 3;
    ctx.strokeStyle = P.ink;
    ctx.stroke();
  }

  // The summary, then the highlights after diamonds.
  ctx.direction = rtl ? "rtl" : "ltr";
  ctx.textAlign = rtl ? "right" : "left";
  ctx.font = `400 ${plan.summary.size}px ${SANS_FAMILY}`;
  ctx.fillStyle = P.ink;
  plan.summary.lines.forEach((l, i) => ctx.fillText(l, at(INNER_X, 0) + (rtl ? INNER_W : 0), plan.summary.y + i * plan.summary.lh + plan.summary.size * 1.0));
  const hs = plan.highlights.size;
  plan.highlights.items.forEach((it) => {
    const cx = rtl ? INNER_X + INNER_W - 8 : INNER_X + 8;
    const cy = it.y + plan.highlights.lh * 0.5;
    ctx.save();
    ctx.translate(cx, cy);
    ctx.rotate(Math.PI / 4);
    const r = hs * 0.2;
    ctx.fillStyle = P.mark;
    ctx.fillRect(-r, -r, 2 * r, 2 * r);
    ctx.lineWidth = 2.5;
    ctx.strokeStyle = P.ink;
    ctx.strokeRect(-r, -r, 2 * r, 2 * r);
    ctx.restore();
    ctx.font = `700 ${hs}px ${SANS_FAMILY}`;
    ctx.fillStyle = P.ink;
    it.lines.forEach((l, i) => ctx.fillText(l, rtl ? INNER_X + INNER_W - 50 : INNER_X + 50, it.y + i * plan.highlights.lh + hs * 1.0));
  });

  // The address and the app's name.
  ctx.direction = "ltr";
  ctx.textAlign = "left";
  ctx.font = `400 21px ${MONO}`;
  ctx.fillStyle = P.ink;
  ctx.fillText(data.address, PAD, 1834);
  ctx.fillText("Shared from Antares", PAD, 1866);
  return await new Promise<Blob | null>((done) => canvas.toBlob(done, "image/png"));
}

export { TITLE_W };
