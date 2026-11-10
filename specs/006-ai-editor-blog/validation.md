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

## Production (2026-11-11; US1 at 6925685a, US2 at c97d74ed, US3 at 7aaf72b4; a backup before each deploy)

US1 (toolbar): deployed and the served editor bundle carries the mixed state; the behaviour is the CI browser view `editor-toolbar` on the same bundle (a production browser run needs a signed-in session and was not done).
US2 (progress): smoke with synthetic accounts (cleanup clean): a check started at `reading`, then `searching` with 3 and 4 lookups, then `writing`, and finished `ready` with its progress kept.
US3 (AI editor): quality run with synthetic accounts (cleanup clean, real accounts untouched): 5 rough drafts and 5 bare frameworks, English and Indonesian. A model result is evidence, not a guarantee.

| Measure | Result |
|---|---|
| Checks that finished `ready` | 10 of 10, each through reading, looking things up (once not needed) and writing |
| Time to ready | 19 to 186 s; 9 of 10 under 3 minutes (the one over: 186 s) |
| Suggestions per rough draft | 3 to 5 (replace and insert, each with a reason): 5 of 5 drafts got at least one |
| Suggestions per 5-section framework | 5 to 7, five `insert`s in every one: prose written from the author's own points |
| Suggestions containing a figure that is not in the draft | 0 of all suggestions (the validator dropped none, so it was not exercised in production; it is covered by tests) |
| Notes, reasons and proposed text in the draft's language | all of them (English drafts in English, Indonesian drafts in Indonesian; a stopword check) |
| Draft unchanged after the check, and after accepting one suggestion and rejecting another (the server records only the decision) | 10 of 10 |
| Check still `ready` after an edit to the title | 10 of 10 (the new rule) |
| Publish original after a check | published, text equal to what was written |

Read by eye (one Indonesian rough draft and one English framework): the Indonesian suggestions are fluent Indonesian and keep the author's voice and meaning; the framework's proposed paragraphs were placed after each section's bullet list, each explaining why the two bullets say what they say, with no new facts or figures. In the first run the "sections expanded" figure read 0 of 5 for three frameworks because it counted only insertions placed directly after a heading, and the assistant placed them after the bullet list, which is the right place; the five insertions per framework are the evidence of one per section.

Not driven: a human timed trial, a larger or other-language sample, and the panel in a production browser session. The step "reading" was seen but not "looking things up" for one short Indonesian draft (the assistant needed no lookups).
