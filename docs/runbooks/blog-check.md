# Runbook — the blog's AI check

The author's own assistant (their Hermes profile, chosen by the backend) reads a draft, searches the web and returns notes
and proposed interactive blocks; the author accepts or rejects each one before publishing
(`specs/001-blog-cms-openui-publish`). It is **on by default**.

## Switch

`ANTARES_BLOG_CHECK` in `/opt/antares/antares.env`:

| Value | Effect |
|---|---|
| unset, `on` | every account whose assistant confirms web search can use it |
| `smoke` | only the synthetic smoke accounts (`google_issuer = https://synthetic-test.invalid/antares`) |
| `off` | nobody; `GET /api/blog/capabilities` reports `check: false`, the UI hides it, `POST /check` answers 503 |

Case and surrounding spaces are ignored. **Any other value turns the check OFF and is logged at startup** (`blog: ANTARES_BLOG_CHECK must be on, smoke or off, not "…": the AI check is OFF until this is fixed`); it never leaves the check on, and chat and the rest of the blog keep serving. Restart Antares after changing it. Writing and publishing never depend on it.

## What a check costs and how it is limited

A check is one Hermes run (about a minute, web search) on the author's own profile, billed to the shared Kenari account like a chat
reply. It is subject to the same rules as a message: the author's **daily hard spending limit** refuses it; the tokens it reports
are recorded and counted in the author's spending view and usage (`store.UserModelUsage`), also after the post is deleted; the
admin's **blocked-model list** applies (the model that would run is named, a blocked or unknown default never runs); the account's
run allowance applies. On top: one running check per post, two per author, 30 per rolling 24 hours. The account must stay approved
and ready until the answer is recorded.

## Smoke

`antares-smoke` runs `blog.check` after `blog.lifecycle`: a real check on the synthetic account A, then accept, publish enriched,
read signed out, edit (the check stays ready), unpublish, and a check that the account's chat list did not change. It **skips**
(an INCOMPLETE verdict, never a pass) where the deployment does not offer the check; use `--skip-blog-check` where that is
intended (for example with `ANTARES_BLOG_CHECK=off`). `--check-timeout` bounds it (default 5 minutes).

## Failure reading

A check ends as exactly one of: `ready`, `failed` (`error`: the assistant reported a failure or asked a question nobody can answer;
`disconnected`: the connection was lost and the run could not be confirmed finished; `unusable`: the reply was not the envelope),
`timed_out`, `cancelled` (by the author, or the account lost eligibility while it ran) or `out_of_date` (only old rows: the draft changing no longer ends a check, spec 006). A
process restart marks a running check `failed`/`disconnected`. Sources are never marked verified: the run's events do not expose what a
search returned (research R4).

## Trust caveat

Antares is still described as trusted-use only (README, AGENTS.md): the blog must not be represented as safe for multiple mutually untrusted authors until the project's isolation gate is demonstrated. An approved author's public post is visible to anyone; there is no admin takedown in v1, so the remedy is suspending or rejecting the author, which hides their posts.

## Progress and suggestions (spec 006)

- **Stage**: `post_checks.stage` is `reading` from the start, `searching` on a tool start (`lookups` counts them) and `writing` on the first message delta; a later lookup returns it to `searching`. `last_event_at` is the last event of any kind, written at most once every 2 s apart from a stage change. A stalled run ends as `timed_out` after 6 minutes; the screen says "Still working" after 45 s of silence.
- **Suggestions**: `post_edits` (author-owned) holds `replace` and `insert` suggestions with the anchor of their part. A reply is validated: a span must sit inside one line of its part, a figure must already be in the draft (title included), no link, address or tag, a quotation over 6 words must be in the draft, at most `4 x parts + 8`. A dropped one is named in one sentence.
- **Decisions**: `PUT /api/blog/posts/{id}/edits/{editId}` records accepted or rejected for the latest ready check; the draft is changed only by the editor in the browser (`web/src/blog/draftBridge.ts`).
- **Anchors**: `internal/blog/anchors.go` and `web/src/blog/anchors.ts` follow one rule and one fixture (`internal/blog/testdata/anchors.json`). Blocks are placed again by anchor when a check is read and when publishing; a block whose part is gone is skipped (`skippedBlocks`).
- **Limits**: a check from the editor and one from Publish share the same limits; `LIMIT_REACHED` carries `retryAt` or `running`.
