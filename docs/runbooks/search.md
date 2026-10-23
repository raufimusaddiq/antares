# Runbook — chat search (UXP-003)

Code: `internal/store/search.go`, `internal/chat/search.go`, migration `0005_chat_search.sql`.

## What it is

A **derived, per-user** index (PRD §21.3, option B) over the user's own conversation titles and
user/assistant turns. Hermes stays canonical; the index is rebuilt per chat from the owner's own
transcript. It is the scoped exception to the no-transcript-copy default, not a second transcript store.

- `search_docs (chat_id, seq)`: one row per turn; `seq` is the history API's turn position, `-1` is the title.
- `search_state`: per chat, `indexed_at` / `invalidated_at` / `truncated`. Fresh = `indexed_at >= invalidated_at`.

## Privacy by construction

- Every row carries `user_id`, and the composite FK to `chats (id, user_id)` makes the database refuse a
  row whose owner differs from its chat's.
- `store.SearchChats` is the only reader. It joins `chats` to `search_docs` on the owner id and filters
  `c.user_id = ?` in the same query. There is no unscoped match and no render-time filter.
- A chat id the caller does not own, in the path or in `chatId`, is `404 NOT_FOUND` before the index is
  touched. A cursor is a position inside the caller's own list.
- Unknown query parameters (`profile`, `apiKey`, `sessionId`, …) are `400`, not ignored.
- The search runs behind the same default-deny gate as history. A tenant that is not READY gets
  `TENANT_NOT_READY`, not stale local hits.

## Matching

`store.Fold` (Unicode simple lower-casing, rune for rune) folds the stored text at index time and the query
at search time; matching is `instr(folded, term)` per term (all terms must occur in one turn). SQLite
`LOWER()` folds ASCII only; do not use it or `LIKE` here. Folding is not accent-stripping: `unal` ≠ `Ünal`.
Highlights are computed in Go over the same fold and returned as text segments; the browser renders them as
text nodes and `<mark>`, so message content is never parsed as markup.

## Freshness, bounds, honesty

- A search reads at most 3 stale chats from Hermes (1 for a scoped search); opening a chat indexes it from
  the transcript already fetched; a run ending in a chat invalidates it. Later pages read nothing.
- Per turn 4000 runes, per user 100000 turns. A clipped chat is `truncated` and counts as incomplete.
- The response says `index: {complete, chats, pending}`. The UI says "Still indexing N of M" and never calls
  a partial index complete.
- Order is recency (chat activity, then newest turn). No relevance score exists. The plan sorts only the
  turns inside one chat (`TestSearchPlanUsesOwnerIndexWithoutSorting`).

## At rest

The index is plain SQLite text, duplicated and sensitive, **not encrypted** (PRD §21.3). It lives in the
database file (0600, dedicated 0700 directory; see `data-layer.md`) and is in every backup of it.

## Not yet covered

No chat delete/rename/archive route exists yet. When one is added it must call `store.PurgeChatIndex`
(delete) or `store` title re-index (rename); archived/non-ACTIVE chats are already excluded by the query.
