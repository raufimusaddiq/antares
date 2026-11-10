# Validation: Blog Writing Feedback and the AI Editor (spec 006)

Verified before the US3 merge (2026-11-11): `go vet`, `go test ./...`, the Python self-tests, 962 web tests, and the real-browser views `editor-toolbar` (16 checks),
`blog-editor` (34 checks, the progress steps), `ai-editor` (40 checks at 320/390/820/1280 px, light and dark).

| Success criterion | How verified |
|---|---|
| SC-001 toolbar matches the text at the caret | `ONLY=editor-toolbar`: 52 cursor moves and selections against the DOM and the editor, at four widths, light and dark; it fails on the old code |
| SC-002 a named step within 2 s, never more than 5 s behind | `progress_test.go` (the step is written at once on a change; polling is 1.5 s); production smoke after US2 |
| SC-003 the screen names the real step | `ONLY=blog-editor` (steps, clock, a reload shows the real step, a quiet run says so), `CheckProgress.test.tsx` |
| SC-004, SC-005, SC-007 suggestions, framework expansion, language | production quality run (below) |
| SC-006 the draft is unchanged until an accept; publish without accepting publishes the original | `TestACheckStoresItsEditsAndTheAuthorDecidesEachOne` (a check and a decision leave the document and version byte-identical), `ONLY=ai-editor` (the draft equals what was typed until Accept), the publish tests |

Decision recorded: FR-018 is "undoable" (the editor's undo and an Undo right after accepting); the product keeps no draft history.

Not yet driven (follow-up after the deploy): the production quality run with synthetic accounts (T037) and the production smoke (T038).
