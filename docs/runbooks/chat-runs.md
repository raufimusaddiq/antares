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

## Not in this slice

Rename/delete chat, model picker, approvals, clarify, steer, assets, jobs (still `CAPABILITY_UNAVAILABLE`
behind the guard). Transcript paging beyond "newest 50". Journals are in memory: after a restart the text
streamed so far is rebuilt from Hermes' transcript when the run completes.
