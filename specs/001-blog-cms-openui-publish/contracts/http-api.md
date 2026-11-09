# Contract: HTTP endpoints

All authenticated routes require an ACTIVE session and CSRF protection like the existing API; none accepts a
profile name, route prefix, API key, header or session id from the client (rejected, not ignored). Every
query is scoped to the caller as `author_id`. A foreign or missing id returns the same `404`.

## Authenticated (author)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/blog/posts` | List my posts with status, title, updated time |
| POST | `/api/blog/posts` | Create a draft |
| GET | `/api/blog/posts/{id}` | Read my post (head version, status, current check) |
| PUT | `/api/blog/posts/{id}` | Autosave `{baseVersion, title?, summary?, doc?}`; `409` with latest on stale base |
| PATCH | `/api/blog/posts/{id}/settings` | Slug, summary, cover; `409` on slug conflict |
| DELETE | `/api/blog/posts/{id}` | Delete after confirmation in the UI |
| POST | `/api/blog/posts/{id}/check` | Start a check of the head version (`409` if one is running) |
| GET | `/api/blog/posts/{id}/check` | Check state, notes, proposed blocks with sources |
| POST | `/api/blog/posts/{id}/check/cancel` | Stop a running check |
| PUT | `/api/blog/posts/{id}/blocks/{blockId}` | `{decision: accepted|rejected, ack?: true}` (`ack` required when `needs_ack`) |
| POST | `/api/blog/posts/{id}/publish` | `{mode: "enriched"|"original"}`; enriched requires a `ready` check on the head version |
| POST | `/api/blog/posts/{id}/unpublish` | Remove from public view |
| POST | `/api/blog/posts/{id}/duplicate` | Copy as a new draft |
| POST | `/api/blog/posts/{id}/assets` | Upload an image (type/size limited) |
| GET | `/api/blog/posts/{id}/assets/{assetId}` | Author-only read before publish |
| GET | `/api/blog/capabilities` | Whether writing, checking and web search are available (FR-029) |
| GET | `/api/blog/limits` | The numbers the author is held to (text a check reads, post size, images, blocks, checks per 24 h), read from the enforced constants, shown before publishing |

## Public (no session): the only unauthenticated blog routes

| Method | Path | Returns |
|--------|------|---------|
| GET | `/b` | HTML shell, indexable, with injected metadata |
| GET | `/b/{slug}` | HTML shell with injected title/description/Open Graph and an escaped text copy |
| GET | `/api/public/blog/posts` | `[{slug, title, summary, coverUrl, authorName, publishedAt}]`, 20 per page, newest first; `?page=N` (from 1) |
| GET | `/api/public/blog/posts/{slug}` | `{title, summary, doc, blocks:[{anchor, markup, fallback, sources:[{url, title, retrievedAt, verified}]}], authorName, publishedAt}`; `verified` is always false until a Hermes exposes what a search returned (research R4) |
| GET | `/api/public/blog/posts/{slug}/assets/{assetId}` | Image of a published post |

Public handler rules: `GET` only; slug matched against a strict pattern before any query; one query, joined to
the author approved (`approval_status = APPROVED`: suspended and rejected authors are hidden; their assistant workspace state is not consulted); no author id, email, profile, chat or run identity in any field; no Hermes call;
one shared per-minute allowance (600) for the public JSON reads and the HTML pages together; per-caller limiting is not done (the server cannot yet tell callers apart behind its proxies); `X-Content-Type-Options: nosniff`; CSP stays as strict as the app shell (no new script or
frame sources); unknown, unpublished and hidden posts all return the same `404`.

## Route inventory test

A kept test lists every route registered without a session. It must equal the existing set (`/s/*`, login
pages, health/version) plus exactly the five public blog routes above; adding any other fails the build.
