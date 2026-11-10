# Implementation Plan: Blog Writing Feedback and the AI Editor

**Branch**: `006-ai-editor-blog` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-ai-editor-blog/spec.md`

## Summary

Three stories on the blog editor, shipped as three increments (each its own PR, design first):

1. **Toolbar truth (US1)**: the formatting buttons read their state from the editor through a subscription that re-renders on every selection or content change
   (today the toolbar reads `editor.isActive()` during a render that nothing triggers on cursor moves, so it goes stale). One helper computes for each button
   `on | off | mixed` over the whole selection (marks and block types), the button exposes it as `aria-pressed="true|false|mixed"`, and mixed is drawn differently.
2. **Check progress (US2)**: the server derives the check's step from the events Hermes already streams to it (`reasoning`, `tool.started/completed`, `message deltas`):
   `reading` -> `searching` (with a count of lookups) -> `writing`. It stores the step, when the step began and when the last event arrived on the check row, and the check
   read returns them with the server time. The web shows named steps, a clock that ticks in the browser (never announced), "last sign of life N seconds ago", and a
   plain timed-out / failed / cancelled screen. The same progress component is used in the Publish flow and in the new editor panel.
3. **The AI editor (US3)**: the check envelope gains `edits` (blog-check v3): `replace` (an exact span of one line of the author's text -> proposed text) and `insert`
   (proposed paragraphs after a part, used to expand a bare framework), each with a one-sentence reason, in the draft's language. Edits are stored per check
   (`post_edits`), shown beside the draft in a new **Check with the editor** panel and in the Publish review screen, and applied **by the editor in the browser** when
   the author accepts (one ordinary, undoable editor change; autosave saves it like any typing), so there is still exactly one writer of the draft. The server records only
   the decision. A check is no longer "out of date" because the draft changed: every suggestion and data block carries a text anchor and is found again in the current
   draft when it is used; one whose passage is gone is shown as no longer applicable (owner decision, Q2).

**No new dependency, no new service, no new route outside `/api/blog/*`, no change to `auth.PublicRoutes`/`PublicBlogRoutes`, no model call by Antares** (the assistant is
the author's own Hermes assistant, as for today's check). Two migrations (`0030` progress, `0031` suggestions). Per AGENTS.md and the owner's standing rule, the toolbar states, the editor panel,
the progress steps and the suggestion cards are drawn in `docs/design/antares.figcraft` and approved before code (Stage 0).

## Technical Context

**Language/Version**: Go (server), TypeScript + React 19 + Vite + Tiptap 3.33 (web).

**Primary Dependencies**: none new (`useEditorState` is in `@tiptap/react` already installed).

**Storage**: SQLite. Migration `0030_check_progress.sql` (US2): columns `post_checks.stage`, `stage_at`, `last_event_at`, `lookups`. Migration `0031_post_edits.sql` (US3): table `post_edits`, column `post_blocks.anchor_text`. See [data-model.md](data-model.md).

**Testing**: Go (envelope v2 validation incl. figures-not-in-draft, anchors relocation with the shared fixture, progress stage derivation from fake Hermes event scripts, decisions route ownership,
checks no longer go out of date, blocks relocate at publish, limits shared, admin-blindness guard for the new table and routes); vitest (toolbar states incl. mixed, progress component, suggestion
cards and accept/reject/no-longer-applicable, bridge apply and undo, parseCheck); real browser (`ONLY=editor-toolbar`: 50 cursor moves match the DOM; `ONLY=blog-editor` extended: panel, progress, suggestions,
no overlap, 44 px); a production quality run with synthetic accounts (finished draft, bare framework, Indonesian and English), recorded in `validation.md`.

**Target Platform**: the embedded web bundle (`internal/webui/dist`, rebuilt with each change).

**Project Type**: web application, existing.

**Performance Goals**: first named step within 2 s of starting, never more than 5 s behind (poll 1.5 s; the stage write is throttled to at most one per 2 s apart from a stage change); the toolbar updates within one frame of a transaction.

**Constraints**: constitution I-V; the draft is written only by the editor and its autosave; a suggestion never invents a figure (validated server-side), quote or link; the existing size limit (`maxCheckInput`), spending limit and per-author check limits apply unchanged (shared, Q3).

**Scale/Scope**: 2 migrations, 1 table, 5 columns, 1 route, 1 prompt version, 1 envelope extension, 1 new editor panel, 1 progress component, 1 toolbar hook.

## Decisions added after analysis (2026-11-11)

- Starting a check from the editor panel flushes autosave first and refuses with the same "not saved yet" sentence as Publish; only one surface polls a check at a time (the panel pauses while the Publish screen is open).
- The limit message says when the author can try again (`LIMIT_REACHED` with `retryAt`, the time the oldest check in the rolling 24 hours ages out; `running` when two are running now). The status is the one the code returns (422), not 429.
- The post's status stays `ready` after a finished check through further edits; the old reset to the previous status on a save is removed with the out-of-date rule.
- Polling goes from 2,000 to 1,500 ms in the US2 change, not in the behaviour-preserving extraction.

## Constitution Check

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I Default deny, admin blind | OK | `post_edits` rows carry `author_id`, every query filters on it; the decision route is the author's own and 404s otherwise; the table holds draft text, so it is added to the admin-blindness scan lists and the smoke-fixture classification; no admin route reads it. |
| II Front end, not an agent | OK | The suggestions come from the author's own Hermes assistant through the existing check run; Antares only validates the reply and applies the author's accepted change to the author's own draft. No second prompt pipeline: `blog-check v3` extends the one check prompt. |
| III Honest UI | OK | Progress shows only steps that happened (derived from real events), never a percentage; a failed, cancelled or timed-out check is shown as itself; a suggestion that no longer fits says so instead of applying or silently disappearing. |
| IV Tests guard behaviour | OK with a recorded change | Tests that asserted "an edit marks the check out of date" assert a rule the owner changed (Q2); they are rewritten to the new rule, not deleted, and the security invariants are untouched. See Complexity Tracking. |
| V Small footprint | OK | No new service or dependency; one table; anchors are strings. |

## Project Structure

### Documentation (this feature)

```text
specs/006-ai-editor-blog/
├── spec.md, plan.md, research.md, data-model.md, quickstart.md, tasks.md (next)
├── checklists/requirements.md
└── contracts/ check-envelope-v2.md, api.md, ui-contract.md
```

### Source Code

```text
internal/blog/
├── blog_prompt.txt        # blog-check v3 (edits grammar, language rule)
├── envelope.go            # edits validation (ParseEnvelopeParts), figures rule
├── anchors.go             # new: anchor text of a part, relocation in a document (shared fixture with the web)
├── check.go               # stage tracking in driveRun, edits stored in finishCheck, no pin / no out-of-date, blocks relocated at publish
├── edits.go               # new: DecideEdit, EditView
├── handlers.go            # PUT /api/blog/posts/{id}/edits/{editId}
└── testdata/anchors.json  # one fixture read by Go and vitest
internal/store/migrations/0030_check_progress.sql, 0031_post_edits.sql
web/src/blog/
├── formatState.ts         # new: on/off/mixed over a selection
├── Editor.tsx             # toolbar from useEditorState, "Check with the editor", the panel, the bridge
├── AssistPanel.tsx        # new: panel beside the draft (phone: below)
├── CheckProgress.tsx      # new: steps, clock, sign of life, stopped states
├── Suggestions.tsx        # new: cards (replace, insert), accept / reject / no longer applicable
├── draftBridge.ts         # new: applyEdit / undo against the live editor, anchors (same fixture)
├── useCheckRun.ts         # new: start, poll, cancel (extracted from PublishFlow)
├── PublishFlow.tsx, PreviewScreen.tsx   # use CheckProgress, Suggestions, useCheckRun
└── types.ts, blogApi.ts   # progress, edits
scripts/browser-check.mjs  # views editor-toolbar, blog-editor additions; CI steps
docs/design/antares.figcraft  # page "AI editor"
```

**Structure Decision**: the existing blog package and web blog folder; no new package.

## Complexity Tracking

| Departure | Why needed | Simpler alternative rejected because |
|-----------|-----------|--------------------------------------|
| Checks no longer go out of date on an edit; suggestions and blocks are found again by text anchors (Go for blocks at publish, TS for edits in the editor, one shared fixture) | Owner answer Q2: accepting one suggestion edits the draft, and an author must be able to work through a list | Keeping "any edit invalidates the check" would allow one accepted change per check. Server-side application of edits was rejected: it makes a second writer of the draft and conflicts with autosave's base version. |
| The accepted change can be stepped back by the editor's undo (and an Undo button right after accepting), not by a stored version | The post model keeps no draft history (a save starts a new version; older ones are deleted except the published one) | A snapshot per acceptance was rejected as new storage for a rare need. Owner accepted undo only (2026-11-11); FR-018 now says so. |
| Rule "the assistant must never rewrite the author's text" (spec 001 contract) is replaced by "never changes the draft itself; may propose changes" | The owner's request | Not optional; the contract file is amended in the same change. |
