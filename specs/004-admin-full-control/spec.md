# Feature Specification: Admin Full Control of Models and Public Posts

**Feature Branch**: `004-admin-full-control` (work happens on a ticket branch; see AGENTS.md)

**Created**: 2026-11-10

**Status**: Draft

**Input**: User description: "finalize admin full control for: 1. default llm model settings for all user (media, chat, summarize, blog assistants). could turn on and turn off the model for all user 2. admin post management (takedown)"

## Context

What an administrator can already do today: approve, reject, suspend and reinstate accounts; choose the default image and video model for every account; block models for everyone (available only as a programming interface, with no screen); set which tools are allowed. What is missing, and what this feature adds:

- The administrator cannot choose the default model for **chat**, for **summaries and titles**, or for the **blog assistant** (the post check). Each takes whatever its source happens to default to.
- The model block list has **no screen**, so "turn a model off for everyone" needs a technical call.
- The administrator has **no way to remove a public blog post**. The only remedy today is suspending the author, which punishes the person and every other post they wrote (spec 001 left takedown "for a later spec"; spec 003 made posts easy to find, which makes this the next real gap).

The administrator stays **blind to user content**: this feature never lets them read chats, memory, files or drafts.

## Clarifications

### Session 2026-11-10

- Q: Does the author get any notice of a takedown beyond the status on their posts page? → A: Yes: also a one-time banner the next time they open Antares (no push, no email).
- Q: When the default chat model changes, does it apply to existing chats with no personal choice? → A: Yes: existing chats with no personal choice follow the current default at their next message; a chat with a personal choice keeps it.
- Q: If a purpose's default model is delisted or past sunset, what happens? → A: The purpose falls back to the service default, with a visible warning to the administrator on the models screen.
- Q: What usage can the administrator see on the models screen? → A: Totals per model only (calls and tokens), never per account.
- Q: Does the admin list include published posts hidden because their author is suspended or rejected? → A: Yes, marked as hidden because of the author's account state; only published content is shown.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Take down a public post (Priority: P1)

An administrator learns that a published post breaks the rules (spam, abuse, a leak). They find it in an admin list of public posts, take it down with a written reason, and it disappears from the blog index, the landing-page latest posts, search, and its own address (which no longer shows the content). The author is told it was taken down and why. The administrator can restore it later if the decision was wrong.

**Why this priority**: Posts are now easy to discover from the landing page, so an unremovable harmful post is the largest remaining risk and the only remedy today (suspending the author) is blunt.

**Independent Test**: Publish a post as one user. As the administrator, take it down with a reason. Check that a visitor can no longer see it in the list, in search, or at its address, that the author sees it as taken down with the reason, and that restoring it brings it back unchanged.

**Acceptance Scenarios**:

1. **Given** a published post, **When** the administrator takes it down with a reason, **Then** the post leaves the public blog index, search and the landing-page latest posts, and its address shows the same "not found" answer as a post that never existed.
2. **Given** a taken-down post, **When** its author opens their own posts, **Then** it is shown as taken down by an administrator, with the reason, and the author cannot republish it.
   2b. **Given** a post was taken down since the author last opened Antares, **When** they next open it, **Then** a one-time banner says which post was taken down and links to the reason; once dismissed or viewed it does not return.
3. **Given** a taken-down post, **When** the administrator restores it, **Then** it is published again exactly as it was and appears in the index and search.
4. **Given** the administrator takes a post down, **When** they view it afterwards, **Then** they see only what any visitor could have seen (its published content), never the author's drafts or earlier unpublished versions.
5. **Given** any takedown or restore, **When** the audit log is read, **Then** it records who acted, which post, the action and the reason, and the reason is shown to the author.
6. **Given** a person who is not an active administrator, **When** they try to list, take down or restore a post, **Then** they are refused and nothing changes.

---

### User Story 2 - Turn models on and off for everyone (Priority: P1)

An administrator opens an admin screen listing the models available to the service. They switch a model off, and from then on nobody can choose or use it: it disappears from every person's model picker, and a chat already set to it is told the model is unavailable rather than silently using another. Switching it back on restores it. Each change is recorded.

**Why this priority**: The control exists only behind a technical call today, so the owner cannot react quickly to a model that is too expensive, broken or unwanted.

**Independent Test**: As the administrator, switch one model off. As an ordinary user, confirm it is gone from the picker and cannot be selected or used by a chat, a summary, a title or the blog assistant. Switch it on and confirm it returns.

**Acceptance Scenarios**:

1. **Given** the models screen, **When** the administrator switches a model off, **Then** it is no longer offered to any user and cannot be used for chat, summaries, titles, media or the blog assistant.
2. **Given** a chat whose chosen model was switched off, **When** its owner sends a message, **Then** the message is not sent, the owner is told the model is unavailable, and no other model is substituted.
3. **Given** a model that is the current default for any purpose, **When** the administrator tries to switch it off, **Then** they are told which purposes depend on it and must pick another default first (or the change is refused), so a purpose is never left without a working model.
4. **Given** a switched-off model, **When** the administrator switches it on, **Then** it is offered again and users' own earlier choices work again.
5. **Given** the model catalogue cannot be read just now, **When** the administrator opens the screen, **Then** they see that plainly, the current switches are kept, and nothing is shown that is not really configured.

---

### User Story 3 - Choose the default model per purpose (Priority: P2)

For each purpose the service uses a model for, the administrator picks the default model all accounts get: **chat**, **summaries and titles**, **blog assistant**, **images** and **videos**. Each purpose offers only models that can do that job and are switched on. "Service default" leaves the purpose on its built-in default. A person's own choice in a chat still applies where chat already allows one; the administrator's default is what everyone gets until they choose otherwise.

**Why this priority**: It completes "full control", but the service works without it (each purpose already has a built-in default), and image and video defaults already exist.

**Independent Test**: Set a chat default and a blog assistant default as the administrator. As a user with no personal choice, start a new chat and run a post check, and confirm both use the administrator's models. Change a default and confirm the next use follows it.

**Acceptance Scenarios**:

1. **Given** the administrator sets a default chat model, **When** a user starts a new chat without choosing a model, **Then** the chat uses that default.
2. **Given** a user has chosen a model for a chat, **When** the administrator later changes the default, **Then** that chat keeps the user's choice.
   2b. **Given** an existing chat where the user never chose a model, **When** the administrator changes the default and the user sends the next message, **Then** that message uses the new default.
3. **Given** the administrator sets the summary and title default, **When** a title or a shared-picture summary is next written, **Then** it uses that model.
4. **Given** the administrator sets the blog assistant default, **When** an author runs the post check, **Then** it uses that model.
5. **Given** the administrator picks a default for a purpose, **When** they open the choices, **Then** only models that can do that job and are switched on are offered.
6. **Given** the administrator picks "Service default", **When** a purpose is next used, **Then** it behaves as it did before this feature.
7. **Given** the image or video default is changed, **When** it is saved, **Then** it keeps behaving as today (every ready account picks it up).

---

### Edge Cases

- A model is switched off while a run is already in progress: the run finishes; the next one is refused.
- The model chosen as a purpose's default disappears from the catalogue or passes its sunset date: the purpose reports the problem to the administrator and falls back to the service default, visibly, never silently to an unrelated model.
- Two administrators change the same setting at once: the last save wins and both are in the audit log.
- Taking down a post that is already taken down, or restoring one that is not: no change and a clear message.
- The author is suspended or rejected while their post is also taken down: it stays hidden; reinstating the author does not bring a taken-down post back.
- The author edits or unpublishes a taken-down post: allowed to edit as a draft, but it cannot be published again until an administrator restores it.
- The reason for a takedown is empty or very long: an empty reason is refused; a long one is bounded.
- A post is taken down while someone is reading it or while a share card of it is cached: new visits show "not found"; the administrator is told the removal covers the service only, not copies already saved elsewhere.
- An administrator takes down or restores their own post: allowed and audited like any other.

## Requirements *(mandatory)*

### Functional Requirements

**Post takedown**

- **FR-001**: An active administrator MUST be able to list published posts (title, author name, publish date, status), including posts hidden from visitors because their author is suspended or rejected, marked as such, and open one to see only its published content.
- **FR-002**: An active administrator MUST be able to take down a published post by giving a non-empty reason; a taken-down post MUST disappear from the public index, search, landing-page latest posts, and its address MUST answer exactly as a post that does not exist.
- **FR-003**: An active administrator MUST be able to restore a taken-down post; it returns as it was, with its original address and publish date.
- **FR-004**: The author MUST be able to see that a post was taken down, when, and the reason, and MUST NOT be able to republish it until it is restored. The author MUST also get a one-time in-app banner the next time they open Antares after a takedown; no push or email is sent.
- **FR-005**: Every takedown and restore MUST be recorded in the audit log with actor, post, action, reason and time.
- **FR-006**: An administrator MUST NOT be able to read, list or count any author's drafts, unpublished versions, post checks, or any reader activity through this feature.
- **FR-007**: The public routes and what a visitor receives MUST NOT change shape: a taken-down post is simply absent, with no marker visible to visitors.
- **FR-008**: A takedown MUST NOT change the author's account state or any other post of theirs.

**Model switches**

- **FR-009**: An active administrator MUST be able to see every model the service can offer, whether it is on or off, and which purposes use it as their default.
- **FR-009b**: The models screen MAY show usage totals per model (calls and tokens) to help decide what to switch off; it MUST NOT show usage per account. Totals cover chat and post-check runs only (titles and summaries are not recorded) and the screen says so.
- **FR-010**: An active administrator MUST be able to switch a model off or on for all accounts; a switched-off model MUST NOT be selectable or usable by any account for chat, summaries, titles, media or the blog assistant.
- **FR-011**: A chat set to a switched-off model MUST refuse to send and say the model is unavailable; it MUST NOT substitute another model.
- **FR-012**: The service MUST refuse to switch off a model that is the current default of a purpose, naming the purposes, until another default is chosen.
- **FR-013**: Every switch change MUST be recorded in the audit log with the actor and the model.
- **FR-014**: When the model list cannot be read, the screen MUST say so and keep existing switches; it MUST NOT show models as available that are not.

**Defaults per purpose**

- **FR-015**: An active administrator MUST be able to set the default model for each purpose: chat, summaries and titles, blog assistant, images and videos, or choose "Service default".
- **FR-016**: Each purpose MUST offer only models that can do that job and are switched on.
- **FR-017**: A purpose's default MUST apply to every account that has not chosen otherwise, including existing chats with no personal model choice (they follow the current default at their next message); a user's explicit per-chat choice MUST keep winning where chat already allows one.
- **FR-018**: A default that stops being usable (delisted, past sunset, switched off by another route) MUST fall back to the service default, and the models screen MUST show the administrator a visible warning naming the purpose and the model; users are not blocked.
- **FR-019**: Every default change MUST be recorded in the audit log.
- **FR-020**: Image and video defaults MUST keep their current behaviour, including re-checking every ready account on save.

**Cross-cutting**

- **FR-021**: All these actions MUST be available only to an active administrator; any other caller is refused and nothing changes.
- **FR-022**: The administrator screens MUST be reachable from the existing admin area, work on phone and desktop, in light and dark, and be keyboard and screen-reader usable.
- **FR-023**: No user conversation, memory, file or draft may be exposed to the administrator by this feature, and no model provider may be called on a user's behalf for chat (constitution Principles I and II).

### Key Entities

- **Takedown mark**: a mark on a published post: who took it down, when, the reason, and whether it was later restored. The post itself and its author are unchanged.
- **Model switch**: whether a model is on or off for everyone, with who changed it and when (today's block list, given a screen).
- **Purpose default**: the model chosen for one purpose (chat, summaries and titles, blog assistant, images, videos), or none (service default).
- **Audit entry**: an existing record of an administrator action; takedowns, restores, switches and defaults are added to it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After a takedown, 100% of public ways to reach the post (index, search, latest posts, its address) stop showing it within one minute (its images may stay cached for up to five), and a visitor sees no difference from a post that never existed.
- **SC-002**: An administrator can find a published post and take it down in under 1 minute.
- **SC-003**: An administrator can switch a model off, and it disappears from every account's picker, in under 30 seconds.
- **SC-004**: No switched-off model is used by any purpose after the switch (0 runs, summaries, titles, media or post checks).
- **SC-005**: A purpose is never left with no working model: 100% of attempts to switch off a default are refused with the purposes named.
- **SC-006**: 100% of takedowns, restores, switches and default changes appear in the audit log with actor and reason where a reason applies.
- **SC-007**: In kept tests, the administrator can never retrieve a draft, unpublished version, post check or reader activity.

## Assumptions

- "Summarize" means the two narrow helper uses that already exist: sidebar titles and shared-picture summaries. They share one default.
- "Blog assistant" means the post check an author runs before publishing.
- A takedown is **reversible** and does not delete anything; the author keeps their text and can edit it as a draft.
- The author is told through the post's status on their posts page and a one-time banner on next open, not by email or push.
- A user's own per-chat model choice stays; the administrator's default only applies when none was made.
- The block list is the same list as "switch off", extended with a screen.
- Published post content is already public, so the administrator seeing it is not a new exposure; drafts and unpublished versions stay invisible to them.
- The change adds no new service, queue or dependency (constitution Principle V). Public blog routes keep their current shape (Principle I).
- Design comes first in figcraft and is approved before code (owner standing rule).
- Out of scope: appeals, emails or push notifications to the author (the in-app banner is in scope), bulk takedown, automatic content scanning, per-user model overrides by the administrator, and any cost budgets.
