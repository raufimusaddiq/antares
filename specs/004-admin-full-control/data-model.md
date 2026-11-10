# Data Model: Admin Full Control

## posts (existing) — added columns

| Column | Type | Rule |
|--------|------|------|
| takedown_at | TEXT NOT NULL DEFAULT '' | RFC3339 UTC; '' = not taken down |
| takedown_by | TEXT NOT NULL DEFAULT '' | admin user id; '' when not taken down |
| takedown_reason | TEXT NOT NULL DEFAULT '' | 1–500 characters, trimmed, no control characters except a line break or tab; shown to the author |
| takedown_title | TEXT NOT NULL DEFAULT '' | the published title at the moment of the takedown, so a post its author unpublished meanwhile can still be named; never a draft title |
| takedown_seen_at | TEXT NOT NULL DEFAULT '' | set when the author has seen the banner; reset to '' on a new takedown |

Invariants: the four takedown fields are all empty or all set. `publicWhere` gains `AND p.takedown_at = ''`. Publish/update-published require `takedown_at = ''`. Restore clears all four.

States shown to admin: `visible` (published, author approved, not taken down), `hidden_account` (published, author suspended/rejected, not taken down), `taken_down`. Unpublished posts and drafts are never listed, except a post that carries a takedown mark (its author unpublished it afterwards): it stays reachable so it can be restored, and is shown with the saved title only.

## model_defaults (new)

| Column | Type | Rule |
|--------|------|------|
| purpose | TEXT PRIMARY KEY | one of `chat`, `summary`, `blog_assistant` |
| model_id | TEXT NOT NULL | 1–200 chars, no control characters |
| set_by | TEXT NOT NULL | admin user id |
| set_at | TEXT NOT NULL | RFC3339 UTC |

No row = service default. Images and videos stay in `tenant_tool_policy.image_model/video_model`.

## model_blocked (existing)

Unchanged: the "off" set. A model id may not be added while it is any purpose's default.

## audit_log (existing) — new actions

`post.take_down` (target post, reason in detail), `post.restore`, `model_defaults.set` (purpose, model), `model_policy.set` (existing).

## audit_log (existing) — added column

`detail TEXT` (nullable): what an admin action says about itself (a takedown's reason, a default's model). Written with the audit row, never returned by the admin API.
