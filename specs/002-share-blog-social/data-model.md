# Data Model: Share Blog Posts

No stored data. No migration.

## ShareCardInput (in memory, front end only)

| Field | Source | Rule |
|---|---|---|
| title | the published post (or the author's draft in the editor preview) | plain text, never interpreted |
| summary | the post's summary, else its opening text | plain text, cut at a sentence or word boundary |
| highlights | the post's section headings, in order | up to 4; each shortened to one or two lines; none invented |
| authorName | the post's public display name | as on the post page |
| coverUrl | the post's cover, same-origin | optional; failure to load falls back to the plain block |
| siteName, address | constants and the canonical address | short readable form (host and path) |

## ShareCard (in memory)

A PNG blob of exactly 1080 × 1920 pixels (fixed canvas) produced from a ShareCardInput; discarded when the dialog closes. Nothing is uploaded.

## State of the Story dialog

`preparing → ready → (sending | saved) ` and `failed`; `failed` always leaves the link copyable (FR-017).
