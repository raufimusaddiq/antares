# Data Model: Discover and Read Other People's Blog Posts

No schema change and no new stored data.

## PublicSummary (unchanged)
`slug`, `title`, `summary`, `coverUrl`, `authorName`, `publishedAt`. No email, id or account field; identical for every caller.

## List query (new optional parameters of the existing route)
| Parameter | Type | Rule |
|-----------|------|------|
| `page` | integer | existing; 1..`maxPublicPage`; beyond → empty list |
| `limit` | integer | new; 1..20; missing, invalid or out of range → 20 |
| `q` | text | new; trimmed, whitespace collapsed, ≤ 80 characters (longer → empty list); matched against title, summary, author display name (the fallback label "Author" is not searchable); `%` `_` `\` are literal |

Order: `published_at DESC, id`. Filter: `publicWhere` (published, approved author) applied in the same statement.

## Reader state (client only, not stored)
`anonymous` | `account` (from `GET /api/me`) and, when ACTIVE, the set of the reader's own published slugs (from
`GET /api/blog/posts`). Lives in component state for the visit; nothing is written to storage or the server.
