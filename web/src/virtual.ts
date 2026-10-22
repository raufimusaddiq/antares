// Windowing arithmetic for the message list (PRD PERF-001). Pure functions, so
// the scroll-anchor rule that keeps the visible message still when an older page
// is prepended can be tested without a browser.

export interface Layout {
  /** offsets[i] is the top of item i; offsets[n] is the total height. */
  offsets: number[];
}

/** Heights are measured per key; an unmeasured item is assumed to be `estimate`. */
export function layout(keys: readonly string[], heights: ReadonlyMap<string, number>, estimate: number): Layout {
  const offsets = new Array<number>(keys.length + 1);
  let y = 0;
  for (let i = 0; i < keys.length; i++) {
    offsets[i] = y;
    y += heights.get(keys[i]) ?? estimate;
  }
  offsets[keys.length] = y;
  return { offsets };
}

/** First index whose bottom edge is below `y` (binary search over offsets). */
export function indexAt(l: Layout, y: number): number {
  const n = l.offsets.length - 1;
  let lo = 0;
  let hi = n;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (l.offsets[mid + 1] <= y) lo = mid + 1;
    else hi = mid;
  }
  return Math.min(lo, Math.max(0, n - 1));
}

/** The half-open index range to mount for a viewport, with `overscan` px on each side. */
export function visibleRange(l: Layout, scrollTop: number, viewport: number, overscan: number): [number, number] {
  const n = l.offsets.length - 1;
  if (n === 0) return [0, 0];
  const start = indexAt(l, Math.max(0, scrollTop - overscan));
  let end = indexAt(l, scrollTop + viewport + overscan) + 1;
  end = Math.min(n, Math.max(end, start + 1));
  return [start, end];
}

/** The item a viewport top sits in, and how far into it. */
export interface Anchor {
  key: string;
  /** px between the item's top and the viewport's top (may be negative or larger than the item). */
  delta: number;
}

export function anchorAt(keys: readonly string[], l: Layout, scrollTop: number): Anchor | null {
  if (keys.length === 0) return null;
  const i = indexAt(l, scrollTop);
  return { key: keys[i], delta: scrollTop - l.offsets[i] };
}

/** Where scrollTop must be for the anchor to stay exactly where it was on screen, or null if it is gone. */
export function scrollTopFor(keys: readonly string[], l: Layout, a: Anchor): number | null {
  const i = keys.indexOf(a.key);
  return i < 0 ? null : l.offsets[i] + a.delta;
}

/** Within `slack` px of the end counts as "following the newest message". */
export function atBottom(scrollTop: number, viewport: number, total: number, slack = 24): boolean {
  return total - (scrollTop + viewport) <= slack;
}
