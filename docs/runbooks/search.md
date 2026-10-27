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

## Deviation from the PRD: no FTS5, and what that costs (measured)

PRD §21.3 names SQLite FTS5 for option B. This index is a plain table searched with `instr(folded, term)`:
it keeps substring matching (`Ünal` inside a longer word, any script, no tokenizer) and one fold function
for index, query and highlight. The price is that **every search scans the user's whole index** (a miss reads
every row; a hit stops after `limit` rows). It is therefore bounded by a per-user budget, not by an index:
`MaxIndexedUserDocs` = 100,000 turns and `MaxIndexedUserRunes` = 25,000,000 runes (`search_state.runes`),
whichever comes first; a chat cut by either is `truncated` and reported as "partly indexed". A search that
runs past 10 s is cancelled (`SEARCH_TIMEOUT`).

Measured with `ANTARES_SEARCH_MEASURE=1 go test ./internal/store -run TestMeasureSearchAtQuota -v` (2 vCPU
host, random text, warm cache, best of 3, `limit` 20, one search of one user, nothing else running):

| index (docs × runes/doc) | miss (full scan) | rare hit | two terms, miss |
|---|---|---|---|
| 100,000 × 250 (turn cap, inside rune budget) | 120 ms | 23 ms | 112 ms |
| 6,200 × 4,000 (rune budget, longest turns) | 208 ms | 62 ms | 474 ms |
| 100,000 × 500 (50M runes, over today's budget) | 772 ms | 237 ms | 1.24 s |
| 100,000 × 4,000 (400M runes, the old limits) | 1.73 s | 507 ms | 2.73 s |

The last two rows are what the turn cap alone allowed before the rune budget; they are kept to show why the
budget exists. A run of the same test while other builds were running on the host gave 3–10 s for those rows,
so a loaded host is slower than this table. If real accounts outgrow the budget, move to FTS5 with the
`trigram` tokenizer over the already-folded text (keeps substring matching; terms under 3 characters still
scan) rather than raising the budget.

## Matching

`store.Fold` (Unicode simple lower-casing, rune for rune) folds the stored text at index time and the query
at search time; matching is `instr(folded, term)` per term (all terms must occur in one turn). SQLite
`LOWER()` folds ASCII only; do not use it or `LIKE` here. Folding is not accent-stripping: `unal` ≠ `Ünal`.
Highlights are computed in Go over the same fold and returned as text segments; the browser renders them as
text nodes and `<mark>`, so message content is never parsed as markup.

## Freshness, bounds, honesty

- A search reads at most 3 stale chats from Hermes (1 for a scoped search); opening a chat indexes it from
  the transcript already fetched; a run ending in a chat invalidates it. Later pages read nothing.
- Per turn 4000 runes, per user 100000 turns and 25M runes (see the measurement above). A clipped chat is `truncated` and counts as incomplete.
- The response says `index: {complete, chats, pending, failed, partial}`. `pending` (not indexed yet) is "Still indexing N of M"; `failed` (transcript unreadable; retried no sooner than every 5 minutes, so a few bad chats cannot use up every search's read budget); `partial` (cut by a limit, never fixed by waiting) is "only partly indexed". Neither is called
  a partial index complete.
- Order is recency of creation (chat created_at, then newest turn); the key is immutable so a run starting mid-pagination cannot move a chat across a cursor. No relevance score exists. The plan sorts only the
  turns inside one chat (`TestSearchPlanUsesOwnerIndexWithoutSorting`).

## At rest

The index is plain SQLite text, duplicated and sensitive, **not encrypted** (PRD §21.3). It lives in the
database file (0600, dedicated 0700 directory; see `data-layer.md`) and is in every backup of it.

## Retention, export, deletion and disk (#171)

- **Retention.** The index is derived data: rows exist only for the owner's `ACTIVE`/`ARCHIVED` chats and are rebuilt from
  Hermes on demand. There is no time-based expiry; a finished run marks its chat stale (`InvalidateChatIndex` in `finish`),
  and the next search or open re-reads it. Per-user bounds: 100,000 turns and 25M runes (above).
- **Deletion.** Deleting a chat removes its `search_docs` and `search_state` rows in the purge transaction
  (`store/chatmgmt.go`, `FinishDeleteChat`); both tables also cascade with the chat and the user. Proven by
  `internal/chat/manage_test.go` ("deleted chat still searchable" check) and `internal/store/search_test.go`.
- **Export.** The index is never exported. Chat export reads the transcript the history route returns; a backup copies
  the database file, index included (`backup-restore.md`), so a restored backup holds the index of chats that existed then.
- **Disk forecast**, derived from the measurement in `capacity.md` §3 (fresh DB, 10,000 runs / 20,000 turns, prompt ≈ 190 B,
  reply 2,000 B): 636 B per run unindexed; +5.48 KB per indexed message pair, ≈ 2.5× the text; up to ~10 MB WAL while running.
  So a fully indexed account costs ≈ 2.5 bytes per byte of chat text, and the per-user rune budget (25M runes ≈ 25 MB of
  ASCII text) caps one account's index at roughly **60–65 MB**. One owner with 5,000 message pairs of that size: ≈ 3 MB
  unindexed, ≈ 30 MB indexed. These are extrapolations of the recorded ratio, not new measurements.
- **Telemetry before any cache.** Every history page reads the whole transcript from Hermes, then slices it. Antares logs
  `chat: history page fetch_ms=… total_ms=… turns=… page=…` per page and `chat: search index write_ms=… turns=… text_bytes=…`
  per index write: counts and durations only, no id or text (`TestHistoryAndIndexLatencyAreLoggedWithoutContent`). No
  transcript cache is added: none is justified until those logs show it. Revisit when the p95 of `fetch_ms` on the live
  host passes ~1 s or `turns` approaches the 20K-turn transcript bound.

## Not yet covered

No chat delete/rename/archive route exists yet. When one is added it must call `store.PurgeChatIndex`
(delete) or `store` title re-index (rename); archived/non-ACTIVE chats are already excluded by the query.
