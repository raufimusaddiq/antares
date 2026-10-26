# Runbook — chat management (rename, pin, archive, delete)

Issue #148, PRD §26.1 (session/history), §9A.2, §21.5, UXP-ACC-018. `PATCH /api/chats/{chatId}` and `DELETE /api/chats/{chatId}`
were 501 placeholders; they are built. Phase 0 is still FAIL: this is trusted-user operation, not a multi-user security claim.

## What is local and what reaches Hermes

| Action | Where it happens | Hermes call |
|---|---|---|
| Rename | `chats.title` and the title search row, one transaction | none |
| Pin / unpin | `chats.pinned_at` (only an `ACTIVE` chat can be pinned) | none |
| Archive / restore | `chats.status` `ARCHIVED` ⇄ `ACTIVE`; archiving unpins. An archived chat is still readable, searchable and usable: a message into it restores it | none |
| Delete | claim, upstream delete, local purge (below) | `DELETE /api/sessions/{id}` |

Antares' index is what the sidebar shows (PRD §9A.2), so rename, pin and archive are not mirrored upstream and nothing is claimed
about upstream state for them. Hermes' own `PATCH` accepts `title`, `pinned`, `archived` (`_handle_patch_session`), so mirroring is
possible later; it was not done because upstream title rules were not probed.

`PATCH` takes exactly one of `title`, `pinned`, `archived`; any other field is a 400 (a profile, session id or owner is refused, not ignored).
A chat whose delete is in flight answers `PATCH` exactly as it answers `DELETE`: `409 CHAT_STATE` for its owner (it exists until that delete settles, and a 404 is what a browser reads as "deleted"), `404` for anyone else.
The chat list cursor is `v2` (pinned flag, `updated_at`, id). A `v1` cursor (issued before pinning existed) is still accepted and means a position in the unpinned part of the list, so a cursor held across a deploy keeps working; a malformed or unknown-version cursor is a 400.

## The Hermes delete contract (read from source, not production)

`gateway/platforms/api_server.py`, `_handle_delete_session` (probe copy of the deployed build under
`antares-execution-20261024/probes/api_server.py`, line numbers there: route table 1668, handler 3187):

- Advertised in `GET /v1/capabilities` as `endpoints.session_delete = {method: DELETE, path: /api/sessions/{session_id}}`
  (`docs/hermes/fixtures/capabilities.body.json`). Antares sends the delete only if the exact method and path are advertised;
  otherwise `501 CAPABILITY_UNAVAILABLE`, nothing changes, and `/api/capabilities` reports `chat_delete` unsupported (the UI shows Delete disabled with that reason).
- `200 {"object":"hermes.session.deleted","id":…,"deleted":bool}`. Only `deleted:true` is success.
- `404 {"error":{"code":"session_not_found"}}`: the session is not there. Treated as already gone. Any other 404 (an unknown profile route) is **not** "gone" and fails.
- `409 session_active_turn` (a turn is writing to it) → `RUN_CONFLICT`. `503 session_db_unavailable` → `HERMES_UNAVAILABLE`.
- The handler also scrubs transcripts/dumps, drops routing entries and collects unheld images upstream. Antares relies on that and does not touch Hermes' data directories.

Verified only against the local fake (`internal/chat/harness_test.go`, `scripts/capacity/upstream.go`) shaped from that source.
It was **not** called on the deployed Hermes. A first real delete is an owner-approved operator step.

## Delete, in order

1. Ownership (`GetChat`), the default-deny gate, and "this chat is on the account's current binding", else no Hermes call.
2. Capability check (above).
3. `BeginDeleteChat`: one statement moves the chat to `PENDING_DELETE` only if it has no non-terminal run (`RUN_CONFLICT` otherwise). Only an `ACTIVE`/`ARCHIVED` chat can be claimed, so exactly one request holds the claim: an overlapping delete gets `409 CHAT_STATE` (never a 404, which a browser would read as "deleted"; another account still gets 404), sends nothing upstream, and cannot restore a chat the holder is still deleting. `AdmitRun`'s insert re-checks the chat status, so no run joins a chat on its way out.
4. Upstream delete. Anything but success/session-gone → the chat goes back to the status it had (active or archived; its pin was never touched) with `delete_failed_at` set: still listed, in the same place, with a visible "Delete failed" flag, readable, retryable; the error code is audited. (`pre_delete_status` remembers ACTIVE/ARCHIVED while `PENDING_DELETE`; the `DELETE_FAILED` status is no longer written.)
5. `FinishDeleteChat`, one transaction: search rows, draft, run prompts, run images, message metadata, runs, the chat, and staged images that no other chat's run also sent.
   Tokens the runs reported are kept as two per-user numbers (`deleted_chat_usage`) so deleting chats does not reset the token budget or the usage total. A chat whose runs reported nothing writes no row, so "usage unknown" does not become "0 tokens".
6. A restart returns any `PENDING_DELETE` chat to its previous status, flagged (`RecoverRuns`), so a delete cut short is visible, not silent.

The local steps run on a context that survives the browser disconnecting, so a delete Hermes completed is not stranded.

## Audit

`chat.rename|pin|unpin|archive|unarchive|delete` with the actor, `chat` and the opaque chat id, outcome `ok`, `refused:CHAT_STATE`, or `failed:<code>`.
No title, session id, profile or text is recorded.

## UI

Each sidebar row has an actions button (`Actions for <title>`, `aria-haspopup=menu`): Rename…, Pin to top, Archive, Delete…. Arrow keys/Home/End move,
Escape closes and returns focus. Rename and delete are modal dialogs (focus trapped, Escape cancels; delete starts on Cancel and says what goes with the chat).
A change shows only after the server confirms it. `Show archived` lists the archive. On a phone the strip stays a line of chips (the open one whole; `mobile layout` CI enforces it) and the open chat's actions are under Options → Conversation actions; the archive replaces the strip like an open search, and its rows keep their buttons. A message sent into an archived chat makes it active on the server; the list is read again so it reappears at once.
After a delete the browser also forgets that chat's local draft, resets the search panel, and opens the next chat (or the empty state).

## Browser check

`web/scripts/manage-check.mjs` (see its header) against the `scripts/capacity` scratch stack: desktop 1280, phones 390 and 320 (emulator, not a device).
