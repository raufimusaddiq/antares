import { describe, expect, it } from "vitest";
import { anchorAt, atBottom, indexAt, layout, scrollTopFor, visibleRange } from "./virtual";

const keys = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => `t${from + i}`);

describe("windowing", () => {
  it("mounts a bounded window of a 1000-turn list at any scroll position", () => {
    const k = keys(0, 1000);
    const heights = new Map(k.map((id, i) => [id, 60 + (i % 7) * 20] as const));
    const l = layout(k, heights, 90);
    for (const top of [0, 1234, l.offsets[500], l.offsets[1000] - 800]) {
      const [s, e] = visibleRange(l, top, 800, 600);
      expect(e - s).toBeLessThan(40);
      // the visible band is fully covered
      expect(l.offsets[s]).toBeLessThanOrEqual(Math.max(0, top - 600) + 1e-9);
      expect(l.offsets[e]).toBeGreaterThanOrEqual(Math.min(l.offsets[1000], top + 800 + 600));
    }
  });

  it("handles empty, single and unmeasured lists", () => {
    expect(visibleRange(layout([], new Map(), 80), 0, 800, 600)).toEqual([0, 0]);
    const one = layout(["a"], new Map(), 80);
    expect(visibleRange(one, 0, 800, 600)).toEqual([0, 1]);
    expect(indexAt(one, 5000)).toBe(0);
    expect(layout(["a", "b"], new Map(), 80).offsets).toEqual([0, 80, 160]);
  });

  it("measured heights replace the estimate", () => {
    expect(layout(["a", "b", "c"], new Map([["b", 300]]), 80).offsets).toEqual([0, 80, 380, 460]);
  });
});

describe("prepending an older page keeps the visible message where it was", () => {
  const heightsFor = (ids: string[]) => new Map(ids.map((id) => [id, 50 + (Number(id.slice(1)) % 5) * 31] as const));

  it("for every scroll position and page size", () => {
    const newest = keys(50, 100); // newest page loaded first
    for (const olderCount of [1, 7, 50]) {
      for (let top = 0; top < 2500; top += 37) {
        const older = keys(50 - olderCount, 50);
        const before = layout(newest, heightsFor(newest), 90);
        const a = anchorAt(newest, before, top)!;
        const all = [...older, ...newest];
        const after = layout(all, heightsFor(all), 90);
        const next = scrollTopFor(all, after, a)!;
        // the anchor item sits at the same distance from the viewport top as before
        const screenYBefore = before.offsets[newest.indexOf(a.key)] - top;
        const screenYAfter = after.offsets[all.indexOf(a.key)] - next;
        expect(screenYAfter).toBeCloseTo(screenYBefore, 9);
      }
    }
  });

  it("also when the prepended page is measured later than it is inserted", () => {
    const newest = keys(50, 80);
    const a = anchorAt(newest, layout(newest, heightsFor(newest), 90), 400)!;
    const all = [...keys(30, 50), ...newest];
    // first pass: estimates only; second pass: real heights. Re-anchoring after each stays put.
    for (const h of [new Map(newest.map((k) => [k, heightsFor(newest).get(k)!] as const)), heightsFor(all)]) {
      const l = layout(all, h, 90);
      const y = scrollTopFor(all, l, a)!;
      expect(l.offsets[all.indexOf(a.key)] - y).toBeCloseTo(-a.delta, 9);
    }
  });

  it("reports a vanished anchor instead of guessing", () => {
    const k = keys(0, 10);
    const a = anchorAt(k, layout(k, new Map(), 80), 300)!;
    expect(scrollTopFor(keys(20, 30), layout(keys(20, 30), new Map(), 80), a)).toBeNull();
  });
});

describe("following the newest message", () => {
  it("is true only near the end", () => {
    expect(atBottom(1200, 800, 2000)).toBe(true);
    expect(atBottom(1190, 800, 2000)).toBe(true);
    expect(atBottom(1000, 800, 2000)).toBe(false);
  });
});
