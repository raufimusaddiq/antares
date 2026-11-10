---

description: "Task list for Blog Writing Feedback and the AI Editor"
---

# Tasks: Blog Writing Feedback and the AI Editor

**Input**: Design documents from `specs/006-ai-editor-blog/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/{check-envelope-v2,api,ui-contract}.md, quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour.

**Organization**: Design first (Phase 1, in figcraft, owner-approved before any code), a small behaviour-preserving foundation, then one phase per user story. The three stories ship as three PRs in this order: US1, US2, US3 (US1 and US2 are independent of US3; US3 reuses the progress component of US2).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 (toolbar truth), US2 (check progress), US3 (the AI editor) from spec.md
- Paths are from the repository root. Web code is in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design (`docs/design/antares.figcraft`, page "AI editor"): match its variables and styles in `web/src/styles.css`; where code and design differ, fix the code or the design in the same PR.
- Rebuild `internal/webui/dist` with any web change (`TestBundleIsNotStale`). The one new API path (`/api/blog/posts/...`) is already under the allowlisted prefix in `internal/webui/webui_test.go`; check it.
- The draft is written only by the editor and its autosave. The server never changes the draft text. Admin routes never return a draft, a suggestion or a check (constitution I): `post_edits` goes into the admin-blindness scan and `scripts/smoke_fixtures.py`.
- A suggestion never states a figure, a quote or a link that is not in the draft (validated on the server, FR-013).
- No button may draw over another: new screens get a no-overlap check, 44 px targets on a phone, named controls (as in `ONLY=admin-a11y`). Motion: 180-220 ms, `--ease-out`, none under `prefers-reduced-motion`.
- Measuring scripts for the production quality run are not committed (AGENTS.md); results go in `validation.md`.
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push; merge commit only. Deploy: backup first, smoke with fixtures, clean up, real accounts untouched.

---

## Phase 1: Design first (figcraft, owner approval before any code)

**Purpose**: the owner's standing rule. No code task starts before T004.

- [x] T001 Read the existing editor, publish and review frames (pages "Blog CMS", "Editor", "Publish / preview") and the "Admin control" and "Writing rules" pages with figcraft `inspect_document` and `screenshot`; create page **AI editor** in `docs/design/antares.figcraft`
- [x] T002 [US1] Draw the editor toolbar at 1280 and 390: each button on, off and mixed (bold partly applied), focus, pressed; the mixed look must not rely on colour alone (half-filled outline); phone wraps without overlap, 44 px
- [x] T003 [US2] [US3] Draw the "Check with the editor" button in the editor top bar and the panel: progress (steps Reading your post, Looking things up with a count, Writing suggestions; done, current, pending; elapsed time; "Last sign of life 4 s ago"; Cancel), stalled, timed out, failed, cancelled, limit reached; suggestion cards (`replace` with passage, proposed text, reason, Accept, Reject; `insert` with the "Written by the assistant" chip; accepted with Undo; rejected; no longer applicable; none; too little to edit); the Publish review screen with progress and suggestions; phone as a bottom sheet at 390 and 320; all per `contracts/ui-contract.md`
- [x] T004 Screenshot every frame in light **and dark** (dark = duplicates with `variable.setMode`; clear the default white fills of containers by name first), check spacing, type, 44 px targets, no overlap, 320 px width; send the PNGs to the owner and wait for approval; save with `save_file` and an explicit path
- [x] T005 Create the GitHub issue for this spec (design, three PRs) first; then update `specs/006-ai-editor-blog/contracts/ui-contract.md` and `docs/design/README.md` (new "AI editor" frames, the new components: step list, suggestion card, mixed toggle) to match the approved design; PR (refs the issue), Hermes Review, merge

**Checkpoint**: owner approved the design (T004, T005 done); Phase 2 may start.

---

## Phase 2: Foundation (behaviour-preserving, shared by US2 and US3)

- [ ] T006 (moved to the US3 PR, where the editor panel needs it; US2 shows progress in `PublishFlow` directly) Extract the check lifecycle from `web/src/blog/PublishFlow.tsx` into `web/src/blog/useCheckRun.ts` (`start`, poll with one request at a time, `cancel`, `apply`, the error words), with `PublishFlow` using it, keeping the 2,000 ms polling and no other change in behaviour; every existing test in `web/src/blog/PublishDialog.test.tsx` and `PreviewScreen.test.tsx` passes unchanged; add `web/src/blog/useCheckRun.test.tsx` for the polling rules (one request at a time, a refusal halts polling, abort on unmount)

**Checkpoint**: nothing a user can see has changed.

---

## Phase 3: User Story 1 - The formatting buttons always show the truth (Priority: P1) 🎯 MVP

**Goal**: the toolbar always matches the text at the cursor or selection, with a mixed state, and is usable on a phone.

**Independent Test**: type a sentence, make one word bold and a line a heading, move the cursor with mouse and arrow keys, select a partly bold range: the buttons follow at once; 50 moves in a real browser all match.

### Tests for User Story 1 (write first, they must fail)

- [x] T007 [P] [US1] vitest `web/src/blog/formatState.test.ts`: `coverage()` returns `on`, `off`, `mixed` for marks (bold, italic, code, link) and block types (heading level 2 and 3, bullet list, ordered list, blockquote, code block) over a collapsed cursor, a range inside one mark, a range partly inside it, a range over two blocks of which one matches; stored marks after pressing Bold with an empty selection show `on`
- [x] T008 [P] [US1] vitest `web/src/blog/Toolbar.test.tsx`: with a real editor in jsdom, moving the selection (`setTextSelection`) and running a command change every button's `aria-pressed` (`true`, `false`, `mixed`) with no other action; every button has an accessible name
- [x] T009 [US1] Browser view `ONLY=editor-toolbar` in `scripts/browser-check.mjs`: type text with a bold word, an italic word, a heading, a list item, a quote and a code line; make 50 cursor moves and selections (arrow keys, clicks, shift-selection, a partly bold range, a range over a heading and a paragraph); after each, every button's `aria-pressed` equals the truth read from the DOM at the caret (`strong`, `em`, `h2`, `li`, `blockquote`, `pre`); at 320, 390, 820, 1280 px, light and dark; no button overlaps another; every target at least 44 px on a phone; add the CI step in `.github/workflows/ci.yml`

### Implementation for User Story 1

- [x] T010 [US1] `web/src/blog/formatState.ts`: `coverage(editor, test)` walking the text blocks and text nodes in the selection (`all` -> on, `none` -> off, `some` -> mixed; an empty selection uses the stored marks and the cursor's parent) and `useFormatState(editor)` built on `useEditorState` returning `{bold, italic, heading2, heading3, bullets, numbers, quote, code, link}` each `on|off|mixed`, re-rendering only when a value changes
- [x] T011 [US1] `web/src/blog/Editor.tsx`: draw the toolbar from `useFormatState`; each button gets `aria-pressed="true|false|mixed"`; `web/src/styles.css`: the mixed look from the design (half-filled outline) for `.blog-toolbar button[aria-pressed="mixed"]`, 44 px phone targets and wrapping as designed
- [x] T012 [US1] Rebuild `internal/webui/dist`; make T007-T009 green with all suites (`go test ./...`, `npx vitest run`, the browser views)

**Checkpoint**: US1 works alone; deployable (PR 1).

---

## Phase 4: User Story 2 - The author always knows what the assistant is doing (Priority: P1)

**Goal**: a running check shows its real step, elapsed time and sign of life; stalled, timed-out, failed and cancelled checks are plain; leaving and returning shows the current step.

**Independent Test**: start a check: named steps move on, the clock runs, the sign of life updates; close the tab and return: the real step; a stalled run ends as timed out with Try again.

### Tests for User Story 2 (write first, they must fail)

- [x] T013 [P] [US2] Go tests in `internal/blog/progress_test.go` with the fake Hermes event script (`internal/blog/fakehermes_test.go`): a new check reads `stage` `reading` with `stageSince` = `startedAt` at once; after a tool starts it reads `searching` with `lookups` 1, after a second `lookups` 2; after the first message delta `writing`; a tool start after that returns to `searching`; `lastEventAt` moves with each event but is written at most once per 2 s apart from a stage change (a burst of 50 deltas writes at most 1-2 times); the read carries the server `now`; a script that never finishes ends as `timed_out` after `checkTimeout` and keeps its `progress`; a cancelled check keeps its elapsed time; another author's check read is a 404; the migration upgrades an existing database with every existing check at stage `reading`
- [x] T014 [P] [US2] vitest `web/src/blog/CheckProgress.test.tsx`: the step list marks done, current and pending from `progress.stage` and shows "N lookups"; the clock ticks from the server `now` offset (fake timers) and is `aria-hidden`; the live region text changes only when the step changes and is `aria-live="polite"`; a sign of life older than 45 s shows "Still working. Nothing new for N s."; timed out, failed, cancelled and limit-reached screens say which, that nothing was published, and offer Try again / Back to editing / Publish original; no percentage is ever rendered
- [x] T015 [US2] Browser: extend `ONLY=blog-editor` (view `checking`) in `scripts/browser-check.mjs` with the steps, the clock, the stalled variant and a reload that shows the real step from the mock; no overlap, 44 px targets, 320/390/820/1280, light and dark (mock `RUNNING_CHECK` gains `progress`)

### Implementation for User Story 2

- [x] T016 [US2] Change `POLL_MS` to 1,500 and update the polling tests (SC-002); migration `internal/store/migrations/0030_check_progress.sql` (the plan's `0030` is split in two so each story ships its own migration): `ALTER TABLE post_checks ADD COLUMN stage TEXT NOT NULL DEFAULT 'reading' CHECK (stage IN ('reading','searching','writing'))`, `stage_at TEXT NOT NULL DEFAULT ''`, `last_event_at TEXT NOT NULL DEFAULT ''`, `lookups INTEGER NOT NULL DEFAULT 0`; migration test that an existing database upgrades
- [x] T017 [US2] `internal/blog/check.go` `driveRun` and `StartCheck`: set `stage_at` and `last_event_at` to `started_at` on insert; in the event callback map `KindThinking` (stay), `KindToolStarted` (-> `searching`, `lookups + 1`), `KindDelta` (-> `writing`), write a stage change at once and `last_event_at` at most once per 2 s (all with `AND author_id = ? AND state = 'running'`); `CheckView` gains `Progress` (`stage`, `stageSince`, `lastEventAt`, `lookups`, `startedAt`, `now`) in `readCheck`
- [x] T018 [P] [US2] `web/src/blog/types.ts` and `web/src/blog/blogApi.ts` (`parseCheck` reads `progress`, refuses a stage it does not know by treating it as `reading`); new `web/src/blog/CheckProgress.tsx` from the design (steps, clock, sign of life, stopped screens, polite live region) and the CSS in `web/src/styles.css`
- [x] T019 [US2] `web/src/blog/PublishFlow.tsx`: the "Checking your writing" step uses `CheckProgress`; reloading mid-check shows the real step (FR-007); rebuild `internal/webui/dist`; make T013-T015 green with all suites

**Checkpoint**: US1 and US2 both work independently (PR 2).

---

## Phase 5: User Story 3 - The assistant works as the author's editor and fills out a bare framework (Priority: P1)

**Goal**: the check proposes changes to the author's text and prose for a bare framework, in the draft's language; the author accepts or rejects each; nothing changes before; editor and Publish show the same suggestions; a check stays usable after the draft changes.

**Independent Test**: a rough draft and a five-heading framework, in Indonesian and English: suggestions with reasons beside the draft; accept one (one undoable change), reject one; the rest stay usable after typing; publish without accepting publishes the original.

### Tests for User Story 3 (write first, they must fail)

- [ ] T020 [P] [US3] Go tests in `internal/blog/envelope_test.go`: a version 1 reply (no `edits`) is accepted as before; version 2 accepts a good `replace` and a good `insert`; each is dropped on its own with one notice for: unknown `kind`, `part` outside the draft, `replace` with an empty `original`, an `original` over 1,000 characters, an `original` with a newline, an `original` not a substring of one line of that part (the `## ` / `- ` markers removed), `proposed` empty, over 4,000 characters (8,000 for `insert`), equal to `original`, an `insert` with more than 12 paragraphs, a `reason` over 300 characters, control characters, HTML tags, Markdown link syntax, a URL or an e-mail address, a number in `proposed` that is not in the draft (including the title), a quoted span of more than 6 words not in the draft; more than `4 x parts + 8` edits drops the rest with one notice; the order is kept
- [ ] T021 [P] [US3] Go tests in `internal/blog/anchors_test.go` driven by `internal/blog/testdata/anchors.json` (documents and expected results: a part found at its index, found after an insertion above, found after a deletion above, the same text twice -> nearest to the old index, gone -> not found, a list's first item, a heading, whitespace collapsed, truncated at 120 characters, an image-only part: empty anchor, never gets an edit, a block is placed by its stored index)
- [ ] T022 [P] [US3] Go tests in `internal/blog/edits_test.go` and `check_test.go`: a finished check stores its edits and returns them with their `anchor`, decision `open`; `PUT /api/blog/posts/{id}/edits/{editId}` accepts only `{"decision":"accepted"|"rejected"}` (unknown field, other value: 400), records the decision and never changes the draft (the post's document and version are byte-identical before and after), another author's edit is 404, a missing or malformed id is 404, an older check's edit is 409 `SUPERSEDED`, a check that is not `ready` is refused; **a save no longer turns a ready or running check `out_of_date`** (rewrites `TestEditingTheDraftMakesTheCheckOutOfDate` and `TestEditingWhileItRunsDiscardsTheAnswer` to the owner's rule Q2, recorded in the commit message); a block's `afterNode` is relocated against the saved draft after an insertion above it, and `-1` when its place is gone; `publish` with `enriched` after the draft changed publishes the accepted blocks at their relocated places, skips one whose place is gone and returns `skippedBlocks`; `publish` with `original` publishes exactly the author's text; checks started from the editor and from Publish count against the same limits (2 running, 30 per 24 hours) and the refusal is `LIMIT_REACHED` (422) with `retryAt` (the oldest check in the rolling 24 hours plus 24 h) or `running: true`; running a check leaves the draft's document and version byte-identical; a finished check's post status stays `ready` after an edit; the prompt's first line is `blog-check v3` and states the language rule; `post_checks.prompt_version` records it
- [ ] T023 [P] [US3] Go guard tests: add `post_edits` text planted in a draft to the admin-blindness scan (`internal/auth/admin_blog_test.go`) and prove no admin response contains it; `scripts/smoke_fixtures.py` classifies `post_edits` as owned (`author_id IN fx`, deleted before `post_checks`) and its self-tests pass
- [ ] T024 [P] [US3] vitest `web/src/blog/anchors.test.ts` driven by the same `internal/blog/testdata/anchors.json` through the TypeScript `locatePart` (read with `fs` in the test), and `web/src/blog/draftBridge.test.ts`: `applyEdit` replaces only the span inside the matching text block (one transaction, undone by one `undo`), inserts paragraphs after the right part, returns `not_applicable` when the passage changed (by typing or by another accepted edit), never touches other text, keeps the surrounding marks
- [ ] T025 [P] [US3] vitest `web/src/blog/Suggestions.test.tsx` and `AssistPanel.test.tsx`: cards show the passage, the proposed text and the reason; `insert` shows "Written by the assistant"; Accept applies through the bridge then records the decision, shows accepted with Undo; Reject records and changes nothing; a card whose passage changed shows "no longer applicable" with no Accept; focus moves to the next card after a decision; "Nothing to suggest" and the too-little-to-edit note; the panel starts a check, shows `CheckProgress`, and lists suggestions; limit reached says when to try again (from `retryAt`) and that Publish original still works; starting a check flushes autosave first and shows the "not saved yet" sentence when it cannot; the panel does not poll while the Publish screen is open; the Publish review screen lists the same suggestions
- [ ] T026 [US3] Browser: extend `ONLY=blog-editor` in `scripts/browser-check.mjs`: open the panel, run a check on the mock, accept one and reject one, undo the accepted one, type elsewhere and see the rest stay usable, no overlap, 44 px, 320/390/820/1280, light and dark, panel below the draft on a phone without covering the toolbar or Publish; the draft text is unchanged until the accept click

### Implementation for User Story 3

- [ ] T027 [US3] Migration `internal/store/migrations/0031_post_edits.sql` per `data-model.md`: table `post_edits` (`id TEXT PRIMARY KEY`, `check_id TEXT NOT NULL REFERENCES post_checks(id) ON DELETE CASCADE`, `post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE`, `author_id TEXT NOT NULL REFERENCES users(id)`, `position INTEGER NOT NULL`, `kind TEXT NOT NULL CHECK (kind IN ('replace','insert'))`, `part INTEGER NOT NULL`, `anchor TEXT NOT NULL`, `original TEXT NOT NULL DEFAULT ''`, `proposed TEXT NOT NULL`, `reason TEXT NOT NULL`, `decision TEXT NOT NULL DEFAULT 'open' CHECK (decision IN ('open','accepted','rejected'))`, `decided_at TEXT NOT NULL DEFAULT ''`), indexes `(check_id, position)` and `(author_id, post_id)`, and `ALTER TABLE post_blocks ADD COLUMN anchor_text TEXT NOT NULL DEFAULT ''`; migration test
- [ ] T028 [US3] `internal/blog/anchors.go`: `anchorOf(part)` (text of the first text block, whitespace collapsed, at most 120 characters) and `relocate(doc, anchor, hint)` (the top-level part whose anchor equals it, nearest to the old index first, -1 when none); `internal/blog/testdata/anchors.json` with the cases of T021
- [ ] T029 [US3] `internal/blog/envelope.go`: `ParseEnvelopeParts(raw, parts, now)` (the existing `ParseEnvelope(raw, topNodes, now)` stays as a wrapper that drops `edits`) implementing T020's rules and the `Result.Edits` list; `checkInput` returns the parts' text lines
- [ ] T030 [US3] `internal/blog/blog_prompt.txt` -> `blog-check v3` per `contracts/check-envelope-v2.md` (the edits grammar, the language rule, about one suggestion per section most important first, a bare framework is expanded with `insert` from the author's own points, no new facts, too little to edit says so in one note); replace "You must NEVER rewrite, correct, shorten or extend the author's text" with "You never change the draft yourself; you only propose"; amend `specs/001-blog-cms-openui-publish/contracts/check-envelope.md` to say so
- [ ] T031 [US3] `internal/blog/check.go` (`ErrTooMany` returns `retryAt` or `running`; `handlers.go` writes them): store edits and block `anchor_text` in `finishCheck`; stop pinning in `StartCheck`; stop calling `outOfDate` on a save and no longer reset the post's status to its previous one (`internal/blog/posts.go`); `DecideBlock` no longer needs `version == latest`; `readCheck` returns `edits` and the relocated block `afterNode`; `PublishEnriched` relocates accepted blocks against the current draft, skips the ones whose place is gone and returns `skippedBlocks`
- [ ] T032 [US3] New `internal/blog/edits.go` (`DecideEdit`, `EditView`, errors) and the route `PUT /api/blog/posts/{id}/edits/{editId}` in `internal/blog/handlers.go` (Active guard, same origin, strict body, owner-filtered query, latest ready check only); `scripts/smoke_fixtures.py` per T023
- [ ] T033 [P] [US3] `web/src/blog/anchors.ts` (`locatePart`, `anchorOf` on a ProseMirror document) and `web/src/blog/draftBridge.ts` (`applyEdit`, `undoLast`, a registered ref so `PublishFlow` can reach the live editor behind the review screen); `web/src/blog/BlogApp.tsx` holds the bridge and passes it to `Editor` and `PublishFlow`
- [ ] T034 [P] [US3] `web/src/blog/types.ts`, `blogApi.ts` (`parseCheck` reads `edits`, `decideEdit`), new `web/src/blog/Suggestions.tsx`, `web/src/blog/AssistPanel.tsx` and the CSS in `web/src/styles.css` from the design; `web/src/blog/Editor.tsx` gets the "Check with the editor" button and the panel (a bottom sheet on a phone); starting flushes autosave first (same sentence as Publish) and the panel pauses polling while the Publish screen is open; `web/src/blog/PreviewScreen.tsx` lists the suggestions and the skipped-block sentence
- [ ] T035 [US3] Rebuild `internal/webui/dist`; make T020-T026 green with all suites

**Checkpoint**: all three stories independent and complete (PR 3).

---

## Phase 6: Polish & Cross-Cutting

- [ ] T036 [P] Update `docs/PRD.md` (the blog check section: progress, suggestions, anchors, shared limits), `docs/runbooks/blog-check.md` (stages, how a stalled check ends, the edits rules and the figure rule, the shared limits), every place that states the old rule: `specs/001-blog-cms-openui-publish/spec.md` (the edit-while-running edge case and the Check entity line), `contracts/check-envelope.md` (the `out_of_date` row and the never-rewrite rule), `contracts/design-pages.md:44`, `HANDOVER.md`, `docs/runbooks/blog-check.md` (lines about going out of date), `docs/PRD.md` (the paragraph saying any edit makes the check out of date), and write `specs/006-ai-editor-blog/validation.md` (what was verified and how, what was not driven)
- [ ] T037 Production quality run with synthetic accounts (throwaway script, not committed): a finished rough draft and a bare framework of five headings, each in Indonesian and in English, plus a draft with a made-up statistic in it (5 rough drafts and 5 frameworks in all, mixed Indonesian and English); record suggestions per draft, figures not in the draft (must be 0 after validation), language of notes and proposed text, the progress steps seen and their timing, the draft unchanged until accepted, publish without accepting publishes the original; record SC-003 (steps seen in the browser, not a human trial) and SC-004 to SC-007 in `validation.md` honestly (a model result is evidence, not a guarantee); cleanup
- [ ] T038 Run `quickstart.md` end to end; PRs, Hermes Review passes, merge commit; backup, deploy, `/api/version` and `/readyz`; production smoke with fixtures (toolbar is browser-checked in CI; progress steps and a suggestion accepted through the API/browser; the decision route; the check stays usable after an edit); cleanup; close the issue; real accounts untouched

---

## Dependencies & Execution Order

- Phase 1 (design) blocks everything; the owner approves T004/T005.
- Phase 2 (T006) blocks US2 and US3 (both use `useCheckRun`); US1 does not need it.
- US1 -> PR 1, US2 -> PR 2, US3 -> PR 3. US3 reuses `CheckProgress` (T018) and the migration number follows US2's (0031 after 0030).
- Within a story: tests first (they fail), then migration, server, web, bundle.
- `web/src/blog/Editor.tsx` is touched by T011 and T034 (sequence them); `scripts/browser-check.mjs` by T009, T015, T026 (sequence them); `internal/blog/check.go` by T017 and T031.
- Polish last; T037 needs US3 deployed.

## Parallel examples

- US1 tests: T007, T008 together; US2 tests: T013, T014; US3 tests: T020, T021, T022, T023, T024, T025 (different files).
- US3 server (T028-T032) in order; its web part (T033, T034) after the API shape is agreed.

## Implementation strategy

- **MVP**: Phases 1-3 (design, foundation, toolbar). Deploy and verify, then US2, then US3. Each story is deployable alone; US3 changes a rule (a check no longer goes out of date on an edit), so its PR says so and shows the rewritten tests.
- Per AGENTS.md, one commit per review round; each PR states what changed, how it was verified and what is open.
