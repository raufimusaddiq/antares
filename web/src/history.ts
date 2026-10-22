import type { Turn } from "./chatApi";

// How pages of history combine in the browser. Turns are identified by seq (the
// server's position in the transcript), so merging is by identity, never by
// array position or count.

/** Older turns go in front. Anything already held (by seq) is dropped, so a repeated page cannot duplicate a turn. */
export function prependOlder(held: readonly Turn[], older: readonly Turn[]): Turn[] {
  if (held.length === 0) return [...older];
  const first = held[0].seq;
  return [...older.filter((t) => t.seq < first), ...held];
}

/**
 * The newest page arrived (after a run finished, say). It replaces the held
 * turns from its first seq on and keeps the older ones the reader already
 * loaded, so scrolling back is not undone. If the held turns do not touch the
 * page (a gap), the held turns are dropped: a list with a hole in it would be
 * presented as a complete conversation.
 */
export function mergeNewest(held: readonly Turn[], page: readonly Turn[]): { turns: Turn[]; reset: boolean } {
  if (page.length === 0) return { turns: [...held], reset: false };
  const first = page[0].seq;
  const older = held.filter((t) => t.seq < first);
  const touches = older.length === 0 ? held.length === 0 || held[0].seq >= first : older[older.length - 1].seq === first - 1;
  if (!touches) return { turns: [...page], reset: true };
  return { turns: [...older, ...page], reset: false };
}
