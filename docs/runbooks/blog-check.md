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

Restart Antares after changing it. Writing and publishing never depend on it.

## What a check costs and how it is limited

A check is one Hermes run (about a minute, web search) on the author's own profile, billed to the shared Kenari account like a chat
reply. It is subject to the same rules as a message: the author's **daily hard spending limit** refuses it; the tokens it reports
are recorded and counted in the author's spending view and usage (`store.UserModelUsage`), also after the post is deleted; the
admin's **blocked-model list** applies (the model that would run is named, a blocked or unknown default never runs); the account's
run allowance applies. On top: one running check per post, two per author, 30 per rolling 24 hours. The account must stay approved
and ready until the answer is recorded.

## Smoke

`antares-smoke` runs `blog.check` after `blog.lifecycle`: a real check on the synthetic account A, then accept, publish enriched,
read signed out, edit (the check goes out of date), unpublish, and a check that the account's chat list did not change. It **skips**
(an INCOMPLETE verdict, never a pass) where the deployment does not offer the check; use `--skip-blog-check` where that is
intended (for example with `ANTARES_BLOG_CHECK=off`). `--check-timeout` bounds it (default 5 minutes).

## Failure reading

A check ends as exactly one of: `ready`, `failed` (`error`: the assistant reported a failure or asked a question nobody can answer;
`disconnected`: the connection was lost and the run could not be confirmed finished; `unusable`: the reply was not the envelope),
`timed_out`, `cancelled` (by the author, or the account lost eligibility while it ran) or `out_of_date` (the draft changed). A
process restart marks a running check `failed`/`disconnected`. Sources are never marked verified: the run's events do not expose what a
search returned (research R4).
