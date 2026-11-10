---

description: "Task list for Admin Full Control of Models and Public Posts"
---

# Tasks: Admin Full Control of Models and Public Posts

**Input**: Design documents from `specs/004-admin-full-control/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/{admin-api,author-api,ui-contract}.md, quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour.

**Organization**: Design first, **admin screens first** (Phase 1, in figcraft, owner-approved before any code), then the shared foundation, then one phase per user story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 (takedown), US2 (model switches), US3 (defaults per purpose) from spec.md
- Paths are from the repository root. Web code is in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design (`docs/design/antares.figcraft`, page "Admin control"): read its variables and styles and match them in `web/src/styles.css`. Where code and design disagree, say so in the PR and fix the wrong side.
- Rebuild `internal/webui/dist` with any web change (`TestBundleIsNotStale` enforces it). Browser code stays inside the allowlist in `internal/webui/webui_test.go`.
- No new unauthenticated route; `auth.PublicRoutes`, `auth.PublicBlogRoutes` and their kept tests stay unchanged. A taken-down post answers exactly as a missing one (same status, body and headers).
- The admin never receives a draft, unpublished version, post check or reader/read data (owner decision 2026-11-10). Admin responses are allowlist structs.
- Cloudflare is not modified. Never touch real users' accounts in tests or smoke; clean up fixtures.
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push; merge commit only.

---

## Phase 1: Design in figcraft (blocks all code)

**Purpose**: every screen, state and flow is drawn in `docs/design/antares.figcraft` on a new page **Admin control**, using the existing variables, the admin frames (users table, Suspend confirm form, Assistant media/tools) and spec 003's pills; the owner approves before Phase 2. **Admin screens come first.**

- [x] T001 Read the existing admin frames, the confirm form, the table-to-cards phone layout, the state chips and the author's posts page with figcraft `inspect_document` and `screenshot`; create page **Admin control** in `docs/design/antares.figcraft`
- [x] T002 [US1] Draw **Admin → Posts** at desktop 1280 and phone 390: nav item "Posts", list (title, author, date, state chip Visible / Hidden (author's account) / Taken down), state filter, search, loading, empty, no-match, error with Retry, "Show more"
- [x] T003 [US1] Draw the **post view** (published content only) in each state (visible, hidden by account, taken down), the **Take down** confirm form (required reason, 500-character counter, busy, error), the **Restore** confirm, the "already taken down / not taken down" notices and the "author unpublished it, stays unpublished" note, and the line in the take-down form that a takedown covers Antares only, not copies already saved elsewhere (spec edge case); phone and desktop
- [x] T004 [US2] Draw **Admin → Models** at desktop and phone: nav item "Models", one row per model with an on/off switch, usage totals (calls, tokens; never per account) and "default for …" chips; saving and saved states; the refused switch-off message naming the purposes; catalogue-unavailable state (current switches kept)
- [x] T005 [US3] Draw the **purpose defaults** block on the Models screen: five pickers (Chat, Summaries and titles, Blog assistant, Images, Videos) each offering only fitting, switched-on models plus "Service default"; the fallback warning ("saved default no longer usable, service default in use") naming purpose and model; keep Images/Videos behaving as today ("re-checking accounts")
- [x] T006 [US1] Draw the **author side**: posts-page row in the Taken down state (reason shown, Publish disabled with the reason, Edit still allowed), the one-time banner in the app shell (one takedown, several, dismissed) on phone and desktop
- [x] T007 Screenshot every frame in light **and dark** (dark = duplicates with `variable.setMode`, 9000 below, as in "Blog discovery"), compare with neighbouring screens (spacing, type, 44 px targets, 320 px width), fix, and ask the owner to approve (expect a comment round, `comment.list`); save with `save_file` and an explicit path
- [x] T008 Update `specs/004-admin-full-control/contracts/ui-contract.md` and `docs/design/README.md` (new "Admin control" frames, any new component) to match the approved design

**Checkpoint**: owner approved the design (T007, T008 done); Phase 2 may start.

---

## Phase 2: Foundational (shared by all stories)

**Purpose**: schema, store, guards and docs every story uses. No story starts before this.

- [x] T009 Add `internal/store/migrations/0028_admin_control.sql`: `posts.takedown_at/takedown_by/takedown_reason/takedown_seen_at` (each `TEXT NOT NULL DEFAULT ''`) and `model_defaults(purpose TEXT PRIMARY KEY CHECK (purpose IN ('chat','summary','blog_assistant')), model_id TEXT NOT NULL, set_by TEXT NOT NULL, set_at TEXT NOT NULL)`; migration test in `internal/store/` that an existing database upgrades with all posts `takedown_at = ''`
- [ ] T010 [P] Add `internal/store/modeldefaults.go`: `ModelDefaults`, `SetModelDefault(actor, purpose, model, reqID, now)` (audit action `model_defaults.set`, in one transaction; model_id "1–200 chars, no control characters"), `DefaultPurposes(model)` (all five purposes incl. image/video from `TenantMediaModels`) and `ModelUsageTotals(since)` (COUNT and SUM of tokens grouped by model over `runs.usage_model` and `post_checks.usage_model`; no user column selected); tests in `internal/store/modeldefaults_test.go` (totals cover chat and post-check runs only; titles and summaries are not recorded)
- [x] T011 [P] Amend the admin-blindness guards in `internal/auth/admin_blog_test.go` per plan Complexity Tracking: `TestNoAdminRouteNamesPostsReadersOrReads` allows exactly the four `/api/admin/blog-posts` routes by explicit list, keeps banning `reader`, `reads`, `views` everywhere; add the new GET routes to `scanned()` (the positive published-fields-only test is T015, so this phase leaves CI green)
- [x] T012 [P] Update `docs/runbooks/security-invariants.md` §6 and `AGENTS.md` wording (admin may list and take down published posts and read their published content; still no drafts, unpublished versions, checks, readers) and amend `.specify/memory/constitution.md` to **1.2.0** (MINOR: materially expanded guidance) with the matching sentence in Principle I and a Sync Impact Report; link spec 001 FR-015 to this spec

**Checkpoint**: schema exists, guards are amended, CI is green; stories can proceed. Each story can ship as its own PR.

---

## Phase 3: User Story 1 - Take down a public post (Priority: P1) 🎯 MVP

**Goal**: an administrator takes a published post down with a reason; it vanishes from every public surface; the author is told and cannot republish; restore brings it back unchanged.

**Independent Test**: publish as a fixture author; take down as admin; confirm the index, search, latest posts, address (404 identical to a missing slug), link preview and images no longer show it; the author sees state, reason and a one-time banner; restore returns it unchanged; audit rows exist.

### Tests for User Story 1 (write first, they must fail)

- [x] T013 [P] [US1] Go tests in `internal/blog/takedown_test.go`: take down hides the post from `PublicList` (page, `q`, `limit`), `PublicPost`, the link-preview `PostMeta` and its cover asset in the same call; the 404 body and headers equal those of an unknown slug; restore returns it with the same slug, publish date and content; reason required (empty/whitespace refused), "1–500 characters, trimmed, no control characters"; double take-down and double restore are refused (`ALREADY_TAKEN_DOWN`, `NOT_TAKEN_DOWN`) and change nothing; the author's other posts and account state are unchanged (FR-008); suspending then reinstating the author never revives a taken-down post
- [x] T014 [P] [US1] Go tests in `internal/blog/takedown_test.go`: publish and update-published are refused with `TAKEN_DOWN` while taken down (409, reason returned), editing the draft is allowed; author unpublishes while taken down then admin restores → stays unpublished with the note; `GET /api/blog/notices` returns unseen takedowns for the author only (max 5, newest first, no admin identity), `POST /api/blog/notices/{postId}/seen` marks it, another author's post → 404, a new takedown after restore shows a new notice
- [x] T015 [P] [US1] Go tests in `internal/blog/takedown_test.go` (the admin routes need the blog mounted, so they live beside the other blog tests): the four routes refuse everyone but an active admin (anonymous, pending, suspended, ordinary user; changes nothing, no audit row), same-origin rule on the POSTs, one `audit_log` row per take-down/restore with actor, post, reason, request id; list filters (`visible`, `hidden_account`, `taken_down`), keyset paging, `q`; drafts, unpublished and unknown ids → one identical 404; posts of a suspended author are listed as `hidden_account` and their published content can be opened; an admin may take down and restore their own post (audited like any other); and `TestAdminBlogPostRoutesReturnOnlyPublishedFields` (draft, unpublished version, post check and reader data planted with recognisable text; none appears in any admin response, before and after the author's suspension)
- [x] T016 [P] [US1] vitest in `web/src/Admin.test.tsx` and `web/src/blog/Takedown.test.tsx`: Posts list (states, filter, search, empty, error+retry, Show more), post view, Take down form (reason required, counter, busy, error), Restore, the author's Taken down row with Publish disabled, the banner (appears once, marks seen, none when nothing unseen), and readable labels for the new audit actions in the Audit table

### Implementation for User Story 1

- [x] T017 [US1] `internal/blog/public.go`: add `AND p.takedown_at = ''` to `publicWhere`; make the public asset/cover route apply the same filter; confirm `internal/server/blogpages.go` `PostMeta` goes through `publicWhere` (no code change expected, covered by T013)
- [x] T018 [US1] `internal/blog/takedown.go`: `TakeDown`, `Restore` (single conditional UPDATE each, audit row in the same transaction), `AdminList`, `AdminGet` (published version only, ignores the account clause), `UnseenNotices`, `MarkSeen`; `internal/blog/posts.go`: `ErrTakenDown` guard on publish/update-published; author list item gains `takedown?:{at,reason}`
- [x] T019 [US1] `internal/blog/admin_handlers.go` (handlers) + registration in `internal/auth/admin.go` (`adminBlog`): `GET /api/admin/blog-posts`, `GET /api/admin/blog-posts/{postId}`, `POST …/take-down`, `POST …/restore` per `contracts/admin-api.md` (allowlist response structs, 20 per page, errors in the existing shape); author routes `GET /api/blog/notices` and `POST /api/blog/notices/{postId}/seen` (Active guard) per `contracts/author-api.md`
- [x] T020 [P] [US1] New `web/src/adminPostsApi.ts` + `web/src/blog/blogApi.ts`: typed calls and parsers (unknown states refused, like `parseAccount`)
- [x] T021 [US1] New `web/src/AdminPosts.tsx` (nav item added in `web/src/Admin.tsx`): **Posts** screen and post view with Take down / Restore confirm forms and the "copies elsewhere" line, as designed; readable labels for `post.take_down`, `post.restore`, `model_defaults.set` in the Audit table (`web/src/Admin.tsx`); `web/src/styles.css` for new rules only
- [x] T022 [US1] `web/src/blog/` author posts page: Taken down row with reason, Publish disabled with the reason; `web/src/App.tsx`/shell: one-time banner (`role="status"`), asks `GET /api/blog/notices` once for accounts with writing, marks seen on dismiss/view
- [x] T023 [US1] Motion for the new web pieces using the motion skills (restrained: confirm form and banner enter 180–220 ms, `--ease-out`, honours `prefers-reduced-motion`; no animation on frequent list actions)
- [x] T024 [US1] Rebuild `internal/webui/dist`; add browser views `admin-posts` and extend `blog-editor` in `scripts/browser-check.mjs` (mock backend: list, take down, restore, banner) at 320/390/820/1280 light and dark; add CI step in `.github/workflows/ci.yml`
- [x] T025 [US1] Make T011, T013–T016 green; `go test ./...`, `npm test`, `ONLY=admin-posts` and `ONLY=blog-editor` browser views, `TestBundleIsNotStale`

**Checkpoint**: US1 works alone and is demonstrable (MVP).

---

## Phase 4: User Story 2 - Turn models on and off for everyone (Priority: P1)

**Goal**: an admin screen lists every model with an on/off switch; off means nobody can choose or use it, for any purpose; a purpose's default cannot be switched off.

**Independent Test**: switch a model off; it leaves every picker, a chat on it refuses to send, titles, summaries, media and post checks do not use it; switching off a default is refused naming purposes; switching on restores it.

### Tests for User Story 2 (write first)

- [ ] T026 [P] [US2] Go tests in `internal/auth/admin_models_test.go`: `GET /api/admin/models` shape (catalogue ok/unavailable, `on`, `purposes`, `defaultFor`, usage totals per model, **no per-account field**); only an active admin may call it; `PUT /api/admin/model-policy` returns 409 `MODEL_IS_DEFAULT` with the purposes when a blocked id is any purpose's default (including image and video), changes nothing and writes no audit row; `PUT /api/admin/media-models` refuses an off model; catalogue unavailable keeps switches and says so
- [ ] T027 [P] [US2] Go tests for enforcement of a switched-off model: chat send → `MODEL_UNAVAILABLE` and nothing substituted (`internal/chat/`), the picker hides it, the post check refuses it (`internal/blog/check_test.go`), titles and summaries make no call to it and use their existing no-model fallback (`internal/chat/summary_test.go`, `title` test); a run already in progress finishes
- [ ] T028 [P] [US2] vitest in `web/src/Admin.test.tsx`: Models list (switch, saving, saved, error), usage totals, refused switch-off message, catalogue-unavailable state, keyboard and labels

### Implementation for User Story 2

- [ ] T029 [US2] `internal/auth/admin_models.go`: `GET /api/admin/models` (catalogue via `kenari.Catalogue`, block list, defaults, usage from T010); default guard in `adminSetModelPolicy` (409 `MODEL_IS_DEFAULT` `{purposes}`) and in `adminSetMediaModels`; register routes in `internal/auth/admin.go`; update `scanned()` and `TestEveryAdminGetRouteIsScannedForPostContent` (T011)
- [ ] T030 [US2] `internal/chat/title.go` and `internal/chat/summary.go`: skip the model call when the configured helper model is blocked (existing fallback applies); keep `internal/chat/service.go` and `internal/blog/check.go` block enforcement as is (covered by T027)
- [ ] T031 [P] [US2] New `web/src/adminModelsApi.ts`: `getModels`, `setModelPolicy` with typed `MODEL_IS_DEFAULT` error
- [ ] T032 [US2] New `web/src/AdminModels.tsx` (nav item added in `web/src/Admin.tsx`): **Models** screen switches and usage as designed; `web/src/styles.css` new rules only; motion per T023's rules
- [ ] T033 [US2] Rebuild `internal/webui/dist`; browser view `admin-models` in `scripts/browser-check.mjs` at 320/390/820/1280 light and dark; CI step
- [ ] T034 [US2] Update `docs/runbooks/models.md` (the "Admin UI for the block list: Not built" row becomes built; document the default guard); make T026–T028 green with all suites

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 3 - Choose the default model per purpose (Priority: P2)

**Goal**: the admin picks the default for chat, summaries and titles, blog assistant (and keeps images and videos); each purpose offers only fitting, switched-on models; users' own choices win.

**Independent Test**: set chat and blog assistant defaults; a new chat, an existing chat with no saved model (at its next message) and a post check use them; a chat with its own model keeps it; a title and a shared-picture summary use the summary default; an unusable default falls back with a visible warning.

### Tests for User Story 3 (write first)

- [ ] T035 [P] [US3] Go tests in `internal/auth/admin_models_test.go`: `PUT /api/admin/model-defaults` accepts `{chat?, summary?, blog_assistant?}` ("" = service default, omitted = unchanged), rejects a model not offered for that purpose, off, or invalid ("1–200 chars, no control characters"), audits each change; the read shows `current`, `effective` and `warning` (`not_listed|sunset|off|provider_unavailable`); per-purpose offers only fitting, on models
- [ ] T036 [P] [US3] Go tests in `internal/chat/`: a new chat with no choice sends the admin chat default; an existing chat with no saved model follows a changed default at its next message; a chat with its own model keeps it; an unusable default falls back to the profile default and the Models read shows the warning (no user-facing failure); title and summary use the summary default, else `TITLE_MODEL`; `internal/blog/check_test.go`: the post check runs the blog-assistant default through the same block/validation path; image and video defaults keep today's behaviour including re-verifying every ready account
- [ ] T037 [P] [US3] vitest in `web/src/Admin.test.tsx`: five pickers offer only fitting, on models plus "Service default", save, warning text, Images/Videos "re-checking accounts" message unchanged

### Implementation for User Story 3

- [ ] T038 [US3] `internal/auth/admin_models.go`: `PUT /api/admin/model-defaults`, validation against the catalogue per purpose (chat endpoint, not sunset, not blocked), computed `effective`/`warning`; reuse `store.SetModelDefault` (T010)
- [ ] T039 [US3] `internal/chat/service.go` / `internal/chat/modelprefs.go`: when the chat has no saved model, resolve the admin chat default through the picker's existing validation and send it explicitly (provider from the caller's own usable Hermes profile); on failure use the profile default (`effectiveDefault`), never fail the send
- [ ] T040 [US3] `internal/chat/title.go` + `cmd/antares/main.go`: `TitleModel.Model` becomes a per-call lookup (admin summary default if valid, else `cfg.TitleModel`); `internal/blog/check.go`: the blog-assistant default is resolved where the block-list resolution already is
- [ ] T041 [P] [US3] `web/src/adminModelsApi.ts` + `web/src/AdminModels.tsx`: purpose defaults block and warnings as designed; fold Images/Videos into the same block while keeping their save and re-verify behaviour; browser view `admin-models` extended; rebuild `internal/webui/dist`
- [ ] T042 [US3] Make T035–T037 green with all suites; update `docs/runbooks/models.md` and `docs/runbooks/media-models.md` for the five purposes, noting that media generation runs in a Hermes plugin so "off" is enforced for media by the default guard, not at generation time (FR-010)

**Checkpoint**: all three stories independent and complete.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T043 [P] Documentation: `docs/PRD.md` (new "Admin control" paragraph), `specs/004-admin-full-control/validation.md` (results and what was not driven: SC-002 and SC-003 were not timed with a person; media "off" is guard-enforced), tick every task above
- [ ] T044 [P] Accessibility pass on the new screens (switch names, focus return after confirm, banner announcement, 44 px targets, reduced motion) and the full browser suite (`mobile-layout`, `blog-public`, `blog-editor`, `blog-discovery`, `admin-*`) at 320/390/820/1280, light and dark
- [ ] T045 Run `quickstart.md` end to end against the dev stack; confirm SC-001…SC-007 (including admin-blindness scan)
- [ ] T046 PR(s), Hermes Review passes, merge commit; backup, deploy, `/api/version` and `/readyz`; production smoke with a fixture author and post (take down, public 404, restore, models screen reads), then clean up fixtures; close the issue; real accounts untouched

---

## Dependencies & Execution Order

- Phase 1 (design) blocks everything; the owner approves T007/T008.
- Phase 2 blocks all stories. T010, T011, T012 are parallel after T009.
- US1 and US2 depend only on Phase 2 and can run in parallel; US2's T029 and US1's T019 both edit `internal/auth/admin.go` (sequence those two). The Posts and Models screens live in separate files (`AdminPosts.tsx`, `AdminModels.tsx`, own api files); only the nav and Audit labels touch `web/src/Admin.tsx` (US1 first). T040 edits `internal/chat/title.go` after T030.
- US3 depends on US2's `GET /api/admin/models` (T029) and the Models screen (T032).
- Polish last.

## Parallel examples

- US1 tests: T013, T014, T015, T016 together.
- US2 tests: T026, T027, T028 together; US3 tests: T035, T036, T037 together.
- Web client types (T020, T031) in parallel with server work in the same story.

## Implementation strategy

- **MVP**: Phases 1–3 (design, foundation, takedown). Deploy and verify, then US2, then US3.
- Each story is deployed behind no flag; its screens are admin-only, so shipping a story early is safe.
- Per AGENTS.md, one commit per review round; the PR for each story states what changed, how it was verified and what is open.
