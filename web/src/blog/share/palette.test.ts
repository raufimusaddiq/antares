import { describe, expect, it } from "vitest";
import { BLOG_PALETTE, CHAT_PALETTE } from "../../cardKit";

// WCAG 2.x relative luminance and contrast ratio.
const lum = (hex: string) => {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a: string, b: string) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

describe("the blog picture's colours", () => {
  // Spec 002 FR-020, SC-008: every text on its background is at least AA.
  it.each([
    ["ink on the beige background", BLOG_PALETTE.ink, BLOG_PALETTE.paper],
    ["ink on the white card", BLOG_PALETTE.ink, BLOG_PALETTE.card],
    ["ink on the mint strip", BLOG_PALETTE.ink, BLOG_PALETTE.mint],
    ["white on the cobalt sticker", "#ffffff", BLOG_PALETTE.cobalt],
    ["ink on the pink mark", BLOG_PALETTE.badgeInk, BLOG_PALETTE.badge],
  ])("%s has AA contrast", (_n, fg, bg) => {
    expect(ratio(fg, bg)).toBeGreaterThanOrEqual(4.5);
  });

  it("differs from the chat picture in its background and its strip, so the two are told apart", () => {
    expect(BLOG_PALETTE.paper).not.toBe(CHAT_PALETTE.paper);
    expect(BLOG_PALETTE.mint).not.toBe(CHAT_PALETTE.peri);
    expect(BLOG_PALETTE.badge).not.toBe(CHAT_PALETTE.badge);
  });
});
