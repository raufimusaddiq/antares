# Data model: Blog Writing Feedback and the AI Editor

Migrations `0030_check_progress.sql` (the progress columns) and `0031_post_edits.sql` (`post_edits`, `post_blocks.anchor_text`). Every row carries `author_id` and every query filters on it (constitution I).

## `post_edits` (new): a suggested change to the author's text

| Column | Type | Rule |
|--------|------|------|
| id | TEXT PK | server-generated |
| check_id | TEXT NOT NULL | `REFERENCES post_checks(id) ON DELETE CASCADE` |
| post_id | TEXT NOT NULL | `REFERENCES posts(id) ON DELETE CASCADE` |
| author_id | TEXT NOT NULL | `REFERENCES users(id)` |
| position | INTEGER NOT NULL | order returned by the assistant (most important first) |
| kind | TEXT NOT NULL | `CHECK (kind IN ('replace','insert'))` |
| part | INTEGER NOT NULL | top-level part index in the version the check read |
| anchor | TEXT NOT NULL | text of the part's first text block, whitespace collapsed, at most 120 characters |
| original | TEXT NOT NULL DEFAULT '' | `replace`: the exact span, one line, at most 1,000 characters; `insert`: empty |
| proposed | TEXT NOT NULL | plain text; `insert` may hold several paragraphs separated by a blank line; at most 4,000 characters (8,000 for `insert`) |
| reason | TEXT NOT NULL | one or two plain sentences, at most 300 characters |
| decision | TEXT NOT NULL DEFAULT 'open' | `CHECK (decision IN ('open','accepted','rejected'))` |
| decided_at | TEXT NOT NULL DEFAULT '' | |

Indexes: `(check_id, position)`, `(author_id, post_id)`. No "no longer applicable" column: that is computed against the current draft by whoever uses the suggestion.

## `post_blocks` (changed)
`anchor_text TEXT NOT NULL DEFAULT ''`: same definition as `post_edits.anchor`, for the block's `anchor` part. Old rows have '' and are placed by their stored index, as before.

## `post_checks` (changed): progress
| Column | Type | Rule |
|--------|------|------|
| stage | TEXT NOT NULL DEFAULT 'reading' | `CHECK (stage IN ('reading','searching','writing'))` |
| stage_at | TEXT NOT NULL DEFAULT '' | when the current stage began (set to `started_at` on insert by the start code) |
| last_event_at | TEXT NOT NULL DEFAULT '' | when the last upstream event arrived |
| lookups | INTEGER NOT NULL DEFAULT 0 | tool starts seen, shown as "N lookups" |

State `out_of_date` stays in the CHECK for old rows and is no longer set. `post_versions.pinned` stays; a check no longer pins.

## Entities in the spec -> storage
- **Check progress** -> the four columns above.
- **Text suggestion** -> `post_edits`.
- **Post language** -> not stored (R8).

## State
Suggestion: `open -> accepted | rejected`. Both are final for that check; a rejected suggestion can be accepted later only by running a new check. "No longer applicable" is a display state of an `open` suggestion.
Check: `running -> ready | failed | cancelled | timed_out` (no `out_of_date`).

## Cleanup and guards
`scripts/smoke_fixtures.py`: `post_edits` owned by `author_id IN fx` (deleted before `post_checks`). The admin-blindness guard plants draft text and scans every admin response; the decision route is not an admin route.
