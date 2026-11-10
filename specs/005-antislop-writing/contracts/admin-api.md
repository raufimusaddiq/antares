# Admin API (active administrator only; same guard and same-origin rule as every `/api/admin/*` route)

`GET /api/admin/writing-rules` -> `{enabled, version, source, license, last24h: {with, without, notApplied}, warning?}`
- `version`: `"v1"` (the text in this build); `source`: `"anti-slop (github.com/miqdadbadjuber/anti-slop)"`; `license`: `"MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)"`.
- `last24h`: counts of chat runs and post checks started in the last 24 hours with the rules (`with`) and without them (`without`: switched off deliberately, or not applied); titles and summaries are not counted; no identifier of any account, chat or post.
- `notApplied`: runs started since the feature shipped (migration `0029` applied) whose rules were expected and not attached (column NULL). `warning`: `"not_applied"` when `enabled` is true and `notApplied` > 0; absent otherwise. Runs while the rules were switched off (`off`) and runs before the migration never raise it.

`PUT /api/admin/writing-rules` body `{enabled: bool}` and nothing else -> the same read. One audit row `writing_rules.set` (`enabled=...`), written with the change. Unknown fields, a missing `enabled` or a non-boolean: 400, nothing changes.

Both routes are listed in the admin-blindness guard's route scan; neither returns content, text of the rules or any per-account field.

# Check result (author API, existing route)

`GET /api/blog/posts/{id}/check`: notes may now have `kind: "style"`. Same shape, same limits (plain text, at most the existing note count).
