# Data Model: Share Blog Posts

No stored data. No migration.

## ShareCardInput (in memory, front end only)

| Field | Source | Rule |
|---|---|---|
| title | the published post (or the author's draft in the editor preview) | plain text, never interpreted |
| summary | the post's summary, else its opening text | plain text; clamped to 6 lines |
| authorName | the post's public display name | as on the post page |
| coverUrl | the post's cover, same-origin | optional; failure to load falls back to the plain block |
| siteName, address | constants and the canonical address | short readable form (host and path) |
| variant | the reader's theme | `light` or `dark` |

## ShareCard (in memory)

A PNG blob of 1080 × 1920 pixels produced from a ShareCardInput; discarded when the dialog closes. Nothing is uploaded.

## State of the Story dialog

`preparing → ready → (sending | saved) ` and `failed`; `failed` always leaves the link copyable (FR-017).
