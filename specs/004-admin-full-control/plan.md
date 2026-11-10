# Implementation Plan: Admin Full Control of Models and Public Posts

**Branch**: `004-admin-full-control` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/004-admin-full-control/spec.md`

## Summary

Two admin capabilities, built on what exists:

1. **Post takedown** (P1). A published post gets a takedown mark (who, when, reason). The one row filter every public read
   already shares (`publicWhere`) learns `AND taken_down_at = ''`, so the index, search, latest posts, a post's address
   and the link-preview metadata all stop showing it together, and answer "not found" exactly as for a missing post.
   The admin gets a Posts screen (list incl. posts hidden by their author's account state, a published-content view,
   take down with reason, restore). The author sees the state and reason on their posts page, cannot republish, and gets a
   one-time banner (server-side "seen" mark, so it follows them across devices).
2. **Models** (P1 + P2). One admin Models screen: every Kenari model with an on/off switch (the existing global block
   list, finally with a screen; switching off a model that is a purpose's default is refused), a default per purpose
   (chat, summaries and titles, blog assistant, plus the existing images and videos), per-model usage totals, and a
   visible warning when a default stopped being usable. New table `model_defaults` holds the three new defaults; images and
   videos keep their current storage and re-verify behaviour.

No new service, queue, dependency or route outside `/api/admin/*` and the author-only `/api/blog/*`. `auth.PublicRoutes` /
`auth.PublicBlogRoutes` do not change (constitution Principle I). See [research.md](research.md) for the decisions and
[contracts/](contracts/) for the interfaces.

Per AGENTS.md ("Design comes first") and the owner's standing rule, every screen is drawn in
`docs/design/antares.figcraft` (light and dark, phone and desktop, with empty/loading/error states) and approved before code
(Stage 0, first in [tasks](tasks.md) when generated).

**Owner decisions (2026-11-10)**: (1) the admin-blindness guard change (explicit allowlist of the four takedown routes,
`reader`/`reads`/`views` still banned, new tests for published-only fields) is approved; (2) the admin sees published
content only, never drafts, unpublished versions, post checks or reader data, because published posts are public anyway.

## Technical Context

**Language/Version**: Go (server), TypeScript + React 19 + Vite (web).

**Primary Dependencies**: none new.

**Storage**: SQLite. One migration `0028_admin_control.sql`: four columns on `posts` (`takedown_at`, `takedown_by`,
`takedown_reason`, `takedown_seen_at`, each `TEXT NOT NULL DEFAULT ''`) and a table `model_defaults(purpose PRIMARY KEY CHECK
IN ('chat','summary','blog_assistant'), model_id, set_by, set_at)`. The block list (`model_blocked`) and media defaults
(`tenant_tool_policy`) are reused unchanged.

**Testing**: Go tests (takedown hides every public surface at once, restore returns it unchanged, publish refused while
down, non-admin refused, admin never receives a draft/unpublished version/check/reader data, audit rows, concurrent
double take-down, default-model resolution per purpose, switch-off refused for a default, fallback with warning, blocked
model refused for chat/summary/title/post check); vitest for the Posts and Models screens and the author banner; real
browser checks in `scripts/browser-check.mjs` (new views `admin-posts`, `admin-models`, extended `blog-editor`) at 320/390/820/1280
in both themes; the kept admin-blindness tests amended as described under Complexity Tracking.

**Target Platform**: existing embedded web bundle (`internal/webui/dist`, rebuilt with the change).

**Project Type**: web application, existing.

**Performance Goals**: admin lists 20 per page by keyset, one read each; a takedown is one transaction, visible to the
next uncached public read; the existing 60 s `max-age` (R4) bounds the rest, so SC-001 holds within one minute. Post images follow the same filter (up to 5 min cached).

**Constraints**: constitution I–V; CSP unchanged; the admin never receives a draft, unpublished version, post check or
reader activity; no model provider is called on a user's behalf for chat.

**Scale/Scope**: 1 migration, ~6 admin routes + 2 author routes, 2 admin screens, 1 banner, 3 defaults wired into chat, titles/summaries and post check.

## Constitution Check

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I. Default deny, admin blind | Pass, with one documented widening | No route is added to `auth.PublicRoutes`/`PublicBlogRoutes`; a taken-down row is simply excluded by the shared filter. The admin gains a view of **published** content (already public) but still no drafts, unpublished versions, post checks, reader or read data, conversations, memory or files. This changes `security-invariants.md` §6 wording and the amended guard tests (Complexity Tracking); constitution gets a MINOR (1.2.0) sentence naming the takedown surface. |
| II. Front end, not an agent | Pass | Defaults only choose which model Hermes (chat, post check) or the existing gateway helper (titles, summaries) is told to use. No second LLM framework, no provider call for chat. |
| III. Honest UI | Pass | The Models screen lists only what Kenari serves; an unreadable catalogue is stated, not papered over (FR-014); a default that stopped working shows a warning and the fallback is visible (FR-018). |
| IV. Tests guard behaviour | Pass, one guard changed | See Complexity Tracking: the "no admin route names a post" word guard is replaced by an explicit allowlist of four routes plus stronger positive tests. Nothing is skipped. |
| V. Small footprint | Pass | One migration, no service, queue or dependency. |

Re-check after Phase 1: unchanged (contracts add admin-only and author-only routes).

## Complexity Tracking

| Departure | Why needed | Simpler alternative rejected because |
|-----------|-----------|--------------------------------------|
| `TestNoAdminRouteNamesPostsReadersOrReads` no longer bans the word "post"/"blog" for the four takedown routes, and `TestEveryAdminGetRouteIsScannedForPostContent` gains the new GET routes | Takedown is an admin action on posts; the word ban was written when admins had no post surface (spec 001 R8) | Naming the routes `/api/admin/takedowns` would pass the old test while hiding the same fact; the guard's purpose (no drafts, no readers, no reads) is kept literally: words `reader`, `reads`, `views` stay banned everywhere, and new tests prove the four routes return only published fields. |
| Admin sees published post content | Judging a post needs reading it | Link to the public address only works for visitors-visible posts; posts hidden by account state (FR-001) would be unreviewable |

## Project Structure

### Documentation (this feature)

```text
specs/004-admin-full-control/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── admin-api.md
│   ├── author-api.md
│   └── ui-contract.md
└── tasks.md             # /speckit-tasks
```

### Source Code (repository root)

```text
internal/store/migrations/0028_admin_control.sql
internal/store/modeldefaults.go            # model_defaults, usage totals per model
internal/blog/takedown.go                  # TakeDown, Restore, AdminList, AdminGet, notices
internal/blog/public.go, posts.go, check.go# publicWhere + publish guard + blog_assistant default
internal/server/blogpages.go               # PostMeta follows publicWhere (no code change expected; test)
internal/auth/admin_takedown.go            # four admin routes
internal/auth/admin_models.go              # GET /api/admin/models, PUT /api/admin/model-defaults, default guard on model-policy
internal/chat/{service,modelprefs,title,summary}.go  # chat/summary defaults
web/src/{AdminPosts.tsx,AdminModels.tsx,adminPostsApi.ts,adminModelsApi.ts}  # screens (nav + audit labels in Admin.tsx)
web/src/blog/                              # author state, banner
docs/design/antares.figcraft               # Stage 0 designs
```

**Structure Decision**: extend the existing packages; no new package.

## Stage 0 — Design (figcraft, before any code)

Page "Admin control" in `docs/design/antares.figcraft`, light and dark duplicates, 1280 and 390 wide:

1. Admin → Posts: list (title, author, date, state chip: Visible / Hidden (author's account) / Taken down), filter, search, empty, loading, error.
2. Post view (published content only) with Take down; take-down confirm form (required reason, counter); restore confirm; "already taken down" message.
3. Admin → Models: switch rows (on/off) with usage totals and "default for chat…" chips; per-purpose default pickers (5 purposes); refused switch-off message naming purposes; fallback warning; catalogue-unavailable state; saving state.
4. Author: posts page row in the Taken down state (reason, cannot publish), one-time banner in chat/app shell, publish control disabled with reason.
5. Component check against the design system (reuse state chip, confirm form, table-to-cards on phone, secondary/primary pills from spec 003); any new component is added to `docs/design/README.md`.

Owner approval gate follows Stage 0; implementation starts only after it.
