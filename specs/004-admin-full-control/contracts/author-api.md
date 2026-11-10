# Author API contract (Active accounts, own posts only)

`GET /api/blog/posts` (existing) — each item gains `takedown?:{at,reason}` when taken down. Never includes who acted.

`POST /api/blog/posts/{id}/publish` and update-published: 409 `TAKEN_DOWN` `{reason}` while taken down; nothing changes.

`GET /api/blog/notices` → `{notices:[{postId,title,reason,at}]}` — takedowns this account has not yet seen, newest first, max 5.

`POST /api/blog/notices/{postId}/seen` → 204; sets `takedown_seen_at`. Another author's post: 404.

Public routes (`auth.PublicRoutes`, `auth.PublicBlogRoutes`) and their kept tests are unchanged; a taken-down post answers exactly like a missing slug (404 body and headers identical).
