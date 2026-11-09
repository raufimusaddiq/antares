---

description: "Task list for Blog CMS with AI-Enriched Publishing"
---

# Tasks: Blog CMS with AI-Enriched Publishing

**Input**: Design documents from `specs/001-blog-cms-openui-publish/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ (design-pages.md, http-api.md, check-envelope.md), quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour; security tests are never removed.

**Organization**: Design first (Phase 1, in figcraft), then code built from that design. Within code, tasks are grouped by user story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1..US5 from spec.md
- Paths are from the repository root. Go in `internal/`, web in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design: read its variables and styles, match them in `web/src/styles.css`. Where code and design disagree, say so in the PR and fix the wrong side (AGENTS.md "Design comes first").
- Rebuild `internal/webui/dist` with any web change (needs Linux/Docker; `TestBundleIsNotStale` enforces it).
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push.

---

## Phase 1: Design in figcraft (blocks all code)

**Purpose**: Draw every blog page, state and flow from [contracts/design-pages.md](contracts/design-pages.md) in `docs/design/antares.figcraft`, Light + Dark, Phone (390) + Desktop (1280). Use the existing `Antares` variables and styles; create no new colours without adding a variable.

- [x] T001 [US1] Design **Blog · Public** page: index (populated, empty, loading, error), text-only post, not-found, in `docs/design/antares.figcraft`
- [x] T002 [US4] Design enriched post frames on **Blog · Public**: sortable table, chart with hover detail, metric cards, accordion, sources list with retrieved-on date, AI-added marker, one block in plain-fallback state
- [x] T003 [US5] Design **CMS · My posts**: list with all five status chips, empty/loading/error, row actions, delete confirm, sidebar entry point and capability-hidden state
- [x] T004 [US1] Design **CMS · Editor**: empty, writing with toolbar, selection toolbar, save states (saving, saved, unsaved/offline, conflict side-by-side), image upload states, post-settings sheet with address-conflict error, phone layout
- [x] T005 [US2] Design **Publish flow**: entry choice (check and enrich vs publish original), checking with Cancel, preview (original vs enriched, notes, AI-added badge, per-block Accept/Reject, Accept all), published success with Copy link
- [x] T006 [US3] Design source states in the preview: sources drawer, verified vs unverified, explicit acknowledgement, unsourced-note
- [x] T007 [US2] Design failure states: failed, timed out, cancelled, disconnected, out of date, rejected-block notice, assistant/web-search unavailable (publish original only)
- [x] T008 Design the **Admin** suspended-user row note (hidden posts, no content shown) on the existing Admin page in `docs/design/antares.figcraft`
- [x] T009 Wire the 8 click flows from `contracts/design-pages.md` as prototype interactions in `docs/design/antares.figcraft`
- [x] T010 Update `docs/design/README.md` with the new pages, states, flows and any known gaps; run the overlap audit (no stacked or doubled layers) and save the file
- [x] T011 **Design review gate**: owner reviews the new pages in figcraft; fixes applied. No task below starts until approved. Open the design PR on its own (design file + README only).

**Checkpoint**: design approved and merged; it is now the source of truth for everything below.

---

## Phase 2: Setup

- [x] T012 Start implementation from a branch cut from `origin/main` after the design PR merges (merge `origin/main` if behind, never rebase), and create empty packages `internal/blog/` and `web/src/blog/`
- [x] T013 [P] Evaluate and add the editor dependency to `web/package.json` (R1); record bundle-size delta and licence in `specs/001-blog-cms-openui-publish/research.md`
- [X] T014 [P] (recorded 2026-11-09 with `antares-smoke --events-out`: the events of a run that used web search, from an ordinary chat run; R4 resolved in research.md. A run created with no chat row is NOT recorded: that is T014b.) Record Hermes fixtures (keys redacted) for the web-search tool events and a final output into `internal/hermes/testdata/blog_check_*.json`; update `specs/001-blog-cms-openui-publish/contracts/check-envelope.md` to the real shape (R3, R4). Required before Phase 5
- [ ] T014b Create a Hermes session and run with no Antares chat row, through a synthetic smoke account, and record that it works and is not listed (R3); first test of Phase 5

---

## Phase 3: Foundational (blocks all stories)

- [X] T015 (posts, post_versions and post_assets; `post_checks`, `post_blocks` and `block_sources` come with Phase 5) Write `internal/store/migrations/0026_blog.sql`: tables `posts`, `post_versions`, `post_checks`, `post_blocks`, `block_sources`, `post_assets` exactly as in `data-model.md` (status values `draft`/`checking`/`ready`/`published`/`unpublished`; slug unique among published posts; cascade deletes; `author_id` on every user-facing table; `https`-only source urls enforced in code)
- [X] T016 [P] Document node/mark allowlist and JSON schema validator in `internal/blog/doc.go` (paragraph, heading 2-4, bold, italic, link, bullet/ordered list, blockquote, code, code block, image, divider; size and depth limits); table-driven test in `internal/blog/doc_test.go` rejecting script, raw HTML, unknown nodes and `javascript:` links
- [X] T017 [P] Slug rules in `internal/blog/slug.go`: lowercase `a-z0-9-`, 3-80 chars, reserved words refused; test in `internal/blog/slug_test.go`
- [X] T018 Blog test harness `internal/blog/harness_test.go`: two ACTIVE users A and B, mock Hermes, in-memory store (one shared harness, no per-file copies)
- [X] T019 Capability probe `GET /api/blog/capabilities` in `internal/blog/capabilities.go`, reusing `internal/chat/capabilities.go`; wire route in `internal/server/`; test that unconfigured run or web search reports unavailable (FR-029)
- [X] T020 Route inventory test in `internal/blog/routes_public_test.go` (it needs the blog and identity layers together) listing every route served without a session; fails on any addition outside the existing set plus the five public blog routes (Complexity Tracking)

**Checkpoint**: schema, validators and harness exist; stories can start.

---

## Phase 4: User Story 1 - Write and publish a plain post (Priority: P1) 🎯 MVP

**Goal**: approved user writes, autosaves, publishes the original, readers see it signed out; unpublish, delete; suspend hides.

**Independent Test**: quickstart scenarios 1, 2, 9, 10, 11.

### Tests for User Story 1 (write first, must fail)

- [X] T021 [P] [US1] Two-user ownership test: B cannot read, edit, publish, delete or fetch assets of A's post; all return the same `404` as a missing id, in `internal/blog/posts_ownership_test.go` (kept security test)
- [X] T022 [P] [US1] Public surface test: only published posts of ACTIVE authors; unpublished, draft, suspended-author and unknown slugs return identical `404`; response contains no author id, email, profile or chat identity, in `internal/blog/public_test.go`
- [X] T023 [P] [US1] Autosave compare-and-set test: stale `baseVersion` returns `409` with the latest; no silent overwrite, in `internal/blog/posts_test.go`
- [X] T024 [P] [US1] Suspend-hides/reinstate-restores test using the author's current status in the public query, in `internal/blog/public_test.go`
- [X] T025 [P] [US1] Pending, rejected and suspended users get refused on every authenticated blog route (FR-025), in `internal/blog/access_test.go`
- [x] T026 [P] [US1] Web test: editor restores the draft after reload and keeps a local copy until the server confirms, in `web/src/blog/Editor.test.tsx`

### Implementation for User Story 1

- [X] T027 [US1] Posts store in `internal/blog/posts.go`: create, get, list, autosave (compare-and-set on `latest_version`), settings, publish original (pin `published_version`), unpublish, delete, duplicate; every statement filters by `author_id`
- [X] T028 [US1] Draft-private image upload and author read in `internal/blog/assets.go` (PNG/JPEG/WebP/GIF only, size limit, server-side content sniff, `nosniff`); public read only for published posts
- [X] T029 [US1] Authenticated handlers per `contracts/http-api.md` in `internal/blog/handlers.go`; reject any client-supplied profile, prefix, key, header or session id; wire in `internal/server/`
- [X] T030 [US1] Public handlers and metadata injection in `internal/blog/public.go`: strict slug pattern, single join query to ACTIVE author, per-IP rate limit, `Cache-Control`, escaped `<title>`/description/Open Graph and escaped text copy into the HTML shell, no `noindex`; register the five public routes in `internal/server/`
- [x] T031 [P] [US1] Editor component with toolbar, autosave and save states per the design in `web/src/blog/Editor.tsx` and `web/src/blog/blogApi.ts`
- [x] T032 [P] [US1] Post settings sheet (title, summary, cover, address with conflict error) in `web/src/blog/PostSettings.tsx`
- [x] T033 [P] [US1] Read-only document renderer for reader pages in `web/src/blog/DocView.tsx` (no raw HTML path)
- [x] T034 [US1] Public pages `web/src/blog/PublicIndex.tsx` and `web/src/blog/PublicPost.tsx` per the design, signed-out routes in `web/src/App.tsx`, light/dark and phone
- [x] T035 [US1] "Publish my original", unpublish and published-success UI in `web/src/blog/PublishFlow.tsx` (original path only for now)
- [X] T036 [US1] Add Blog entry to the app navigation, hidden when the capability probe says unavailable, in `web/src/App.tsx`
- [X] T037 [US1] Real-browser checks `blog-public` and `blog-editor` (390 px no sideways scroll, light/dark) in `scripts/browser-check.mjs`
- [X] T038 [US1] Rebuild `internal/webui/dist`, run `go test ./...` and vitest; fix design/code disagreements found

**Checkpoint**: a complete Medium-style writing and publishing tool. MVP.

---

## Phase 5: User Story 2 - AI check and enrichment (Priority: P1)

**Goal**: Publish starts a Hermes check; author previews notes and AI-added blocks; accepts, rejects per block, or publishes the original; every failure is shown as itself.

**Independent Test**: quickstart scenarios 3, 4, 5, 7, 8.

**Depends on**: US1 and T014 (fixtures).

### Tests for User Story 2

- [X] T039 [P] [US2] Envelope validation table test: valid envelope; bad JSON; unknown version; oversize; `afterNode` not in the checked version; script/raw HTML/unknown component/`@ToAssistant`/unregistered action in markup, all dropped with a notice, in `internal/blog/envelope_test.go`
- [ ] T040 [P] [US2] Check lifecycle test: running → ready; failed, timed out, cancelled, disconnected each shown as itself, nothing published, post returns to `draft`, in `internal/blog/check_test.go`
- [ ] T041 [P] [US2] Out-of-date test: editing the head version during `running`/`ready` marks the check `out_of_date` and publish-enriched is refused, in `internal/blog/check_outofdate_test.go`
- [ ] T042 [P] [US2] Two-user test: B cannot start, read, cancel or accept blocks on A's check; the check uses A's binding and never B's, in `internal/blog/check_ownership_test.go`
- [ ] T043 [P] [US2] Web test: Preview shows AI-added badge per block, Accept all / Reject one / Publish original, and each failure state wording, in `web/src/blog/PublishFlow.test.tsx`

### Implementation for User Story 2

- [ ] T044 [US2] Versioned instruction `internal/blog/blog_prompt.txt` (check the writing, propose blocks, search the web, return the envelope, never rewrite the text); embed and expose its version
- [X] T045 [US2] Server-side OpenUI validation against the read-only reader allowlist (no `@ToAssistant`, no submitting forms; `@OpenUrl` only to `https` urls listed in the block's sources) in `internal/blog/validate_openui.go`; keep the allowlist in step with `web/src/openui/validate.ts` (server side done in `openui.go`; the matching read-only library in the browser is T061)
- [X] T046 [US2] Envelope parser and validator per `contracts/check-envelope.md` in `internal/blog/envelope.go`
- [ ] T047 [US2] Check runner in `internal/blog/check.go`: create the Hermes run through the author's backend-selected binding with `Instructions`, track run states, persist notes and proposed blocks, cancel, mark out of date; error codes carry no provider text or credential
- [ ] T048 [US2] Block decision, publish-enriched (snapshot accepted blocks with the pinned version) and check handlers in `internal/blog/handlers.go` per `contracts/http-api.md`
- [x] T049 [P] [US2] Publish flow UI: choice, checking with Cancel, Preview with notes and per-block Accept/Reject in `web/src/blog/PublishFlow.tsx` and `web/src/blog/Preview.tsx`
- [x] T050 [P] [US2] Failure and out-of-date states, rejected-block notice and unavailable state per the design in `web/src/blog/PublishFlow.tsx`
- [ ] T051 [US2] Browser check `blog-publish` (checking, preview, phone layout) in `scripts/browser-check.mjs`; rebuild `internal/webui/dist`; run all tests

**Checkpoint**: enriched publishing works end to end against the mock; real Hermes confirmed by T014 fixtures.

---

## Phase 6: User Story 3 - Data comes from sources (Priority: P2)

**Goal**: every data-bearing block shows sources and a retrieved-on date; unsourced claims become notes, never figures.

**Independent Test**: quickstart scenario 6, plus verified/unverified acknowledgement.

### Tests for User Story 3

- [X] T052 [P] [US3] Data block with no sources is dropped and an `unsourced` note added; non-`https`, credentialed, loopback or private-host source urls are rejected, in `internal/blog/sources_test.go`
- [ ] T053 [P] [US3] Source `verified` is set only when the url appears in the run's observed search results; unverified blocks cannot be accepted without `ack`, in `internal/blog/sources_verify_test.go`
- [ ] T054 [P] [US3] Web test: sources drawer, verified/unverified marking, acknowledgement required, in `web/src/blog/Sources.test.tsx`

### Implementation for User Story 3

- [ ] T055 [US3] Source validation and verification against run tool events in `internal/blog/sources.go` (falls back to "unverified" if events are not exposed, per research R4); enforce `ack` in the block decision handler
- [x] T056 [P] [US3] Sources drawer and acknowledgement UI in `web/src/blog/Sources.tsx`; show sources and retrieved-on date on published data blocks in `web/src/blog/PublicPost.tsx`
- [ ] T057 [US3] Re-run behaviour: republish after edits re-checks and drops or re-proposes blocks tied to changed text in `internal/blog/check.go`; test in `internal/blog/check_republish_test.go`

**Checkpoint**: published data is never an unlabelled model claim.

---

## Phase 7: User Story 4 - Readers interact with the post (Priority: P2)

**Goal**: signed-out readers use native blocks; failures degrade to plain text; accessible, responsive, shareable.

**Independent Test**: quickstart scenarios 13, 14.

### Tests for User Story 4

- [ ] T058 [P] [US4] Read-only renderer test: malicious markup (unregistered action, `/admin/*` fetch, unknown component) is refused with no request made and no side effect; a block that throws falls back to its plain text and the rest renders, in `web/src/blog/ReaderBlocks.test.tsx`
- [ ] T059 [P] [US4] Reader interaction test: sort, expand and hover work with no network call to any authenticated API, in `web/src/blog/ReaderInteraction.test.tsx`
- [ ] T060 [P] [US4] Metadata test: shell contains escaped title, description, canonical and Open Graph tags for a published post and none for unpublished, in `internal/blog/public_meta_test.go`

### Implementation for User Story 4

- [ ] T061 [US4] Read-only OpenUI library variant (actions removed) in `web/src/openui/readerLibrary.ts`; renderer wrapper with per-block error boundary and fallback in `web/src/blog/ReaderBlocks.tsx`
- [ ] T062 [US4] Reader page polish per the design: keyboard and screen-reader order, dark/light, phone, AI-added marker, in `web/src/blog/PublicPost.tsx` and `web/src/styles.css`
- [ ] T063 [US4] Add CSP/robots handling for `/b/*` in `internal/server/` (indexable, same strict script/frame sources); extend `web/src/openui/csp.test.ts` if affected
- [ ] T064 [US4] Browser checks `blog-reader-phone`, `blog-reader-dark`, `blog-reader-keyboard` in `scripts/browser-check.mjs`; rebuild `internal/webui/dist`

**Checkpoint**: the reader experience matches the design on desktop and phone.

---

## Phase 8: User Story 5 - Manage my posts (Priority: P3)

**Goal**: author sees only their posts with status; duplicate, unpublish, delete.

**Independent Test**: quickstart scenario 9 and the list states.

### Tests for User Story 5

- [ ] T065 [P] [US5] List test: only my posts, correct status chips for all five states, in `internal/blog/list_test.go`
- [ ] T066 [P] [US5] Web test: list states (empty, loading, error), row actions, delete confirm, in `web/src/blog/PostList.test.tsx`

### Implementation for User Story 5

- [x] T067 [US5] CMS list UI per the design in `web/src/blog/PostList.tsx` (status chips, row actions, delete confirmation)
- [ ] T068 [US5] Show limits (size, images, blocks) to the author before publishing in `web/src/blog/PublishFlow.tsx` and `internal/blog/handlers.go`
- [ ] T069 [US5] Browser check `blog-list` in `scripts/browser-check.mjs`; rebuild `internal/webui/dist`

---

## Phase 9: Polish and cross-cutting

- [ ] T070 [P] Admin page: suspended-user note per the design, confirm no endpoint or screen exposes post or draft content, in `web/src/Admin.tsx` and `internal/blog/admin_blind_test.go` (kept security test)
- [ ] T071 [P] Secrets and log hygiene test: no credential, run id or provider text in any blog response, error or log, in `internal/blog/redaction_test.go`
- [X] T072 [P] Update `docs/PRD.md` (new section for the blog and the public-surface exception), `docs/runbooks/security-invariants.md` and `.specify/memory/constitution.md` Principle I wording in the same PR, bumping the constitution version
- [ ] T073 Update `docs/design/README.md` to match what shipped (known gaps), and reconcile any code/design disagreement
- [ ] T074 Run every quickstart scenario against the built bundle; record per-package `go test -cover` before and after in the PR description
- [ ] T075 Deploy only with the owner's explicit approval, following `docs/runbooks/deployment.md` (backup, release, verify `/api/version`, smoke test, clean up smoke accounts)

---

## Dependencies and order

- Phase 1 (design) → Phase 2 (setup) → Phase 3 (foundation) → stories.
- US1 first (MVP). US2 needs US1 and T014. US3 extends US2. US4 needs US1 (and US2 for blocks). US5 can run alongside US2-US4 once US1 is done.
- Within a story: tests (fail first) → store/server → UI → browser check → bundle rebuild.
- T011 (design gate) and T014 (fixtures) are hard blockers.

## Parallel opportunities

- Phase 1: T001-T008 touch different pages of the same figcraft file, so draw them one at a time or in separate frames carefully; T009/T010 follow.
- Phase 2: T013 and T014 together.
- Phase 3: T016 and T017 together.
- US1 tests T021-T026 together; UI T031-T033 together.
- US2 tests T039-T043 together; UI T049/T050 together.
- US3 tests T052-T054; US4 tests T058-T060; US5 tests T065-T066.

## Implementation strategy

1. MVP = Phases 1-4: design, then a complete write-and-publish blog without AI.
2. Add US2 with the mock, switch to real Hermes once T014 fixtures exist.
3. US3 and US4 next (honesty of data, then reader polish), US5 and polish last.
4. Ship each phase as its own PR from `origin/main`: design PR (T001-T011), then one PR per story.
