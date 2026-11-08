# Handover: blog feature

Split of work after the design was approved (PR #274). Design and pages are done in the figcraft session;
**backend and Hermes work goes to a separate session that has no FigCraft.** That session needs only this
folder, `docs/design/README.md` and the PRD. It never needs to open the `.figcraft` file.

## State

- Design approved by the owner (T011 ticked). The `.figcraft` file is the source of truth for look and flow;
  read `docs/design/README.md` and `contracts/design-pages.md` for the frame and flow list in text form.
- Setup branch `feat/blog-setup` (local worktree, not pushed): T012 empty packages, T013 TipTap 3.33.0 pinned
  (MIT; about +424 KB minified, +133 KB gzip over bare React as a standalone bundle; lazy-load on the editor
  route only). `internal/blog/doc.go` there is a placeholder; T016 replaces it.
- Not done: everything from Phase 3 on. No Go code exists yet.

## Backend and Hermes session (needs a Go toolchain, a Hermes, no FigCraft)

Tasks: T014 (fixtures, **blocker for US2/US3**), T015-T020, T021-T025, T027-T030, T039-T042, T044-T048,
T052-T053, T055, T057, T060 (server part), T063, T065, T068 (server part), T071, T072, T074, T075 (owner approval only).

Rules that apply:
- `AGENTS.md` and `docs/runbooks/security-invariants.md`: default deny, backend selects the profile,
  ownership on every query, admin blind. The public read surface is the one exception (Complexity Tracking in `plan.md`).
- T014 needs a live Hermes. Record real shapes with keys redacted; do not guess them. Update
  `contracts/check-envelope.md` to the recorded shape. Open risks R3 (run without a visible chat) and
  R4 (does Hermes expose the URLs web search returned) decide whether sourced data can be marked verified.
- Do not touch production without the owner's explicit approval.

## Page session (frontend, no Hermes needed; mock the API from `contracts/http-api.md`)

Tasks: T026, T031-T037, T043, T049-T050, T054, T056, T058-T059, T061-T062, T066-T067, T068 (UI), T070, T073.
Build each page from its frame; where design and code disagree, say so in the PR.

## Known environment traps

- No `go` on the Windows machine used so far; Docker Desktop was not running. Use Linux or WSL for Go.
- `vite build` fails on Windows on `main` (case clash `Artifacts.ts` / `Artifacts.tsx`). Build `internal/webui/dist`
  on Linux (T038 and the later rebuild tasks).

## Design gaps carried forward

Phone/Tablet/Landscape menus have no Blog entry; My posts and the Editor use a simplified rail; preview shows
the first two blocks only; interactions are drawn as static states. See `docs/design/README.md`.
