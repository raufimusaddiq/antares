# Feature Specification: Built-in Anti-Slop Writing Rules for Every Assistant

**Feature Branch**: `005-antislop-writing` (work happens on a ticket branch; see AGENTS.md)

**Created**: 2026-11-10

**Status**: Draft

**Input**: User description: "https://github.com/miqdadbadjuber/anti-slop make this skill as build in skill for chat and post asistant for every profile/account from antares in hermes"

## Context

The anti-slop skill (github.com/miqdadbadjuber/anti-slop, MIT) is a rulebook that tells an AI agent to avoid recognisable "AI slop": filler and hype words, em dashes, invented numbers and quotes, empty calls to action, generic structure. It has six parts: a core filter, UI, copywriting, accessibility, mobile layout and code comments. The core file alone is about 54,000 characters.

Antares already borrows a cut-down piece of it: the summary of a shared picture is written under a short style paragraph taken from this skill. Nothing else the assistant writes follows it. This feature makes the writing rules a built-in part of **every account's assistant**, for the two places the assistant writes for people: **chat answers** and the **post assistant** (the check an author runs before publishing, and the text it proposes). "Every account" means every existing account and every account created later, with nothing for the person to install or switch on.

## Clarifications

### Session 2026-11-10

- Q: Which parts of the upstream skill are built in? → A: The writing and copy rules only (no hype or filler words, no em dashes, no invented facts, quotes or sources, plain sentences). Not its UI, layout, mobile, accessibility or code-comment parts.
- Q: When do the rules apply in chat? → A: Only when the assistant is writing prose for the person to use (emails, posts, descriptions, summaries, drafts and similar). Explaining, answering questions and technical help are not shaped by them.
- Q: Can a person turn the rules off for their own account? → A: No. Only the administrator can switch them, for everyone.
- Q: Where does an author see the post assistant's style notes about their own draft? → A: In the check result they already read before publishing, as a short list of dismissible notes (what kind of problem, and where). A note never blocks publishing and never changes the draft.
- Q: Can people see what rules their assistant follows? → A: No separate page: only the administrator sees the rules and their version; the assistant explains what it keeps to if a person asks.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Writing the assistant does for you reads like a person wrote it (Priority: P1)

A signed-in person asks the assistant to write something for them to use: an email, a post, a product description, a summary, a draft. The text keeps to the writing rules: no hype or filler words, no em dashes, no padding openers and closers, no invented figures, quotes or sources, and a plain sentence instead of a clever one. When the person is only asking a question or getting an explanation or technical help, the assistant answers as it always did: the rules shape what it writes *for* the person, not how it talks *to* them. Code, data, tables and quotations are never touched.

**Why this priority**: Chat is where most text is read, and every account gets it without doing anything.

**Independent Test**: With a fixed set of writing prompts (a short email, a product description, a post intro, a summary) and a fixed set of non-writing prompts (an explanation, a debugging question), run the assistant for a new account and for an existing account. Violations of the rules in the writing replies, counted with a checklist taken from the rules, are far fewer than for the same prompts without the rules; the non-writing replies and any code or quoted text are unchanged.

**Acceptance Scenarios**:

1. **Given** an existing account created before this feature, **When** the person asks for a piece of writing, **Then** the text follows the rules, with no action from the person or the administrator.
2. **Given** an account created after this feature, **When** the person asks for a piece of writing in their first message, **Then** the text follows the rules.
2b. **Given** the person asks a question, or for an explanation or help with code, **When** the assistant answers, **Then** the answer is not reshaped by the rules.
3. **Given** a person asks for a block of code, a table or an exact quotation, **When** the assistant answers, **Then** that content is returned exactly as asked and, where the assistant is writing prose around it for the person, the rules apply only to that prose.
4. **Given** the person asks for something in a style of their own (for example "write this in an excited marketing voice"), **When** the assistant answers, **Then** the assistant does what was asked and still never invents facts, numbers or quotes.
5. **Given** the person's own saved personal instructions, **When** the assistant answers, **Then** both apply, and the person's instructions about tone win where they conflict with a style rule (never over the rule about invented facts).

---

### User Story 2 - The post assistant writes clean copy and flags slop (Priority: P1)

An author runs the check on a post. The assistant's own text (block titles, captions, notes, proposed wording) follows the writing rules, and it points out, as a note the author can ignore, where the author's draft itself leans on filler or hype. It never rewrites the author's text without the author accepting it, and never adds figures or sources it cannot back.

**Why this priority**: The post assistant writes text that can end up on a public page under the author's name.

**Independent Test**: Run the check on three drafts (one clean, one full of filler, one with invented-sounding statistics). The assistant's own text is clean in all three, the filler draft gets a note, the clean draft gets none, and no proposed block contains a number without a named source.

**Acceptance Scenarios**:

1. **Given** a draft full of hype words, **When** the check runs, **Then** the check result carries a short list of style notes, each naming the kind of problem and where it is, and the draft is unchanged.
   1b. **Given** style notes in a check result, **When** the author dismisses one, **Then** it disappears from the result, and none of them stops the author from publishing.
2. **Given** a clean draft, **When** the check runs, **Then** no style note is added.
3. **Given** the assistant would propose a block with a figure it cannot source, **When** it writes the block, **Then** it leaves the figure out or marks it as unverified, as the post assistant already does for sources.
4. **Given** any existing account, **When** it runs a check after this feature ships, **Then** the rules apply without anything changing in the author's settings.

---

### User Story 3 - The administrator knows it is on, and can switch it off (Priority: P2)

The administrator can see that the writing rules are active for all accounts and which version of the rules is in use, and can turn them off for everyone if they cause trouble. The change is recorded.

**Why this priority**: A built-in behaviour that cannot be seen or stopped is a liability for the one person who runs the service.

**Independent Test**: As the administrator, read the Models-style admin screen, see "Writing rules: on, version X", switch them off, and confirm that the next chat reply and the next post check no longer carry the rules; switch them on and confirm they return.

**Acceptance Scenarios**:

1. **Given** the rules are on, **When** the administrator opens the admin area, **Then** it says so and names the rules' version and source.
2. **Given** the administrator switches the rules off, **When** any account sends a message, **Then** the reply is produced without them, and an audit entry records who switched them and when.
3. **Given** a failure to apply the rules (for example the rules cannot be loaded), **When** a person chats, **Then** the chat still works without them and the administrator sees that the rules were not applied, rather than the chat failing.

---

### Edge Cases

- A request that is both an answer and a piece of writing (for example, explain this and draft the email): the rules apply to the drafted text only.
- A very long conversation or a long draft: the rules must not crowd out the person's content or push a reply over its limit.
- A language other than English: the rules that are about English words (for example a list of hype words) apply as far as they can; the rules about invented facts and filler apply in any language.
- The person asks the assistant to list or explain the rules, or to ignore them: it may explain what it keeps to; it must not treat the rules as secret, and it follows the person's own tone request except for inventing facts.
- A shared picture's summary already follows a short style paragraph: it must follow the same rules, not two different ones.
- A new version of the upstream rules: the version in use only changes when the administrator or a release changes it, never silently mid-conversation.
- An account whose assistant is not ready, suspended or pending: nothing changes for them (they have no assistant to write with).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every account's assistant MUST follow the writing rules when it writes prose for the person to use (emails, posts, descriptions, summaries, drafts and similar), whether the account existed before this feature or is created after it, with no action by the person. Answering questions, explaining and technical help are not shaped by the rules.
- **FR-002**: The post assistant MUST follow the same rules for everything it writes, and MUST add a style note (never an automatic rewrite) where the author's draft leans on filler or hype. Style notes appear in the check result the author already reads, as a short list; each can be dismissed, none blocks publishing, and none changes the draft.
- **FR-003**: The rules MUST NOT change code, data, tables, quotations or any content the person asked for verbatim.
- **FR-004**: The assistant MUST NOT invent figures, quotes, testimonials or sources under any style the person asks for; the rules about invented facts are never overridden by a person's tone request.
- **FR-005**: A person's own saved personal instructions MUST still apply; where they conflict with a style rule about tone or wording, the person's instruction wins, except for FR-004.
- **FR-006**: The shared-picture summary MUST use the same rules as chat, not a separate copy.
- **FR-007**: The rules MUST apply without the person installing, enabling or being shown anything, and MUST NOT be removable or editable by an ordinary account. There is no page for ordinary accounts to read the rules; only the administrator sees them and their version (FR-008). If a person asks, the assistant says plainly what it keeps to and does not pretend there are no rules.
- **FR-008**: The administrator MUST be able to see whether the rules are on and which version of them is in use, and to turn them off and on for everyone; each change MUST be audited.
- **FR-009**: If the rules cannot be applied, chat and the post check MUST still work, and the administrator MUST be able to see that the rules were not applied.
- **FR-010**: The rules in use MUST be a fixed, versioned copy kept with Antares (with the upstream licence and credit), so that behaviour changes only by a deliberate release or administrator action.
- **FR-011**: Applying the rules MUST NOT expose any user's content to another user or to the administrator, and MUST NOT make Antares call a model provider itself for chat (constitution Principles I and II).
- **FR-012**: Only the upstream skill's writing and copy rules are built in: no hype or filler words, no em dashes, no invented facts, quotes or sources, plain sentences. Its UI, layout, mobile, accessibility and code-comment parts are not.
- **FR-013**: In chat the rules apply only when the assistant is writing prose for the person to use; a reply that is only an answer, an explanation or technical help is not shaped by them. Where one request is both, the rules apply to the drafted text only.
- **FR-014**: Only the administrator can turn the rules off or on, for everyone. A person has no switch, and the assistant does not offer one.

### Key Entities

- **Writing rules**: a versioned set of rules with a source, a licence credit and a version label; one set for the whole service.
- **Rules setting**: whether the rules are on for everyone, who last changed it and when.
- **Style note**: a note in a post check's result when the author's draft leans on filler or hype: the kind of problem and where; dismissible, never blocking, never changing the draft by itself.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a fixed set of 20 writing prompts, at least 90% of replies have zero hard-rule violations (no em dash, no invented figure, no hype word from the rules' list), against a baseline measured without the rules; on 10 non-writing prompts, 0 replies are reshaped by the rules.
- **SC-002**: 100% of accounts, existing and new, get the rules on their next message or check, with no action by anyone.
- **SC-003**: 0 changes to code blocks, tables or quoted text across the fixed prompt set.
- **SC-004**: On three test drafts, the post assistant's own text has zero violations, the filler draft gets a note, the clean draft gets none, and no proposed block carries an unsourced number.
- **SC-005**: The administrator can see the state and version and switch the rules off, and the next reply reflects it within 1 minute; every change appears in the audit log.
- **SC-006**: Chat and post checks keep working in 100% of test runs where the rules cannot be loaded.
- **SC-007**: The rules add no more than a small, stated share to the size of each request, so replies are not noticeably slower (the plan sets the number; the writing-only scope is what keeps it small).

## Assumptions

- "Skill" here means the behaviour of the anti-slop rules, not necessarily Hermes' own skill-folder feature: how it is delivered is a plan decision, and must respect the constitution's rule that Antares does not modify Hermes data directories beyond profile create/delete and does not add a second prompt pipeline beyond the instructions it already sends.
- The upstream project is MIT licensed; the copy kept here carries its licence and a credit.
- The two surfaces are chat answers and the post assistant (check and its proposed text); other helpers (titles, summaries) follow the same rules through FR-006.
- Rules about visual design, layout, accessibility and code comments are out of scope (FR-012).
- The administrator switch is a single on/off for everyone, in the admin area, audited like other admin settings.
- Design comes first in figcraft (owner standing rule) for any new admin screen.
- Out of scope: auditing a person's own posts automatically, rewriting posts, per-account rule editing, and measuring reader reception.
