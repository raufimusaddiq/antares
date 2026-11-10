# Admin API contract (active administrator only; same guard and same-origin rule as every `/api/admin/*` route)

Non-admin, pending or suspended callers: 401/403, nothing changes. Errors use the existing `{error:{code,message}}` shape.

## Posts

`GET /api/admin/blog-posts?state=visible|hidden_account|taken_down&q=&after=` → `{items:[{id,slug,title,summary,authorName,publishedAt,state,takedown?:{at,reason,byName}}], next}`. 20 per page, keyset on (publishedAt, id). Only published posts. No body, draft, version, check or reader field exists in the type.

`GET /api/admin/blog-posts/{postId}` → the list item plus the published document (same shape as the public post's `doc`). 404 for anything not published (drafts, unpublished, unknown) with one identical answer.

`POST /api/admin/blog-posts/{postId}/take-down` body `{reason}` (1–500 chars, required) → the item. 409 `ALREADY_TAKEN_DOWN` if so. 404 as above.

`POST /api/admin/blog-posts/{postId}/restore` → the item. 409 `NOT_TAKEN_DOWN`. If the author unpublished the post meanwhile, the mark is cleared, the post stays unpublished (so it leaves the Posts list) and the response is `{note:"The author has unpublished it; it stays unpublished."}`.

Each write: one transaction, one `audit_log` row (actor, action, post id, request id).

## Models

`GET /api/admin/models` → `{catalogue:"ok"|"unavailable", models:[{id,name,on,purposes:["chat"|…],defaultFor:[purpose],usage:{calls,tokens}|null}], purposes:{chat:{current,effective,warning?},summary:{…},blog_assistant:{…},image:{…},video:{…}}}`. `current` is "" for the service default; `warning` ∈ `not_listed|sunset|off|provider_unavailable` when the saved default cannot be used and the service default is in effect. `usage` is per model over 30 days, never per account.

`PUT /api/admin/model-defaults` body `{chat?:id, summary?:id, blog_assistant?:id}` ("" = service default; omitted = unchanged) → the same read as GET. 400 if the model is not offered for that purpose or is off.

`PUT /api/admin/model-policy` (existing) gains: 409 `MODEL_IS_DEFAULT` `{purposes:[…]}` if a blocked id is any purpose's default (including image/video).

`PUT /api/admin/media-models` (existing) unchanged, plus 400 if the chosen model is off.

All rejections change nothing and write no audit row.
