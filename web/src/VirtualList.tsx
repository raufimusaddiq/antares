import { useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { anchorAt, atBottom, layout, scrollTopFor, visibleRange, type Layout } from "./virtual";

// A windowed, variable-height list for the conversation (PRD PERF-001 items 1-2).
//
//  - Only the items near the viewport are mounted. Up to WINDOW_ABOVE items every
//    item is mounted, so an ordinary chat keeps native text selection and find.
//  - Each item is measured in a layout effect, before paint, the first time it is
//    mounted at the current width; later changes are re-read by a ResizeObserver
//    (a width change clears the cache, so every item is measured again). The
//    scroll position is re-derived from an anchor (the item at the viewport top
//    plus the offset into it). A page prepended above, or an item above the
//    viewport changing height, therefore never moves what the reader is looking
//    at. Native scroll anchoring is switched off (CSS) so the two cannot fight.
//  - While the reader is at the bottom the list follows the newest content,
//    including a streaming reply in `footer`; once they scroll up it stops.

export const WINDOW_ABOVE = 100;
const ESTIMATE = 96;
const OVERSCAN = 600;
const NEAR_TOP = 320;

interface Props<T> {
  items: readonly T[];
  itemKey: (item: T) => string;
  render: (item: T, index: number) => ReactNode;
  /** Rendered after the items, inside the scroll area (pending message, live reply). */
  footer?: ReactNode;
  label: string;
  hasMore: boolean;
  loadingMore: boolean;
  /** Status for the top edge, e.g. "Loading older messages…"; may hold a Retry button. */
  topNote: ReactNode;
  onLoadMore: () => void;
  /** Scroll this item (by key) to the top once it is held; once per distinct key. */
  focusKey?: string;
  /** Changes each time the same result is opened again, so it scrolls back even if the key is unchanged. */
  focusNonce?: number;
}

export function VirtualList<T>({ items, itemKey, render, footer, label, hasMore, loadingMore, topNote, onLoadMore, focusKey, focusNonce = 0 }: Props<T>) {
  const scroller = useRef<HTMLDivElement | null>(null);
  const content = useRef<HTMLDivElement | null>(null);
  const heights = useRef(new Map<string, number>()); // per turn key, at measuredWidth
  const measuredWidth = useRef(0);
  // Bumped whenever a measured height changes: the layout below is recomputed only then (or when the items change),
  // not on every scroll frame (#170: an O(n) pass per frame over the whole loaded history).
  const [heightsVersion, bump] = useState(0);
  const [viewport, setViewport] = useState(800);
  const [following, setFollowing] = useState(true);
  // What the last positioning pass saw. The reader's place is always derived from
  // these (the previous layout) and the scroller's live scrollTop, never from the
  // last scroll *event*: a scroll event fires once per frame, so state taken from
  // it can be a frame stale when a render lands, and acting on it would undo the
  // reader's own scroll.
  const placed = useRef<{ keys: readonly string[]; lay: Layout; scrollHeight: number; viewport: number } | null>(null);
  const remeasured = useRef(false);

  const keys = useMemo(() => items.map(itemKey), [items, itemKey]);
  // heights is a ref; heightsVersion stands for its contents.
  const lay = useMemo(() => layout(keys, heights.current, ESTIMATE), [keys, heightsVersion]);
  const total = lay.offsets[keys.length];
  const windowed = keys.length > WINDOW_ABOVE;
  // The first paint is the newest end, where a conversation opens.
  const [top, setTop] = useState(() => Math.max(0, keys.length * ESTIMATE - 800));
  const [start, end] = windowed ? visibleRange(lay, top, viewport, OVERSCAN) : [0, keys.length];

  const latest = useRef({ keys, lay, hasMore, loadingMore, onLoadMore });
  latest.current = { keys, lay, hasMore, loadingMore, onLoadMore };

  const maybeLoadMore = useCallback(() => {
    const el = scroller.current;
    const c = latest.current;
    if (el && c.hasMore && !c.loadingMore && el.scrollTop < NEAR_TOP) c.onLoadMore();
  }, []);

  // Reads the height of each mounted item (all of them, or only those never measured) into the cache; true when one
  // changed.
  const measure = useCallback((onlyNew: boolean) => {
    const el = content.current;
    if (!el) return false;
    let changed = false;
    for (const node of el.querySelectorAll<HTMLElement>("[data-vkey]")) {
      const key = node.dataset.vkey!;
      if (onlyNew && heights.current.has(key)) continue;
      const h = node.getBoundingClientRect().height;
      if (h > 0 && Math.abs((heights.current.get(key) ?? -1) - h) > 0.5) {
        heights.current.set(key, h);
        changed = true;
      }
    }
    return changed;
  }, []);

  // Measure items mounted for the first time at this width, before paint; a changed height re-renders at once, so such
  // an item is never painted at its estimate. Items already measured are re-read by the ResizeObserver below, after
  // layout, so a scroll frame does not re-measure every mounted item (#170).
  useLayoutEffect(() => {
    const changed = measure(true);
    remeasured.current = changed;
    if (changed) bump((v) => v + 1);
  });

  // Keep the reader where they are: at the newest content, or on the message they
  // were reading.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || remeasured.current) return; // heights just changed: this render's layout is stale, the next one decides
    setViewport((v) => (Math.abs(v - el.clientHeight) > 0.5 && el.clientHeight > 0 ? el.clientHeight : v));
    const prev = placed.current;
    // Following the newest content: at the end last time (in last time's coordinates), or just opened.
    const wasFollowing = prev === null || atBottom(el.scrollTop, prev.viewport, prev.scrollHeight);
    if (wasFollowing) {
      if (el.scrollTop + el.clientHeight < el.scrollHeight - 0.5) el.scrollTop = el.scrollHeight;
    } else {
      const a = anchorAt(prev.keys, prev.lay, el.scrollTop);
      const y = a ? scrollTopFor(keys, lay, a) : null;
      if (y !== null && Math.abs(el.scrollTop - y) > 0.5) el.scrollTop = Math.max(0, y);
    }
    placed.current = { keys, lay, scrollHeight: el.scrollHeight, viewport: el.clientHeight };
    setFollowing(atBottom(el.scrollTop, el.clientHeight, el.scrollHeight));
    setTop(el.scrollTop);
    maybeLoadMore();
  });

  // A search result opens at its turn: bring that item to the top once, when it is in the list.
  const focused = useRef<string | undefined>(undefined);
  useLayoutEffect(() => {
    const el = scroller.current;
    const token = `${focusNonce}|${focusKey}`;
    if (!el || focusKey === undefined || focused.current === token) return;
    const i = keys.indexOf(focusKey);
    if (i < 0) return;
    focused.current = token;
    el.scrollTop = Math.max(0, lay.offsets[i]);
    placed.current = { keys, lay, scrollHeight: el.scrollHeight, viewport: el.clientHeight };
    setFollowing(false);
    setTop(el.scrollTop);
  });

  // Content that grows or reflows with no render of ours (images, fonts, a streaming reply, a narrower window): the
  // observer runs after layout, so re-reading every mounted item here forces no extra layout pass.
  useLayoutEffect(() => {
    const el = scroller.current;
    const inner = content.current;
    if (!el || !inner || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      const prev = placed.current;
      if (prev && atBottom(el.scrollTop, prev.viewport, prev.scrollHeight) && el.scrollTop + el.clientHeight < el.scrollHeight - 0.5) {
        el.scrollTop = el.scrollHeight;
        placed.current = { ...prev, scrollHeight: el.scrollHeight };
      }
      // Heights depend on the width: when it changes, forget them all, so an item mounted later is measured afresh
      // rather than placed at the height it had at the old width.
      if (inner.clientWidth !== measuredWidth.current) {
        measuredWidth.current = inner.clientWidth;
        heights.current.clear();
      }
      measure(false);
      bump((v) => v + 1);
    });
    ro.observe(inner);
    ro.observe(el);
    return () => ro.disconnect();
  }, [measure]);

  const onScroll = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    setFollowing(atBottom(el.scrollTop, el.clientHeight, el.scrollHeight));
    setTop(el.scrollTop);
    maybeLoadMore();
  }, [maybeLoadMore]);

  const jumpToLatest = () => {
    const el = scroller.current;
    if (!el) return;
    setFollowing(true);
    el.scrollTop = el.scrollHeight;
    placed.current = placed.current && { ...placed.current, scrollHeight: el.scrollHeight };
  };

  return (
    <div ref={scroller} className="log" role="feed" aria-label={label} aria-busy={loadingMore} tabIndex={0} onScroll={onScroll}>
      <div ref={content}>
        {keys.length > 0 && (
          <div className="vstatus" aria-live="polite">
            <span>{topNote}</span>
          </div>
        )}
        <div style={{ height: lay.offsets[start] }} aria-hidden="true" />
        {items.slice(start, end).map((item, i) => (
          <div key={keys[start + i]} data-vkey={keys[start + i]} className="vitem" aria-posinset={start + i + 1} aria-setsize={keys.length}>
            {render(item, start + i)}
          </div>
        ))}
        <div style={{ height: total - lay.offsets[end] }} aria-hidden="true" />
        <div aria-live="polite">{footer}</div>
      </div>
      {!following && (
        <div className="vjump">
          {/* A small round arrow, as in other chat apps: the label is for assistive technology and the tooltip. */}
          <button type="button" data-icon="arrow-down" aria-label="Jump to latest" title="Jump to latest" onClick={jumpToLatest} />
        </div>
      )}
    </div>
  );
}
