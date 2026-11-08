# Implementation Plan: Blog CMS with AI-Enriched Publishing

**Branch**: `001-blog-cms-openui-publish` | **Date**: 2026-11-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-blog-cms-openui-publish/spec.md`

## Summary

Antares gains a blog: every approved user writes posts in a native rich-text editor, and every post is
published through a **check-and-enrich step** performed by the author's own Hermes profile. Hermes reads the
draft, searches the web, and returns notes plus proposed OpenUI blocks with sources. Antares validates the
reply against the OpenUI allowlist, shows the author a preview, and publishes only what the author accepts.
Readers see published posts without an account through a narrow, read-only public surface modelled on the
existing shared-chat link (`/s/`), with server-injected page metadata and a read-only OpenUI renderer.

**Order of work (per the user and AGENTS.md "Design comes first"): the pages are designed in
`docs/design/antares.figcraft` before any code.** Stage 0 below is that design step; stages after it do not
start until the design pages are reviewed. The inventory of pages, states and flows to design is in
[contracts/design-pages.md](contracts/design-pages.md).

## Technical Context

**Language/Version**: Go (server, per `go.mod`), TypeScript with React 19 + Vite (web)

**Primary Dependencies**: existing `@openuidev/react-lang` 1.0.2 / `react-ui` 0.18.1 and the allowlist in
`web/src/openui/`; one new web dependency, a rich-text editor core (ProseMirror-based, e.g. TipTap), chosen in
[research.md](research.md) R1. No new Go dependency expected.

**Storage**: SQLite, new migration `0025_blog.sql` (posts, post versions, checks, post assets). Same database
as everything else.

**Testing**: `go test ./...` (store, chat-style harness, two-user IDOR, public-surface tests); vitest for the
editor, preview and read-only renderer; real-browser checks in `scripts/browser-check.mjs` for layout.

**Target Platform**: the existing two-container deployment; web app served from the Go binary's embedded
bundle (`internal/webui/dist`, rebuilt with the change).

**Project Type**: web application (Go backend + React frontend), existing.

**Performance Goals**: public post readable < 2 s on mobile for 95% of loads (SC-007); check preview within
2 min for a 1,500-word post for 90% of attempts (SC-002).

**Constraints**: constitution principles I-V; one unauthenticated surface only; no second LLM pipeline; no new
container; reader interactions cannot reach any authenticated API.

**Scale/Scope**: single-owner deployment, tens of users, hundreds of posts; no CDN or search service.

## Constitution Check

*Gate: passes before research; re-checked after design (below).*

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I. Default deny, backend-selected identity, ownership, admin blind | Pass, one justified exception | Writing, checking, publishing and the post list require an ACTIVE session; every query filters by `author_id`; a foreign id returns 404 identical to a missing one. The check uses the author's own binding, chosen by the backend. No admin route reads drafts. **Exception:** `GET /b/*` and `GET /api/public/blog/*` are unauthenticated and serve only published posts of ACTIVE authors. See Complexity Tracking. |
| II. Front end, not an agent | Pass | Enrichment is one Hermes run per check, using the existing per-run `instructions` mechanism (`hermes.RunRequest.Instructions`) with a versioned blog prompt next to `chat/openui_prompt.txt`. Antares validates and stores; it does not generate, call a provider, or run tools. |
| III. Honest, capability-driven UI | Pass | The blog UI is shown only when the capability probe reports run support and web search; checks show failed / timed out / cancelled / out of date as such; unsourced data is never rendered as sourced. |
| IV. Tests guard behaviour | Pass | Test list in [quickstart.md](quickstart.md); security tests (two-user ownership, public surface leaks nothing else, hidden-on-suspend, malicious markup rejected) are kept tests. |
| V. Small footprint | Pass | Two containers, SQLite, no queue: the check is an ordinary run that the existing run machinery tracks. One editor dependency, justified in R1. |

## Project Structure

### Documentation (this feature)

```text
specs/001-blog-cms-openui-publish/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── design-pages.md      # pages/states/flows to design in figcraft FIRST
│   ├── http-api.md          # authenticated + public endpoints
│   └── check-envelope.md    # what Hermes must return from a check
└── tasks.md                 # /speckit-tasks
```

### Source Code (repository root)

```text
docs/design/antares.figcraft         # Stage 0: new pages (Blog public, CMS, Editor, Publish flow)
docs/design/README.md                # page list and flows updated in the same PR

internal/store/migrations/0025_blog.sql
internal/blog/                       # NEW package
├── posts.go                         # CRUD, ownership-scoped queries, slug rules, autosave/versioning
├── check.go                         # start/poll/cancel a check as a Hermes run; apply result
├── envelope.go                      # parse + validate the check reply (contracts/check-envelope.md)
├── validate_openui.go               # allowlist validation of block markup on the server
├── assets.go                        # draft-private images, published when the post is
├── public.go                        # unauthenticated read handlers + page metadata injection
├── blog_prompt.txt                  # versioned instruction for the check run
└── *_test.go
internal/server/                     # route wiring, public-route exemptions, CSP/robots headers
internal/chat/ (read-only reuse)     # capability probe, run plumbing patterns, share.go as precedent

web/src/blog/                        # NEW
├── Editor.tsx                       # rich-text editor + autosave
├── PostList.tsx                     # CMS list
├── PublishFlow.tsx                  # checking → preview → accept/reject → publish
├── Preview.tsx                      # original vs enriched, AI-added markers, sources
├── PublicPost.tsx, PublicIndex.tsx  # signed-out reader pages
└── *.test.tsx
web/src/openui/                      # read-only library variant (no @ToAssistant) for public posts
scripts/browser-check.mjs            # public page + phone layout checks
```

**Structure Decision**: extend the existing Go + React web application. One new Go package (`internal/blog`)
and one new web folder (`web/src/blog`) keep the feature separable from chat. The OpenUI allowlist and
renderer are reused, with a read-only variant for readers.

### Stages

0. **Design in figcraft** (this PR's first deliverable): add the blog pages, states and click flows to
   `docs/design/antares.figcraft`, using the existing variables/styles; update `docs/design/README.md`.
   Review gate: pages approved before code starts.
1. **Data + CMS core (User Story 1)**: migration, posts CRUD, autosave, editor, post list, publish original,
   unpublish, delete, public read of plain posts, suspend-hides. Ownership/IDOR tests first.
2. **Check + enrich (User Story 2)**: blog prompt, check run, envelope validation, preview, accept/reject,
   failure states, out-of-date handling.
3. **Sourced data (User Story 3)**: source requirements enforced in validation, source drawer, retrieved-on
   dates, unsourced notes.
4. **Reader experience (User Story 4)**: read-only renderer, fallbacks, metadata/sharing previews, a11y,
   mobile, dark mode.
5. **Polish (User Story 5 remainder)**: duplicate, covers, address editing, limits shown up front.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| First route family served without a session (`/b/*`, `/api/public/blog/*`), an exception to "default deny" (Principle I) | The owner decided posts are public to anyone (spec Q1). A blog nobody can open is not a blog. | Signed-in-only reading was offered and declined. The exception is made safe by construction: handlers take a slug only, run a single query for published posts of ACTIVE authors, touch no Hermes, expose no account, profile or chat identity, and are rate limited. This mirrors the shipped `/s/` link design. A test enumerates every route and fails if an unauthenticated route is added outside this list. |
| One new web dependency (editor core) (Principle V) | A Medium-class writing experience cannot be built honestly on a textarea, and a hand-written rich-text editor is a larger risk. | Markdown textarea with preview: rejected by the spec's "native like WordPress or Medium". Hand-rolled contenteditable: rejected as a known source of data loss, against SC-006. |

## Post-Design Constitution Re-check

After Phase 1 design the table above still holds. Two risks are recorded rather than assumed away:
(1) whether Hermes can report which URLs a web-search run actually visited, which decides how strongly a
block's sources can be cross-checked (research R4; fallback is an explicit "unverified" label that forces
author acceptance); (2) whether a helper run can be created without a visible chat (research R3; fixtures
must be recorded from the deployed version before Stage 2, per AGENTS.md).
