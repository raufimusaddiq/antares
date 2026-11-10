# Data Model: Admin Full Control

## posts (existing) — added columns

| Column | Type | Rule |
|--------|------|------|
| takedown_at | TEXT NOT NULL DEFAULT '' | RFC3339 UTC; '' = not taken down |
| takedown_by | TEXT NOT NULL DEFAULT '' | admin user id; '' when not taken down |
| takedown_reason | TEXT NOT NULL DEFAULT '' | 1–500 characters, trimmed, no control characters; shown to the author |
| takedown_seen_at | TEXT NOT NULL DEFAULT '' | set when the author has seen the banner; reset to '' on a new takedown |

Invariants: the three takedown fields are all empty or all set. `publicWhere` gains `AND p.takedown_at = ''`. Publish/update-published require `takedown_at = ''`. Restore clears all four.

States shown to admin: `visible` (published, author approved, not taken down), `hidden_account` (published, author suspended/rejected, not taken down), `taken_down`. Unpublished/draft posts are never listed.

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
