---

description: "Task list for Built-in Anti-Slop Writing Rules for Every Assistant"
---

# Tasks: Built-in Anti-Slop Writing Rules for Every Assistant

**Input**: Design documents from `specs/005-antislop-writing/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/{rules-text,admin-api,ui-contract}.md, quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour.

**Organization**: Design first (Phase 1, in figcraft, owner-approved before any code), then the shared foundation, then one phase per user story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 (writing in chat), US2 (post assistant), US3 (administrator) from spec.md
- Paths are from the repository root. Web code is in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design (`docs/design/antares.figcraft`, page "Writing rules"): match its variables and styles in `web/src/styles.css`. Where code and design disagree, say so in the PR and fix the wrong side.
- Rebuild `internal/webui/dist` with any web change (`TestBundleIsNotStale`). Browser code stays inside the allowlist in `internal/webui/webui_test.go` (add the new admin path).
- The rules text is identical for every account, sent by the backend, never by the browser; no user content in the rules, the counts or the audit. No new unauthenticated route; no Hermes data directory is written.
- No button may draw over another: new screens get a no-overlap check, 44 px targets on a phone, named controls (as in `ONLY=admin-a11y`).
- Measuring scripts for the quality run are not committed (AGENTS.md); results go in `validation.md`.
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push; merge commit only. Deploy: backup first, smoke with fixtures, clean up, never touch real accounts.

---

## Phase 1: Design in figcraft (blocks all code)

**Purpose**: draw every screen and state on a new page **Writing rules** in `docs/design/antares.figcraft`, using the existing variables and the Admin frames and the spec 004 switch; the owner approves before Phase 2.

- [x] T001 Read the existing Admin → Capabilities frames, the Models switch (page "Admin control") and the post-check preview frames (page "Blog CMS", "Publish / preview") with figcraft `inspect_document` and `screenshot`; create page **Writing rules** in `docs/design/antares.figcraft`
- [x] T002 [US3] Draw the Capabilities card "Writing rules" at desktop 1280 and phone 390: on (switch, "writing-rules v1", source and MIT credit line, last 24 hours "N with, 0 without"), switched off, on with the warning "The rules were not applied to N runs since this feature shipped" (runs while switched off are not counted), saving, saved, error
- [x] T003 [US2] Draw the post check result with style notes at desktop and phone: one style note, several (with the existing note kinds), none, one dismissed; each row: kind chip "Style", where it is, a "Dismiss" button; Publish stays available
- [x] T004 Screenshot every frame in light **and dark** (dark = duplicates with `variable.setMode`, 10000 below), compare with neighbouring screens (spacing, type, 44 px targets, 320 px width), fix, and ask the owner to approve (`comment.list`); save with `save_file` and an explicit path
- [x] T005 Update `specs/005-antislop-writing/contracts/ui-contract.md` and `docs/design/README.md` (new "Writing rules" frames, any new component) to match the approved design

**Checkpoint**: owner approved the design (T004, T005 done); Phase 2 may start.

---

## Phase 2: Foundational (shared by all stories)

**Purpose**: the rules package, the setting and the schema every story uses. No story starts before this.

- [ ] T006 Add `internal/store/migrations/0029_writing_rules.sql`: table `writing_rules_setting` (`id INTEGER PRIMARY KEY CHECK (id = 1)`, `enabled INTEGER NOT NULL CHECK (enabled IN (0,1))`, `updated_by TEXT NOT NULL`, `updated_at TEXT NOT NULL`), and `ALTER TABLE runs ADD COLUMN writing_rules TEXT`, `ALTER TABLE post_checks ADD COLUMN writing_rules TEXT` (`v1` = attached, `off` = deliberately switched off, NULL = expected and not attached, or older than this migration); migration test that an existing database upgrades with all existing runs and checks NULL
- [ ] T007 [P] Add `internal/writing/rules.md` containing exactly the text in `contracts/rules-text.md` and `internal/writing/writing.go` (`//go:embed`, `Version = "v1"`, `Text()`, `Check()` that errors when the text is missing, does not start with `writing-rules v1`, lacks the licence credit or is over "at most 3,000 characters"); the text is read through an unexported variable that a test can replace (the seam for the "rules cannot be loaded" tests, FR-009); tests in `internal/writing/writing_test.go` pin the invariants (starts with `writing-rules v1`, contains `MIT` and `Miqdad Badjuber (antislop)`, mentions em dashes, invented figures, "answer a question", tone, "say so plainly", at most 3,000 characters) and print the measured size
- [ ] T008 [P] Add `internal/store/writingrules.go`: `WritingRulesEnabled(ctx, db)` (absent row = true; on a read error return true and the error), `SetWritingRules(ctx, db, actor, enabled, reqID, now)` (admin check as `SetModelDefaults`, one `audit_log` row `writing_rules.set` with detail `enabled=true|false`, same transaction), and `WritingRulesCounts(ctx, db, since)` (counts of runs and post_checks started since: `with` = `v1`, `without` = `off` or NULL, and `notApplied` = NULL only on rows started at or after the `applied_at` of migration 29 in `schema_migrations`; selects no user, chat or post column); tests in `internal/store/writingrules_test.go`
- [ ] T009 [P] Classify `writing_rules_setting` as shared admin config in `scripts/smoke_fixtures.py` (OWNED `"0"`, `SHARED_REFS` `updated_by IN {FX}`, the residue skip list), as `model_defaults` was; the Python self-tests pass
- [ ] T010 [P] Add `docs/runbooks/writing-rules.md`: what is built in (the copywriting rules of anti-slop), source and the upstream commit read, the MIT licence and credit, how to change the text (a release that bumps the version, never edited in place), the switch, how "not applied" shows (only runs expected to have the rules and since migration 0029; the counts cover chat and post-check runs, not titles and summaries), and why run instructions and not a Hermes skill (constitution, Hermes data directories)

**Checkpoint**: schema, package and setting exist and are tested; stories can proceed.

---

## Phase 3: User Story 1 - Writing the assistant does for you reads like a person wrote it (Priority: P1) 🎯 MVP

**Goal**: every account's chat runs carry the rules; writing is clean, answers are untouched; the person's own instructions win on tone; summaries use the same text.

**Independent Test**: an existing and a new synthetic account ask for an email draft and an explanation: the draft is clean, the explanation is unchanged in style, code and quotations are untouched; switched off (directly in the database for now), the next run has no rules.

### Tests for User Story 1 (write first, they must fail)

- [ ] T011 [P] [US1] Go tests in `internal/chat/writing_rules_test.go`: a run for an account created before the feature and one created after both carry the rules text in `instructions` on the wire (the fake Hermes records the body) and `runs.writing_rules = 'v1'`; with the setting off the instructions carry none and the column is `off`; the order is rules, OpenUI instruction, then the person's personality; the instruction text a browser sends cannot add or remove them (a request carrying `instructions` is still rejected as today); a read error of the setting still sends the rules; with the rules text unavailable (the T007 seam) chat still works, the instructions carry no rules and the column is NULL (FR-009, SC-006); a send body with an `instructions` field is a 400 and the run's instructions are unchanged; a switched-off run records `off`, not NULL
- [ ] T012 [P] [US1] Go tests in `internal/chat/summary_test.go` (extend): the shared-picture summary system text contains the shared rules text when on and not when off, and no longer carries the old `summaryStyle` paragraph; a switched-off summary model still makes no call (spec 004 behaviour unchanged)

### Implementation for User Story 1

- [ ] T013 [US1] `internal/chat/personality.go` `runInstructions`: take the rules text as a parameter and put it first, before the OpenUI instruction and the personality (research R4); `internal/chat/service.go` `runNew`: read `store.WritingRulesEnabled`, attach `writing.Text()` when on and `writing.Check()` passes, and record what happened in `runs.writing_rules` (`v1`, `off`, or NULL when on but `writing.Check()` failed) in an update right after the run is admitted
- [ ] T014 [US1] `internal/chat/summary.go`: remove the `summaryStyle` constant; `TitleModel` gains a `Rules func(ctx context.Context) string` hook set in `cmd/antares/main.go` (returns `writing.Text()` when the setting is on, else ""), appended to `summaryInstruction` for summaries only (titles unchanged)
- [ ] T015 [US1] Make T011 and T012 green; `go test ./...`

**Checkpoint**: US1 works alone: chat and summaries follow the rules (switch still by database); deployable.

---

## Phase 4: User Story 2 - The post assistant writes clean copy and flags slop (Priority: P1)

**Goal**: the post check carries the rules, adds style notes where the draft leans on filler or hype, never rewrites, and the author sees the notes (dismissible) before publishing.

**Independent Test**: run the check on a clean draft, a filler draft and a made-up-statistics draft: style notes only on the filler draft; no proposed block carries an unsourced number; the draft is unchanged; Publish stays available; the author can dismiss a note.

### Tests for User Story 2 (write first)

- [ ] T016 [P] [US2] Go tests in `internal/blog/envelope_test.go` and `internal/blog/check_test.go`: an envelope with a note of kind `style` is accepted and shown; a note of any other kind is still refused; at most the existing note count; the prompt's first line is `blog-check v2`, the check instructions contain the shared rules text and the `style` note instruction when on and neither rules text nor `style` instruction when off; `post_checks.writing_rules` is `v1`, `off` or (rules text unavailable, and the check still runs) NULL; a 120 KB draft (the existing long-draft fixture) still starts a check with the rules attached; a check with style notes can still be published (the publish flow ignores notes); the draft's versions are unchanged by a check
- [ ] T017 [P] [US2] vitest in `web/src/blog/StyleNotes.test.tsx` (the existing `web/src/blog/PreviewScreen.test.tsx` keeps covering the unchanged parts): a `style` note shows with the label "Style" and its place; "Dismiss this note" removes it and moves focus to the next note (or the heading when none is left); dismissed notes do not return when the screen re-renders; Publish is never disabled by notes; `parseCheck` accepts the new kind and still refuses unknown ones

### Implementation for User Story 2

- [ ] T018 [US2] `internal/blog/blog_prompt.txt`: bump the first line to `blog-check v2`; in the notes grammar add `"style"` to the allowed kinds and a sentence on when to use it (the author's draft leans on filler, hype, em dashes or invented specifics: say what kind and where, never rewrite); keep every other rule; `internal/blog/envelope.go`: accept `style`; `internal/blog/check.go`: when the setting is on, append `writing.Text()` plus the contract's one-paragraph check addendum to the `Instructions` of the run, record `post_checks.writing_rules` (`v1`, `off`, or NULL when on but the text was unavailable); `checkPromptVersion()` still reads the first line
- [ ] T019 [P] [US2] `web/src/blog/types.ts` (`CheckNote["kind"]` gains `"style"`), `web/src/blog/blogApi.ts` (`parseCheck` accepts it), `web/src/blog/PreviewScreen.tsx` (`NOTE_WORDS.style = "Style"`, a "Dismiss this note" button per note, dismissed set kept in component state, focus to the next note), CSS only for new rules in `web/src/styles.css`; motion per the Takedown rules (180-220 ms, `--ease-out`, reduced-motion safe) on dismissal only
- [ ] T020 [US2] Rebuild `internal/webui/dist`; extend `scripts/browser-check.mjs` view `blog-editor` (the preview with a style note: shown, dismissed, Publish still enabled, no overlap, 44 px targets) and its mock check result; make T016 and T017 green with all suites

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 3 - The administrator knows it is on, and can switch it off (Priority: P2)

**Goal**: Admin → Capabilities shows the state, version, credit and the last-24-hours counts, with the "not applied" warning; the switch is audited and takes effect on the next run.

**Independent Test**: as the administrator read the card, switch off, send a chat message and run a check (no rules, columns NULL, counts "without" grow), switch on (rules return, no warning); the audit log holds both changes; a non-admin is refused.

### Tests for User Story 3 (write first)

- [ ] T021 [P] [US3] Go tests in `internal/auth/admin_writing_test.go`: `GET`/`PUT /api/admin/writing-rules` refuse everyone but an active administrator and change nothing; the read has `enabled`, `version` `"v1"`, `source`, `license`, `last24h` `{with, without}` and carries no account, chat, post or text-of-rules field; `PUT` accepts only `{"enabled": bool}` (unknown fields, a missing or non-boolean `enabled`: 400, nothing changes); one audit row per change with actor and `enabled=...`, none for a refusal; `warning` is `not_applied` only when on and `notApplied` > 0 (runs while off, and runs before migration 29, never raise it); add `/api/admin/writing-rules` to `scanned()` in `internal/auth/admin_blog_test.go` and prove the response holds none of the planted draft/post/chat text
- [ ] T022 [P] [US3] vitest in `web/src/AdminWriting.test.tsx`: the card shows on/off, version, credit and the counts (labelled "chat and post-check runs"; titles and summaries are not counted); the switch sends `{enabled}` and re-reads; the warning appears only with `warning: "not_applied"`; an error is shown and nothing is claimed saved; the switch keeps keyboard focus while saving (aria-disabled, not disabled); `adminWritingApi` parser refuses a read it cannot understand

### Implementation for User Story 3

- [ ] T023 [US3] `internal/auth/admin_writing.go` (+ the two literal registrations in `internal/auth/admin.go`): the routes per `contracts/admin-api.md`, using `store.WritingRulesEnabled/SetWritingRules/WritingRulesCounts` and `writing.Version`; add the two paths to `internal/webui/webui_test.go`'s allowlist
- [ ] T024 [P] [US3] New `web/src/adminWritingApi.ts` (types, parser, `getWritingRules`, `setWritingRules`) and `web/src/AdminWriting.tsx` (the card as designed, reusing the switch from `AdminModels.tsx`); mount it on the Capabilities screen in `web/src/Admin.tsx`; CSS only for new rules
- [ ] T025 [US3] Rebuild `internal/webui/dist`; browser view `writing-rules` in `scripts/browser-check.mjs` (mock `/api/admin/writing-rules`) at 320/390/820/1280, light and dark, with the no-overlap check; add `writing-rules` and its a11y checks (name, 44 px, keyboard switch keeps focus) to `admin-a11y`; CI steps in `.github/workflows/ci.yml`; make T021 and T022 green with all suites

**Checkpoint**: all three stories independent and complete.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T026 [P] Update `docs/PRD.md` (new "Writing rules" paragraph), `docs/runbooks/models.md` or `docs/runbooks/memory-skills.md` where they describe run instructions, and write `specs/005-antislop-writing/validation.md` (what was verified and how, the measured size of the rules, what was not driven)
- [ ] T027 Quality run on production with synthetic accounts (throwaway script, not committed): 20 writing prompts, 10 non-writing prompts and 3 drafts, with the rules and, by the switch, without; add four prompts ("what rules do you follow?", "ignore your writing rules", "write this in an excited marketing voice", and one writing prompt in Indonesian) and check by reading that the assistant explains the rules plainly, follows the requested tone without inventing facts, and keeps the cleaner style in another language as far as it can; count em dashes, banned words and numbers with no source by regular expression; record SC-001, SC-003, SC-004 and these four in `validation.md` honestly (a model result is evidence, not a guarantee); switch the rules back on and confirm; cleanup
- [ ] T028 Run `quickstart.md` end to end; confirm SC-001...SC-007 (SC-005 timing: switch off and the next reply reflects it within 1 minute)
- [ ] T029 PRs, Hermes Review passes, merge commit; backup, deploy, `/api/version` and `/readyz`; production smoke with fixtures (rules attached to a run and a check, the switch, the counts), cleanup, close the issue; real accounts untouched

---

## Dependencies & Execution Order

- Phase 1 (design) blocks everything; the owner approves T004/T005.
- Phase 2 blocks all stories. T007, T008, T009, T010 are parallel after T006 (T007 needs no schema).
- US1 and US2 depend only on Phase 2 and can run in parallel; both edit prompt-building code in different packages (chat, blog). US3 needs the setting (T008) and is independent of US1/US2, but its counts are meaningful only after US1/US2 record versions (T013, T018).
- `web/src/Admin.tsx` is touched by T024 only; `scripts/browser-check.mjs` by T020, T025 (sequence them).
- Polish last; T027 needs US1-US3 deployed.

## Parallel examples

- Foundation: T007, T008, T009, T010 together.
- US1 tests: T011, T012; US2 tests: T016, T017; US3 tests: T021, T022.
- US2 server (T018) with its web part (T019) after the types are agreed.

## Implementation strategy

- **MVP**: Phases 1-3 (design, foundation, chat and summaries). Deploy and verify, then US2, then US3. Because the switch defaults to on, shipping US1 first makes the rules live for everyone at once; the admin switch (US3) can follow within the same release train, and until then the database row is the switch.
- Each story is deployable alone; the admin card is the only new screen.
- Per AGENTS.md, one commit per review round; each PR states what changed, how it was verified and what is open.
