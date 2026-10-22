# Runbook — chat runs (slice 4)

Code: `internal/hermes/client.go` (adapter), `internal/chat` (lifecycle, SSE), `internal/store/chats.go`
(owner-scoped queries), `web/src/Chat.tsx` (UI). PRD §6.2, §6.3, §9, §9.1.

## Wire shapes are documented, not captured

Phase 0 captured no fixture for sessions, runs or transcripts (`docs/hermes/PHASE0-FINDINGS.md` §2:
UNVERIFIED). The adapter is written to the **documented** shapes in PRD §9A and nothing was
called on the live container. Every decoder fails closed:

| Call | Expected | Anything else |
|---|---|---|
| `POST /api/sessions` | `{id}` (or `session_id`, `session.id`), id matches `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$` | `HERMES_UNAVAILABLE` |
| `POST /v1/runs` | `{run_id}` | `HERMES_UNAVAILABLE` |
| `GET /v1/runs/{id}` | `{status}`; unrecognised status → `unknown` (never completed) | — |
| `GET /v1/runs/{id}/events` | `text/event-stream`; unrecognised frames dropped | non-stream content type → `HERMES_UNAVAILABLE` |
| `GET /api/sessions/{id}/messages` | array, or `{messages}` / `{data}`; only user/assistant text kept | `HERMES_UNAVAILABLE` |

**Before relying on this in production**, capture a sanitized fixture for each row from a test profile
(`scripts/hermes_contract.py` pattern) and add it to the contract guard. Event names and field names in
`internal/hermes/events.go` are the part most likely to need adjusting.

## Run lifecycle

`PENDING → RUNNING → COMPLETED | FAILED | CANCELLED | TIMED_OUT`, with `DISCONNECTED` for "cannot tell".

- A worker per run, detached from the HTTP request, relays Hermes' stream into a bounded in-memory
  journal. Browsers read the journal over SSE; ids are `<epoch>-<seq>`, so `Last-Event-ID` resumes
  exactly, and an id from another epoch (restart, new worker) replays from the journal's start, which
  always opens with `run.started` or `run.resumed{replace:true}` (the client clears its text).
- **Completion is never inferred.** A stream that ends with no terminal frame is reconciled with
  `GET /v1/runs/{id}`: terminal → recorded; still running → re-subscribe (3 tries); unknown or
  unreachable → `DISCONNECTED` (resumable, still occupies the chat).
- The database is the authority: `MoveRun` only moves a non-terminal run, and the event sent is the
  stored status. A late "completed" after a cancel is reported as cancelled.
- Restart: `RecoverRuns` makes `RUNNING` → `DISCONNECTED` and `PENDING` → `FAILED`. A run is **never
  replayed** (no auto-retry of possibly side-effecting work). Retry is a user action: a new run.
- `UPSTREAM_TIMEOUT` while creating a run → `TIMED_OUT` (it may have started; not replayed).
- An upstream approval request fails the run (`CAPABILITY_UNAVAILABLE`) and stops it upstream: this slice
  has no approval UI and nothing is approved or denied on the user's behalf.
- Cancel records `CANCELLED` first, closes the upstream stream, then asks Hermes to stop. If the stop is
  not accepted (or the account is no longer eligible) the answer says `upstreamStopped:false` and the UI
  says the assistant may still be working.

## Gate and limits

Every Hermes call runs `Service.gate` → `store.ActiveBinding`: APPROVED + tenant READY + binding READY and
verified, read from the database each time. Open SSE streams re-check every heartbeat (15 s) and close on
suspension. One active run per chat (partial unique index), 4 active runs and 30 sends/minute per user.

## History paging (slice 5, `HIST-002`/`HIST-003`, `PERF-001`)

`GET /api/chats/{chatId}/messages?cursor=<opaque>&limit=<n>` — an **app contract**; Hermes has no cursor.

- No `cursor`: the **newest** page. Default `limit=50`; the server ceiling is 100 and wins over a larger ask.
  `limit` below 1, a malformed query string or a malformed cursor is `400 INVALID_REQUEST` (a damaged
  cursor is never read as "no cursor").
- Answer: `{messages:[{seq,id,role,text,createdAt}], nextCursor, total}`. `seq` is the turn's position in the
  transcript (0 = oldest) and is its identity across pages; `nextCursor` is empty at the start of the
  conversation.
- A cursor carries the position it stops at and a hash of the turn there (bound to the chat). Appending newer
  turns does not move older positions, so pages never overlap or skip. If the turn at that position changed or
  vanished (compaction, rewind) the answer is `409 HISTORY_CHANGED` and the UI reloads the newest page.
- Cost: every page is one bounded full-transcript read from Hermes (`include_compacted=true`), then sliced.
  Limits: 24 MiB body, 20,000 messages, 2 concurrent transcript reads per process. **Over a limit the call
  fails (`HERMES_UNAVAILABLE`) — it never returns a shortened conversation as if it were complete.** There is
  no transcript cache: a stale page after a run would be worse than the extra read. Measured (local fake Hermes,
  1,000 messages): p50 ≈ 4 ms, 4.4 KB per page — `go test -run Measure500 -v -count=1 ./internal/chat`.
- `GET /api/chats` pages by keyset `(updated_at DESC, id)` with an opaque cursor, default 50, ceiling 100. A
  chat that gets a new message while the reader pages moves to the top; that is a navigation list, not a
  transcript, and no chat is listed twice within one pass.

Browser side (`web/src/VirtualList.tsx`, `virtual.ts`, `history.ts`): the first paint is the newest page;
older pages load when the reader is within ~320 px of the top, one at a time, never twice (`seq` merge drops
duplicates). Up to 100 turns are all mounted (text selection and find work); beyond that only the turns near
the viewport are. Each mounted turn is measured before paint and the scroll position is re-derived from the
turn at the top of the viewport, so a prepended page or a late height change above does not move what is being
read. Native scroll anchoring is off (`overflow-anchor:none`). While the reader is at the bottom the list
follows the newest content (including a streaming reply); once they scroll up it stops and "Jump to latest"
appears. `scripts/browser-check.mjs` measures all of this in a real browser.

## Not in this slice

Rename/delete chat, model picker, approvals, clarify, steer, assets, jobs (still `CAPABILITY_UNAVAILABLE`
behind the guard). Attaching the versioned OpenUI instruction to a run (PRD §7.2): the renderer is here, the
per-request instruction mechanism has no verified Hermes shape, so nothing is sent (`docs/runbooks/openui.md`).
Journals are in memory: after a restart the text streamed so far is rebuilt from Hermes' transcript when the
run completes.
