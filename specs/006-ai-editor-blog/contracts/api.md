# API (author, existing blog routes; Active guard, same origin for writes)

## Check read: `GET /api/blog/posts/{id}/check` and the body of `POST /api/blog/posts/{id}/check` (start)
Adds two members to the existing `CheckView`:
```json
{
  "progress": { "stage": "reading|searching|writing", "stageSince": "RFC3339", "lastEventAt": "RFC3339", "lookups": 0, "startedAt": "RFC3339", "now": "RFC3339" },
  "edits": [ { "id": "...", "kind": "replace|insert", "part": 2, "anchor": "text of the part's first text block", "original": "...", "proposed": "...", "reason": "...", "decision": "open|accepted|rejected" } ]
}
```
- `progress` is present while the check is `running` (and kept on the finished check for the elapsed time). `now` is the server time, so the browser can compute its own offset.
- `edits` is empty while running and for an old check. `anchor` is the stored text the browser uses to find the part again.
- Blocks keep their shape; `afterNode` is now the index **relocated against the saved draft** and a block whose place is gone has `afterNode: -1` (shown as no longer applicable, never published).
- A check no longer turns `out_of_date`; the state is never returned for a new check.

## Decide a suggestion: `PUT /api/blog/posts/{id}/edits/{editId}`
Body `{"decision": "accepted" | "rejected"}` and nothing else (unknown field, other value: 400). Records the decision only; **the server never changes the draft** (the editor applied it, and autosave saved it).
Returns the `edit` object. Only the post's latest check can be decided on, and only while it is `ready` (409 `SUPERSEDED` otherwise, as for blocks); another author's, a missing or a malformed id is 404. One audit-free write (like blocks).

## Start a check: unchanged route, shared limits
`POST /api/blog/posts/{id}/check` from the editor panel or from Publish. Same refusals: 409 a check is running, 429 too many (2 running, 30 per 24 hours), 402/429 the daily spending limit, 422 too long, 503 unavailable. The error words tell the author when they can try again and that Publish original still works.

## Publish
`POST /api/blog/posts/{id}/publish` with `mode: "enriched"` no longer requires the check's version to be the draft's latest: accepted blocks are relocated against the draft at that moment; one whose place is gone is skipped and the response carries `skippedBlocks` (count) so the screen can say so. `mode: "original"` publishes exactly the author's text, as today.
