import { describe, expect, it } from "vitest";
import { mergeNewest, prependOlder } from "./history";
import type { Turn } from "./chatApi";

const t = (from: number, to: number): Turn[] => Array.from({ length: to - from }, (_, i) => ({ seq: from + i, role: i % 2 ? "assistant" : "user", text: `m${from + i}` }));
const seqs = (x: Turn[]) => x.map((y) => y.seq);

describe("prependOlder", () => {
  it("puts older turns in front, in order", () => {
    expect(seqs(prependOlder(t(50, 100), t(0, 50)))).toEqual(seqs(t(0, 100)));
  });
  it("never duplicates a turn when a page is delivered twice or overlaps", () => {
    const held = prependOlder(t(50, 100), t(0, 50));
    expect(seqs(prependOlder(held, t(0, 50)))).toEqual(seqs(t(0, 100)));
    expect(seqs(prependOlder(t(50, 100), t(40, 60)))).toEqual(seqs(t(40, 100)));
  });
  it("starts from empty", () => {
    expect(seqs(prependOlder([], t(0, 3)))).toEqual([0, 1, 2]);
  });
});

describe("mergeNewest", () => {
  it("keeps older pages the reader loaded and replaces the tail", () => {
    const held = [...t(0, 100), { seq: 100, role: "user" as const, text: "local" }];
    const r = mergeNewest(held, t(60, 102));
    expect(r.reset).toBe(false);
    expect(seqs(r.turns)).toEqual(seqs(t(0, 102)));
    expect(r.turns[100].text).toBe("m100"); // the server's turn replaced the optimistic one
  });
  it("is the page itself when nothing was held", () => {
    expect(seqs(mergeNewest([], t(10, 20)).turns)).toEqual(seqs(t(10, 20)));
  });
  it("drops held turns that would leave a gap", () => {
    const r = mergeNewest(t(0, 10), t(200, 250));
    expect(r.reset).toBe(true);
    expect(seqs(r.turns)).toEqual(seqs(t(200, 250)));
  });
  it("an empty page changes nothing", () => {
    expect(mergeNewest(t(0, 5), []).turns).toHaveLength(5);
  });
});
