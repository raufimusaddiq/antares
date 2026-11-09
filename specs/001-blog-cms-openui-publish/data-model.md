# Data Model

SQLite, migration `internal/store/migrations/0025_blog.sql`. Every table touched by a user request is
queried with `author_id` in the same statement (Principle I).

## Entities

### posts
| Field | Notes |
|-------|-------|
| id | opaque random id |
| author_id | owner; every authenticated query filters on it |
| slug | unique among *published* posts; chosen by the author; lowercase `a-z0-9-`, 3-80 chars; reserved words refused |
| title, summary | plain text; title required to publish |
| cover_asset_id | nullable, must belong to this post |
| status | `draft` / `checking` / `ready` / `published` / `unpublished` |
| latest_version | number of the newest saved version |
| published_version | nullable; the version readers see |
| published_at, created_at, updated_at | |

### post_versions
| Field | Notes |
|-------|-------|
| post_id, version | primary key together |
| doc | JSON document tree, validated against the node/mark allowlist; max size bounded |
| created_at | autosave creates/updates the head version; publish pins one |

Autosave updates the head version in place within a short window and starts a new version on publish, check
start, or after a pause, so history stays small. Compare-and-set on `version` (R7).

### post_checks
| Field | Notes |
|-------|-------|
| id, post_id, version | the exact version checked |
| state | `running` / `ready` / `failed` / `cancelled` / `timed_out` / `out_of_date` |
| prompt_version, usage_* | the instruction's version ("blog-check v1") and the tokens the run reported (counted in the author's spending) |
| hermes_session, hermes_run | the check's own Hermes session and run (no Antares chat); never shown to readers; the session is deleted when the check ends |
| notes | JSON list of author-facing notes (text only) |
| reason | when failed: `error`, `disconnected` or `unusable`; no provider text, no credential |
| started_at, finished_at | |

### post_blocks
| Field | Notes |
|-------|-------|
| id, check_id, post_id | |
| anchor | `{afterNode: n}` in the checked version |
| kind | catalogue name (table, chart, metric, accordion, sources…) |
| markup | validated OpenUI Lang text, size/depth bounded |
| fallback | plain text shown if rendering fails (FR-021) |
| decision | `proposed` / `accepted` / `rejected` |
| needs_ack | true if any source is unverified (R4); acceptance must be explicit |

### block_sources
| Field | Notes |
|-------|-------|
| block_id, url, title, retrieved_at, verified | `https` URLs only |

### post_assets
| Field | Notes |
|-------|-------|
| id, post_id, author_id, content_type, bytes (or file ref), created_at | private until the post is published; at most 30 per post and 200 MiB per author, 5 MiB each |

## Relationships and rules

- A post has many versions, many checks, many assets. A check has many blocks; a block has many sources.
- Publishing pins `published_version` and snapshots the **accepted** blocks with that version; later checks do
  not alter what readers see until the author republishes (FR-018).
- Editing the head version while a check is `running` or `ready` moves that check to `out_of_date` (FR-013).
- Public read = post `status = published` AND `published_version` set AND author approved (suspended and rejected authors are hidden; their assistant workspace state is not consulted).
  Nothing else is selectable by the public handlers.
- Deleting a post deletes versions, checks, blocks, sources and assets (cascade) and frees the slug.
- A removed or revoked author's published posts are not served (suspend hides; revoke follows the same query).

## State transitions (post)

```text
draft ──Publish──▶ checking ──ready──▶ ready ──accept──▶ published
  ▲                  │ failed/cancelled/timed_out        │
  └──── back to draft ◀──────────────┘                   ├─unpublish─▶ unpublished ─republish─▶ checking
        "Publish original" skips checking ───────────────┘
```

A failed, cancelled, timed-out or disconnected check always returns the post to `draft` with the failure
recorded on the check; it is never shown as completed.
