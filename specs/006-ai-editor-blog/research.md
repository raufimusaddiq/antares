# Research: Blog Writing Feedback and the AI Editor

## R1. Why the toolbar is stale, and the fix
`Editor.tsx` creates the editor with `useEditor` and draws the toolbar with `editor.isActive("bold")` etc. in the component's render. In Tiptap 3 a React component does not re-render on a selection or
transaction unless it subscribes (`shouldRerenderOnTransaction` defaults to false), so the buttons keep the state of the last render. **Decision**: select the state with `useEditorState` (subscribes to transactions, re-renders only when the selected value changes).
**Rejected**: turning on `shouldRerenderOnTransaction` (re-renders the whole editor component, including its content props, on every keystroke).

## R2. Mixed state
`isActive` answers "is it on for the whole selection" for marks and "is the selection inside this node" for blocks. For a mixed answer, walk the text blocks and text nodes in the selection and count how many have the mark or block type:
all -> on, none -> off, some -> mixed. An empty selection uses the stored marks / the cursor's parent. **Decision**: `formatState.ts` returns `on|off|mixed` per button; `aria-pressed="mixed"` is a valid value for a toggle button; the visual is a half-filled outline (not colour alone).

## R3. What can be known about a running check
The check reads Hermes' event stream already (`driveRun` callback). Frames normalise to `KindThinking`, `KindToolStarted`, `KindToolCompleted`, `KindDelta`, `KindTerminal` (`internal/hermes/events.go`). **Decision**: stage `reading` from the start until the first tool starts; `searching` from the first tool start until a message delta arrives
(`lookups` counts tool starts); `writing` from the first delta. A later tool start returns to `searching` (stage can go back; the screen shows the current one and keeps the done marks for steps passed at least once). Every event updates `last_event_at`, written at most once per 2 s; a stage change is written at once.
**Rejected**: a percentage (not measurable), per-tool names in the UI (a tool label can carry the author's own text, and the screen needs only "looking things up").

## R4. How the web knows "N seconds ago" without trusting its own clock
The read returns the server's `now`; the browser computes its offset once per response and ticks locally. Elapsed = `now - startedAt`, sign of life = `now - lastEventAt`, both server-time based. Announcements: a polite live region changes only when the step changes; the ticking clock is `aria-hidden`.

## R5. Edits: shape and where they are applied
The assistant works on a numbered, plain-text view of the draft (`[i] text`, headings `## `, list items `- `). A suggestion must therefore be expressible against that view and applicable to the rich document in the browser. **Decision**: two kinds.
`replace {part, original, proposed, reason}`: `original` is an exact span inside one line of the part (after the `## ` or `- ` marker is removed, no newline); applied by replacing that span with plain text in the matching text block (marks inside the replaced span are not kept; the surrounding text keeps its own).
`insert {part, proposed, reason}`: one or more paragraphs placed after the top-level part; used to expand a bare framework and for a missing transition.
**Rejected**: replacing a whole part (loses its formatting and its links), markdown or HTML in `proposed` (needs a parser and a sanitiser; plain text is inert), the server applying the edit (second writer, see Complexity Tracking).

## R6. Anchors and "no longer applicable"
Each stored suggestion and block keeps the text of the first text block of its part (collapsed whitespace, at most 120 characters) and the part index at check time. To use one, search the current document for a top-level part whose first text block equals the anchor, nearest to the old index first.
`replace` additionally needs `original` to still be in a text block of that part. None found -> no longer applicable. Go does this for blocks when publishing and when reading a check (so the preview places blocks right); TypeScript does it for edits in the editor. **One JSON fixture** (`internal/blog/testdata/anchors.json`) of documents and expected results is read by both the Go test and the vitest test so they cannot drift.

## R7. No figure that is not in the draft
FR-013 and SC-005: no invented figure, quote or source. A model cannot be fully constrained by a prompt, so the server rejects a suggestion whose `proposed` contains a number (digits, with their separators) that does not appear in the draft text, a URL or an address, or a double-quote span longer than a few words that is not in the draft; the author is told one sentence ("Suggestion 3 was removed: it contained a figure that is not in your draft"). Real data stays the job of the sourced blocks. **Tested** with fixtures.

## R8. Language
The author writes in one language, usually. **Decision**: the prompt says: write notes, reasons and proposed text in the language of the draft (of the passage, for a mixed post); no language field, no detection in Antares. Verified on production with an Indonesian and an English draft (SC-007); a fake cannot prove a model's language, so the unit tests assert the instruction is in the prompt and the production run reads the output.

## R9. How many suggestions
Product rule: about one per section, no fixed cap. The prompt asks for that and for the most important first; the envelope accepts at most `4 x parts + 8` (a safety bound against a runaway reply, well inside `maxEnvelope`), and says so in a notice when it drops the rest.

## R10. One check, two entrances
A check started from the editor and one started from Publish are the same server object (the post's latest check). Publish opens on the review screen when the latest check is ready (as today), so an author who checked while writing and then presses Publish reviews that check; "Check again" starts a new one. Limits are shared (Q3): `maxChecksPerUser` 2 at once, `maxChecksPer24h` 30, the daily spending limit.

## R11. Draft private, admin blind
`post_edits` holds proposed text and the author's own passages. Same rules as `post_blocks`: `author_id` on every row and every query, never in an admin response, in `smoke_fixtures.py` as owned, in the admin-blindness test's planted-text scan.

## R12. The check no longer pins a version
Pinning kept the version a check read from being edited in place. With anchors the check needs no version, so `StartCheck` stops pinning and `outOfDate` stops running on a save. The published version is still pinned. Tests of the old rule are rewritten (Complexity Tracking).
