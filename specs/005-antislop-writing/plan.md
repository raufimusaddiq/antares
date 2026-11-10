# Implementation Plan: Built-in Anti-Slop Writing Rules for Every Assistant

**Branch**: `005-antislop-writing` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/005-antislop-writing/spec.md`

## Summary

A short, versioned copy of the writing rules of anti-slop (MIT) is kept inside Antares and **sent with every assistant run as instructions**, the
mechanism Antares already uses for the personal instructions and the OpenUI instruction. Nothing is written into any Hermes profile, so every
existing and future account gets it on its next run with no migration or backfill.

1. **Rules package** `internal/writing`: the rules text (embedded, ~2.3k characters, `writing-rules v1`, with the licence credit), checked at startup
   (present, within a size cap). One setting, "on" by default, owned by the administrator.
2. **Chat** (`internal/chat/personality.go` `runInstructions`): the rules go first, the person's own instructions after them, so the person's tone wins
   (FR-005). The rules text itself says they apply only when the assistant writes prose for the person (Q2 answer), not when it answers or explains.
3. **Post assistant** (`internal/blog/blog_prompt.txt`, `check.go`, `envelope.go`): the check prompt gains the rules and a fifth note kind, `style`
   (prompt `blog-check v2`). Style notes use the existing notes list in the check result, can be dismissed in the browser, never block publishing and
   never change the draft.
4. **Summaries**: the cut-down style paragraph in `internal/chat/summary.go` is replaced by the same rules text (FR-006).
5. **Administrator**: a card on Admin → Capabilities: on/off, the version and credit, and how many runs in the last 24 hours had the rules and how
   many did not (aggregate counts, no user; chat and post-check runs only). A switch is audited. If the rules are on but a run since this feature shipped went
   out without them, the card says so (FR-009): runs while the rules were deliberately off are recorded as `off` and never raise it, and runs that predate the migration are not counted.

**No new route outside `/api/admin/*`, no change to `auth.PublicRoutes`/`PublicBlogRoutes`, no new dependency, no new service, no Hermes data directory
touched, no model call by Antares for chat.** See [research.md](research.md) for the decisions and [contracts/](contracts/) for the interfaces and the
rules text.

Per AGENTS.md and the owner's standing rule, the Capabilities card and the style-note list are drawn in `docs/design/antares.figcraft` and approved
before code (Stage 0).

## Technical Context

**Language/Version**: Go (server), TypeScript + React 19 + Vite (web).

**Primary Dependencies**: none new (`embed` from the standard library).

**Storage**: SQLite. Migration `0029_writing_rules.sql`: table `writing_rules_setting` (one row), columns `runs.writing_rules` and
`post_checks.writing_rules` (`v1` attached, `off` deliberately switched off, NULL expected and not attached). A new shared-config table is classified for the smoke-cleanup guard.

**Testing**: Go tests (rules attached to chat runs, to checks and to summaries when on, absent when off, order with the personal instructions, the
`style` note kind, admin routes guarded and audited, counts, size cap, rules text invariants); vitest (Capabilities card, style-note list with
dismiss); real-browser checks (`ONLY=writing-rules`); a manual quality run of the fixed prompt set on production with synthetic accounts, results
recorded in `validation.md` (measuring scripts are not committed: AGENTS.md).

**Target Platform**: the existing embedded web bundle (`internal/webui/dist`, rebuilt with the change).

**Project Type**: web application, existing.

**Performance Goals**: the rules add at most 3,000 characters (about 750 tokens) to a run; the setting is read once per run (one indexed row).

**Constraints**: constitution I-V; the rules are identical for every account; no user content in the rules, the counts or the audit; the rules never
override a person's request for verbatim content.

**Scale/Scope**: 1 migration, 1 package, 2 admin routes, 1 admin card, 1 note kind, 3 prompt call sites.

## Constitution Check

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I. Default deny, admin blind | Pass | One text, the same for every account, sent by the backend (a request carrying instructions is still rejected). The admin sees a switch, a version and aggregate counts: no content, no per-account data. No public route added. |
| II. Front end, not an agent | Pass, reviewed | The rules ride the run `instructions` Antares already sends (personal instructions, OpenUI). No second prompt pipeline, no model call for chat, nothing forked. A Hermes skill folder per profile was rejected (research R1) because it would write into Hermes data directories. |
| III. Honest UI | Pass | The assistant says plainly what it keeps to if asked (the rules text requires it); the admin card states when rules were not applied; style notes are notes, not rewrites. |
| IV. Tests guard behaviour | Pass | See Testing; no guard is narrowed. |
| V. Small footprint | Pass | One migration, one small package, no service or dependency. |

Constitution "Production boundaries" (no Hermes data directory writes) is the reason for run instructions over a profile skill.

## Project Structure

### Documentation (this feature)

```text
specs/005-antislop-writing/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── rules-text.md     # the shipping text of writing-rules v1
│   ├── admin-api.md
│   └── ui-contract.md
└── tasks.md              # /speckit-tasks
```

### Source Code (repository root)

```text
internal/writing/{rules.md,writing.go}        # embedded text, Version, Text(), validation, Enabled()/Set via store
internal/store/writingrules.go                # setting, audit, counts
internal/store/migrations/0029_writing_rules.sql
internal/chat/{personality.go,service.go,summary.go}   # attach to runs and summaries; record the version on the run
internal/blog/{blog_prompt.txt,check.go,envelope.go}   # rules in the check, "style" notes, prompt v2
internal/auth/admin_writing.go                # GET/PUT /api/admin/writing-rules (+ registration in admin.go)
web/src/{AdminWriting.tsx,adminWritingApi.ts}  # the card (placed on the Capabilities screen)
web/src/blog/{types.ts,blogApi.ts,PreviewScreen.tsx}    # "style" note kind and dismiss
scripts/smoke_fixtures.py                     # classify writing_rules_setting
docs/design/antares.figcraft, docs/runbooks/writing-rules.md
```

**Structure Decision**: one small new package plus edits at the three existing instruction sites.

## Stage 0: Design (figcraft, before any code)

Page **Writing rules** in `docs/design/antares.figcraft`, light and dark, 1280 and 390 wide:

1. Admin → Capabilities: the "Writing rules" card (on/off switch like the Models switch, version and credit line, last-24-hours counts, the "not applied" warning state, switched-off state, saving and error).
2. The post check result: the style-note list (kind, where, dismiss button), with one note, several, none, and a dismissed state; phone and desktop; reuse the existing note styles.
3. Component check against the design system (switch from spec 004, note rows from the preview screen); any new component is added to `docs/design/README.md`.

Owner approval follows Stage 0; implementation starts only after it.
