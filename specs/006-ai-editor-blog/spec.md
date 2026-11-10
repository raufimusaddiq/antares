# Feature Specification: Blog Writing Feedback and the AI Editor

**Feature Branch**: `006-ai-editor-blog` (work happens on a ticket branch; see AGENTS.md)

**Created**: 2026-11-11

**Status**: Draft

**Input**: User description: "my experience from writing blog: 1. button status responsiveness for text effect 2. no progress when AI Assistant checking, so user don't know where the state is right now 3. enrichment not just mean the data, but to be the copywriter auditor too. i mean AI being my editor, so the notes come with the changes on my original note and it could enrich my blog, so people who just pour their blog framework could be enriched by the assistant. language following the blog language."

## Context

The author's own first-hand experience of writing a post in Antares. Three things got in the way:

1. The formatting buttons (bold, italic, heading, list, quote and so on) do not reliably show what is applied where the cursor is. The author cannot tell whether a text effect is on.
2. While the assistant checks a post, the screen says only that it "can take a minute or two". The author cannot tell whether it is reading, searching, or writing, or whether it is stuck.
3. The check adds data blocks and short notes, but never touches the author's own words. An author who pours in a bare framework (a title, a few headings, a few bullet points) gets notes about it and nothing that turns it into a post. The author wants the assistant to act as an editor: to propose changes to the original text itself, and to fill out a bare framework, in the language the post is written in.

Today the check is deliberately read-only about the author's text (spec 001: the assistant "must never rewrite, correct, shorten or extend the author's text"). Story 3 changes that rule on purpose, and only as proposals the author accepts one by one.

## Clarifications

### Session 2026-11-11

- Q: How much of the author's text may the assistant propose to write or rewrite? → A: Both: it edits what the author wrote and also expands a bare framework into full paragraphs built from the author's own points, marked as assistant-written until accepted.
- Q: Where does an accepted text change land? → A: Straight into the saved draft, as a new saved version the author can step back to; what the editor shows is what gets published.
- Q: Where does the author review the editor's suggestions? → A: At any time while writing: a "Check with the editor" action in the editor runs the same check and shows suggestions beside the draft; Publish still runs the check on the way out, as today.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The formatting buttons always show the truth (Priority: P1)

While writing, the author moves the cursor or selects text. The formatting buttons show, immediately and every time, which effects apply at that point (bold, italic, heading level, list, quote, code, link) and which do not. Pressing a button changes the text and the button together, with no lag and no stale state. On a phone the buttons stay easy to hit and never overlap.

**Why this priority**: It is the smallest fix with the most daily value: the author touches these buttons all the time, and a wrong state makes every other feature feel broken. It needs no assistant at all.

**Independent Test**: In the editor, type a sentence, make one word bold and another a heading. Move the cursor between them with the mouse and with the arrow keys: the buttons switch to match each place at once. Select a range that is partly bold: the button shows a mixed state, not a wrong "on" or "off".

**Acceptance Scenarios**:

1. **Given** the cursor is inside a bold word, **When** the cursor moves to plain text (click, arrow key or touch), **Then** the Bold button shows "off" and moving back shows "on", with no further action by the author.
2. **Given** the author presses Bold with nothing selected, **When** they type, **Then** the button shows "on" and the typed text is bold; pressing it again turns both off.
3. **Given** a selection that is partly bold, **When** the author looks at the Bold button, **Then** it shows a state that is clearly neither fully on nor fully off, and one press makes the whole selection bold.
4. **Given** the cursor is in a heading, list or quote, **When** the author looks at the toolbar, **Then** the matching button is on and the buttons that cannot apply there are not misleading.
5. **Given** a phone, **When** the toolbar is shown, **Then** every button is at least 44 px, named for screen readers, and no button covers another.

---

### User Story 2 - The author always knows what the assistant is doing (Priority: P1)

When the author starts a check, the screen shows where the check is: which step it is in (reading the post, looking things up and finding sources, writing the notes and suggestions), how long it has been running, and that it is still alive. If it stalls, fails or is cancelled the screen says which and what the author can do. The author can leave and come back and still see the same picture.

**Why this priority**: A minute or two of silence reads as "broken" and makes authors cancel or reload, which wastes the check. It is independent of Story 3: today's check gets the progress too.

**Independent Test**: Start a check on a post with several claims. The screen moves through named steps with a running time, ends in the review screen, and never shows a step that is not true. Start a check and close the tab: on return the screen shows the current step.

**Acceptance Scenarios**:

1. **Given** the author starts a check, **When** the assistant is reading, searching or writing, **Then** the screen names the current step in plain words and shows the time spent, and earlier steps show as done.
2. **Given** a step takes longer than usual, **When** nothing new has arrived for a while, **Then** the screen says it is still working and how long since the last sign of life, instead of freezing.
3. **Given** a check that stalls beyond the allowed time, **When** the limit is reached, **Then** the screen says it timed out, nothing is published, and Try again and Back to editing are offered.
4. **Given** a check running, **When** the author closes the page and opens it later, **Then** the screen shows the real current step, not a restart.
5. **Given** a check running, **When** the author cancels, **Then** the screen stops showing progress and says it was cancelled.
6. **Given** a screen reader, **When** the step changes, **Then** the new step is announced once, politely, and the time is not announced every second.

---

### User Story 3 - The assistant works as the author's editor and fills out a bare framework (Priority: P1)

The check no longer only adds data blocks and notes. It also proposes changes to the author's own text, as visible suggestions: a passage with the improvement beside it (clearer wording, a tighter sentence, a missing transition, a claim to soften or support), each with a short reason. For a bare framework (a title, headings and bullet points with little prose) the assistant proposes a fuller text under each heading, built from the author's own points. The author can ask for this at any time while writing (a "Check with the editor" action in the editor, with the suggestions shown beside the draft), and it also runs on the way to Publish as today. The author reviews every suggestion, accepts or rejects it one at a time, and nothing in the post changes until they accept. Suggestions and any written text are in the language the post is written in, and keep the author's voice. Text the assistant wrote is marked as such until the author has accepted it.

**Why this priority**: This is the author's main ask and the biggest change in value, from "extra data" to "an editor". It builds on the review screen that exists, so it ships after Stories 1 and 2, which are small and independent.

**Independent Test**: Take two drafts: one finished but rough, one a bare framework of five headings with bullets. Run a check. The first returns specific text suggestions with reasons, each shown beside the original passage; the second returns a proposed text under each heading. Accept one, reject one: only the accepted one changes the draft. Both are in the post's language, and nothing was changed before the author's choice.

**Acceptance Scenarios**:

1. **Given** a finished draft, **When** the check completes, **Then** the review screen lists text suggestions, each with the original passage, the proposed text and a one-sentence reason, and every one can be accepted or rejected on its own.
2. **Given** a bare framework (headings and bullets), **When** the check completes, **Then** the assistant proposes prose for each section from the author's own points and marks it as written by the assistant.
3. **Given** suggestions on screen, **When** the author accepts one, **Then** only that passage changes in the draft, the change can be undone, and the rest is untouched; rejecting changes nothing.
4. **Given** a post written in Indonesian (or any other language), **When** the check runs, **Then** the notes, reasons and proposed text are in that language, not in English.
5. **Given** an assistant that proposes a figure, a quote or a source, **When** the suggestion is shown, **Then** it carries a real source or is left out, as today's blocks do; no suggestion invents a fact.
6. **Given** the author never accepts any suggestion, **When** they publish, **Then** the post is exactly what they wrote.

---

### Edge Cases

- A selection that spans a bold word, a link and plain text: the buttons show a mixed state and still work.
- A very short draft (a title only): the assistant says there is too little to edit instead of inventing a whole post.
- A very long draft: the check still refuses clearly (as today) and the progress screen never claims a step that did not run.
- The author edits the draft while a check runs: the check is marked out of date, as today; no suggestion is applied to a changed draft.
- A suggestion whose original passage the author has since changed: it is shown as no longer applicable, not applied.
- A post that mixes two languages: the assistant follows the language of the passage it is about.
- The assistant returns something unusable: the author sees which suggestions were dropped and why, never a silent gap.
- Progress that cannot be known exactly: the screen shows the step and elapsed time, never a fake percentage.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The formatting buttons MUST reflect the text effects at the cursor or selection immediately after every cursor move, selection change, key press and button press.
- **FR-002**: A selection that is only partly formatted MUST show a mixed state that differs from "on" and "off".
- **FR-003**: Every formatting button MUST have an accessible name and state, be at least 44 px on a phone, and never overlap another control.
- **FR-004**: While a check runs, the screen MUST name the current step in plain words, show elapsed time, and mark finished steps as done.
- **FR-005**: The screen MUST show that the check is still alive and how long since the last sign of life, and MUST NOT show a step that has not happened or a percentage that is not measured.
- **FR-006**: A stalled, failed, timed-out or cancelled check MUST say which of these happened, that nothing was published, and what the author can do next.
- **FR-007**: Leaving and returning to a running check MUST show its real current step.
- **FR-008**: Step changes MUST be announced to assistive technology once and politely; the clock MUST NOT be announced every second.
- **FR-009**: The check MUST be able to propose changes to the author's own text as suggestions, each with the original passage, the proposed text and a one-sentence reason.
- **FR-010**: For a draft that is mostly a framework (headings and bullets with little prose), the check MUST be able to propose prose for each section built from the author's own points.
- **FR-011**: Nothing in the author's draft MUST change until the author accepts a suggestion; accepting MUST change only that passage and MUST be undoable; rejecting MUST change nothing.
- **FR-012**: Notes, reasons and proposed text MUST be in the language the post (or the passage) is written in.
- **FR-013**: Text written by the assistant MUST be marked as such until accepted, and a suggestion MUST NOT state a number, quote or source it cannot support; unsupported specifics are left out.
- **FR-014**: A suggestion whose original passage has changed MUST be shown as no longer applicable and MUST NOT be applied.
- **FR-015**: Publishing without accepting any suggestion MUST publish exactly the author's text.
- **FR-016**: The check, the progress and the suggestions MUST stay private to the author while a draft: the administrator never sees a draft, a suggestion or a check (constitution principle I).
- **FR-017**: The assistant MAY both edit the author's own sentences and expand a bare framework into full paragraphs built from the author's own points; expanded text MUST come from those points and MUST NOT add facts the author did not give (see FR-013).
- **FR-018**: Accepting a suggestion MUST write the change into the saved draft as a new saved version, so the author can step back to the version before it; the editor and the published post MUST show the same text.
- **FR-019**: The author MUST be able to run the check from the editor at any time while writing, and see its progress (FR-004 to FR-008) and its suggestions beside the draft; the check that runs on the way to Publish MUST give the same suggestions in the review screen.

### Key Entities *(include if feature involves data)*

- **Check progress**: the step a running check is in (reading, looking things up, writing suggestions), when it started, and when it last showed a sign of life. About one check, shown only to its author.
- **Text suggestion**: one proposed change to a passage of the author's draft: the original passage, the proposed text, a one-sentence reason, whether the assistant wrote new text, and the author's decision (open, accepted, rejected, no longer applicable). Belongs to one check and one draft version.
- **Post language**: the language the post (or a passage) is written in, which notes and suggestions follow.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a real browser, after any of 50 cursor moves and selections across bold, italic, heading, list, quote, code and link text, the toolbar matches the text at the cursor every time (no stale state).
- **SC-002**: During a check, the screen shows a named step within 2 seconds of starting, and the displayed step is never more than 5 seconds behind the real one.
- **SC-003**: Authors can tell what a running check is doing: in a short trial, at least 9 of 10 can say which step it is in without help.
- **SC-004**: On a finished draft, at least one useful text suggestion with a reason is returned in at least 8 of 10 test drafts, in under 3 minutes.
- **SC-005**: For a bare framework of five headings with bullets, the assistant proposes prose for every heading in the post's own language in at least 8 of 10 test frameworks, with no invented figures, quotes or sources.
- **SC-006**: In 100% of tests, the draft is byte-for-byte unchanged until the author accepts a suggestion, and publishing without accepting publishes the original.
- **SC-007**: In 100% of language tests (Indonesian and English), notes, reasons and proposed text are in the post's language.

## Assumptions

- The three stories ship separately: Story 1 (buttons) and Story 2 (progress) first, Story 3 (editor) after them.
- Progress shows steps and elapsed time only. A percentage is shown only if it is measured, and that is not expected.
- Detecting the post's language is automatic. The author does not set it.
- The built-in writing rules (spec 005) apply to everything the assistant writes here, and the author's own tone wins.
- The existing rules of the check stay: same spending limits, same refusal for a very long post, the author's own assistant and model, web search only for real sources.
- The existing data blocks (tables, charts, sources) stay as they are; text suggestions are an addition to them.
- Mobile is in scope for all three stories (the author writes on a phone too).
