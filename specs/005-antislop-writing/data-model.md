# Data Model: Anti-Slop Writing Rules

## writing_rules_setting (new)

| Column | Type | Rule |
|--------|------|------|
| id | INTEGER PRIMARY KEY CHECK (id = 1) | one row |
| enabled | INTEGER NOT NULL CHECK (enabled IN (0,1)) | absent row = on |
| updated_by | TEXT NOT NULL | an administrator's user id |
| updated_at | TEXT NOT NULL | RFC3339 UTC |

## runs.writing_rules, post_checks.writing_rules (new columns)

`TEXT` nullable, three meanings: the rules version attached to that run or check (`"v1"`); `"off"` when the administrator had the rules switched off (a deliberate
state); NULL when the rules should have been attached but were not (the text was unavailable), or the run predates migration `0029`.
Only NULL on a run started since `0029` was applied is "not applied". Never read back by anyone but the admin counts (aggregate) and tests.

## Rules text (not stored)

Embedded in the binary: `internal/writing/rules.md`. Fields by convention: first line `writing-rules vN (source, licence)`, a body of at most 3,000
characters. Changing it is a release, bumps N, and is a deliberate, reviewed change.

## Check note kinds (existing, extended)

`clarity`, `fact`, `missing-context`, `unsourced` and new `style`.

## audit_log (existing) - new action

`writing_rules.set` with `detail` = `enabled=true|false`.
