# Handover: the blog feature (complete)

Every task in `tasks.md` is done. The feature is live at `/b` (public) and `/blog` (authors), and the AI check is on by default.
Production runs `e3bea183` or later; its last full smoke, on `e3bea183`, passed 19/19 with `cleanup=clean` (including a real check
on the author's own assistant).

## Where things are

| What | Where |
|---|---|
| What was asked, and why | `spec.md`, `plan.md`, `research.md`, `data-model.md`, `contracts/` |
| Proof it works: the three quickstart commands, the fifteen scenarios with the tests behind each, `go test -cover` before and after | `validation.md` |
| How to run, switch off and pay for the AI check | `docs/runbooks/blog-check.md` (`ANTARES_BLOG_CHECK=on|smoke|off`; limits: one running check per post, two per author, 30 per rolling 24 h, the daily hard spending limit) |
| Smoke test and deploy | `docs/runbooks/smoke-test.md`, `docs/runbooks/deployment.md` |
| The design, and where the code and the frames still differ | `docs/design/antares.figcraft`, `docs/design/README.md` (*Blog: where the frames and the code still differ*) |
| Rules this feature added or relies on | `.specify/memory/constitution.md` (v1.1.0), `docs/PRD.md` §30, `docs/runbooks/security-invariants.md` (default deny, ownership on every query, admin blind) |

## What was decided on the way (so nobody reopens it by accident)

- **Sources are never verified.** Hermes exposes the search query but not the results (research R4), so every source is stored
  `verified: false` (the database refuses otherwise) and every block that names sources needs the author's tick before it can be
  accepted. The product never shows a "verified" state.
- **An edit after a check makes it out of date** and the author re-runs it (T057). Nothing is reused across an edit.
- **The AI-added marker stays above a block** (T062), not beside its title: every block must carry it.
- **Sorting is not built:** OpenUI's table has no sort control (T059).
- **Publish is a dialog over the editor**; its later steps are the page itself below a "Back to editing" bar (a fixed full-screen
  layer made mobile Chrome widen its layout to 832px).
- **Admin is blind to posts and drafts**, proven by scanning every admin endpoint (`internal/auth/admin_blog_test.go`), and a
  new admin route fails that test until it is scanned.

## Known differences and open edges

- The frames and the code differ in a few listed places (the README section above); the biggest is that the editor screens were
  not compared frame by frame.
- Not drawn in the design or built: a sources list at the end of a public post, a cover-image control in Post settings (the API
  accepts it), most of the smaller states listed under *Known gaps* in the README.
- `cmd/antares-smoke` and `internal/smoke` carry production-only paths that unit tests do not reach; every production smoke run
  exercises them.
- CI occasionally creates fewer jobs than the workflow defines and reports the run as failed with every created job green
  (seen once, on #303). Closing and reopening the PR starts a clean run without changing the commit.

## Process notes for the next feature

- Changes start in the design file, then the code; a UI change lands with its design change (`AGENTS.md`).
- Run the real-browser checks after any layout change: the fixed-layer width bug passed every unit test and the document's own
  scroll-width check, and only the browser check on a phone viewport found it.
- A task is ticked only when the file or test it names exists in the branch; reviewers check, and have caught it.
