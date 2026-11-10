# Feature Specification: Discover and Read Other People's Blog Posts

**Feature Branch**: `003-blog-discovery` (no git branch is created by this command)

**Created**: 2026-11-10

**Status**: Draft

**Input**: User description: "in landing page, un-login user/pending user could browse the blog post and read what's people post in antares; login user has entry point to view blog post too (owned by themselves or others)"

## Background

Published posts are already public: anyone with an address can read one, and a public index lists them. But nothing in the product leads there. The sign-in page does not mention the blog, the waiting screen for a pending account does not either, and a signed-in person's chat menu offers only "Blog posts", their own writing workspace. So people only meet a post if someone sends them a link. This feature adds the doors; it does not change what is public.

## Clarifications

### Session 2026-11-10

- Q: Should the sign-in page show a few recent posts itself, or only a "Read the blog" link? → A: Link plus a strip of the 3 latest posts below the sign-in action.
- Q: Should suspended or revoked accounts also get the read-only blog entry, or only pending ones? → A: Every non-approved account (pending, suspended, revoked) gets it.
- Q (owner, after the design review): how should the latest posts look, and do waiting screens get them? → A: each of the 3 latest posts is a clickable card (the whole card opens the post), and the waiting, suspended and revoked screens show the same strip as sign-in (supersedes "entry only, without the strip").
- Q: Should signed-in users get an "All / Mine" filter on the list? → A: No; one list for everyone, with the reader's own posts marked "Yours".
- Q: How should the list know which posts are the reader's own? → A: A separate call available only to signed-in readers returns just the addresses of their own published posts; the public routes stay unchanged and never identify the reader (changed from an earlier "no mark" answer at the owner's request).
- Q: Where does the back control go after reading a post? → A: To where the reader started (sign-in, waiting screen or chat); the list has its own back control to the same place, and "More posts" on a post opens the list.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A visitor browses the blog from the landing page (Priority: P1)

Someone who is not signed in opens Antares and sees the sign-in page. Besides signing in, it shows a short strip of the three latest posts, each a clickable card (title, author, date), below the sign-in action and a clear "Read the blog" entry. The entry opens the list of published posts, where they can open any post and read it without an account. From the list and from a post there is an obvious way to go back to the sign-in page.

**Why this priority**: It is the first half of the request and the only way a new visitor can see what Antares people write before deciding to sign up.

**Independent Test**: With no session, open the sign-in page, use the blog entry, open a post, and return to sign-in, on a phone and on a desktop; confirm no account was needed and nothing private appeared.

**Acceptance Scenarios**:

1. **Given** a visitor with no session on the sign-in page, **When** they look for the blog, **Then** a "Read the blog" entry is visible without scrolling on a phone-width screen and is reachable by keyboard.
2. **Given** the visitor uses it, **When** the list opens, **Then** it shows published posts newest first, each with title, summary, author name, date and cover when there is one, and opening one shows the full post.
3. **Given** the visitor is on the list or on a post, **When** they want to sign in, **Then** a Sign in entry starts sign-in in one step.
4. **Given** no post has been published, **When** the visitor opens the blog, **Then** an honest empty message appears instead of a blank page.
5. **Given** the sign-in page, **When** it loads, **Then** the strip shows the three most recently published posts as cards (title, author, date), the whole card being the link that opens that post, and the sign-in action stays the most prominent control.
6. **Given** the strip cannot load or there are no posts, **When** the sign-in page shows, **Then** the strip is simply absent (sign-in is never blocked or delayed by it) and the "Read the blog" entry remains.

---

### User Story 2 - A pending account can read the blog while it waits (Priority: P1)

A person who signed in but whose account is not yet approved sees the waiting screen. They cannot use chat, but the same strip of the three latest posts (clickable cards) and the "Read the blog" entry are there, so the wait is not a dead end. Reading never gives a pending account any other access, and their status screen stays one step away.

**Why this priority**: The user named this group explicitly; they are the people with the most idle time and no other thing to do.

**Independent Test**: Sign in with an account in the pending state; use the blog entry from the waiting screen; read a post; return to the waiting screen; confirm chat, drafts and writing stay unavailable.

**Acceptance Scenarios**:

1. **Given** a pending account on the waiting screen, **When** it loads, **Then** the three latest posts show as cards (absent when empty or failed); **When** they open a card or use "Read the blog", **Then** they can browse and read posts exactly as a visitor can.
2. **Given** that account is reading, **When** they choose to return, **Then** they land on the waiting screen (not the sign-in page), still signed in.
3. **Given** that account, **When** they open the blog, **Then** no writing, sharing-to-own or chat entry is offered to them, and approval of the account does not depend on this reading.
4. **Given** a suspended or revoked account, **When** it reaches its status screen, **Then** the same read-only blog entry is offered and returns to that screen, because it grants nothing the public does not already have. One rule applies to every non-approved account.

---

### User Story 3 - A signed-in user reaches the blog from the chat (Priority: P1)

An approved user in the chat sees, in the menu and in the side rail, both "Blog posts" (their own workspace, as today) and a new "Read the blog" entry that opens the public list. The list includes their own published posts and everyone else's, in one place, newest first. They can open any post, and from any post or the list return to the chat in one step. When writing is available to them, the list and a post also offer a way to their own workspace.

**Why this priority**: This answers the owner's question directly: today a signed-in user cannot discover other people's posts.

**Independent Test**: Sign in as an approved user, use the new chat-menu entry, find a post by another user and one of your own, read both, and return to the chat; repeat on a phone-width screen.

**Acceptance Scenarios**:

1. **Given** an approved user in the chat, **When** they open the menu, **Then** "Read the blog" is listed next to "Blog posts" and the two are clearly different (reading everyone's posts versus managing my own).
2. **Given** the user opens the list, **When** it loads, **Then** their own published posts appear among the others, marked "Yours", and their drafts do not appear.
3. **Given** the user is reading any post, **When** they choose to go back, **Then** they return to the chat they came from, not to a sign-in page.
4. **Given** the user cannot write (writing not available to them), **When** they open the menu, **Then** "Read the blog" is still offered; "Blog posts" keeps its current rule.
5. **Given** a signed-in user, **When** they read a post, **Then** nothing about their reading is shown to the author or to anyone else.

---

### User Story 4 - Finding something worth reading (Priority: P2)

With more than a handful of posts, the list must stay usable: a first page loads quickly, further posts load on request, and a reader can narrow the list by typing a word that appears in a title, summary or author name.

**Why this priority**: The first three stories work with a short list; this keeps them working as the blog grows.

**Independent Test**: With 60 published posts, open the list, load more, and search for a word that appears in exactly one title.

**Acceptance Scenarios**:

1. **Given** more posts than one page, **When** the reader reaches the end of the page, **Then** a "Show more" control loads the next page and the list never repeats or skips a post.
2. **Given** a word typed into the search, **When** it matches, **Then** only matching posts remain, and an honest "nothing matches" message appears when none does.
3. **Given** a failure to load, **When** the reader retries, **Then** the list continues where it was without losing the posts already shown.

---

### Edge Cases

- A post is unpublished or removed while someone has the list open: opening it shows the existing "not available" page; the list never errors on other posts.
- A person signs out or their session ends while reading: they keep reading as a visitor.
- A visitor arrives on a post link and wants the rest: the post page offers "More posts" to the list.
- Search text is only spaces, very long, or contains markup: it is treated as plain text and never executed.
- An account becomes approved while reading: the next screen they go back to is the chat.
- The entry must not appear for anyone on an admin screen.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The sign-in page MUST offer a "Read the blog" entry that opens the public list without a session, and MUST show the three latest published posts (title, author, date) in a strip below the sign-in action. The strip MUST NOT delay or block sign-in, and MUST be absent when empty or failed. The waiting, suspended and revoked screens show the same strip.
- **FR-002**: The waiting screen of a pending account, and the status screens of suspended or revoked accounts, MUST offer the same read-only entry, and "back" from the blog MUST return to that screen.
- **FR-003**: The chat menu and the chat side rail MUST offer "Read the blog" to every approved user, whether or not writing is available to them, separate from "Blog posts".
- **FR-004**: The list and every post page MUST each offer a one-step way back to where the reader started: sign-in, waiting screen or chat (not to the list); a post also offers "More posts", which opens the list. A reader who arrived directly (no previous place) MUST see Sign in, or the chat when they are signed in.
- **FR-005**: The list MUST show only published, visible posts, newest first, each with title, summary, author display name, publication date and cover when present.
- **FR-006**: For a signed-in reader, their own published posts MUST appear in the same single list, marked "Yours" (no All/Mine filter); drafts and unpublished posts MUST NOT appear. The mark MUST come from a separate call available only to signed-in readers that returns just the addresses of their own published posts. The public routes and their responses MUST stay identical for every reader and MUST NOT identify one. Managing their own posts stays in "Blog posts".
- **FR-007**: A signed-in author who can write MUST find a path from the list and from their own post to their workspace.
- **FR-008**: The list MUST load in pages, offer "Show more", and never repeat or skip a post across pages.
- **FR-009**: The list MUST offer a search that narrows posts by title, summary or author name, with an honest empty result.
- **FR-010**: Reading MUST NOT require, create or change any account, chat, memory or file, and MUST NOT reveal to an author or an admin who read a post.
- **FR-015**: Reading a post or the list MUST leave no record visible to the author, to other readers or to an admin tool (no read counts, no reader lists).
- **FR-011**: The feature MUST NOT widen what is public: the same published rows, the same fields, and no email, account, profile or chat identity in any response; the rate limits on public reads MUST also cover paging and search.
- **FR-012**: Entries MUST be keyboard reachable, labelled for screen readers, and usable at 320 px wide; the sign-in page's existing primary action MUST stay the most prominent.
- **FR-013**: When there are no published posts, or the list cannot be loaded, the reader MUST see an honest message and a retry, never a blank page.
- **FR-014**: An admin's tools MUST NOT gain any ability to read posts that are not already public by this feature.

### Key Entities

- **Published post**: unchanged (title, summary, cover, author display name, publication date, address).
- **Reading entry point**: a labelled link shown on sign-in, waiting and status screens, in the chat, and on blog pages; it carries only the place to return to, kept in the address or the browser, never on the server.
- **List page**: a bounded slice of published posts plus a way to ask for the next slice and an optional search text.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A visitor goes from the sign-in page to reading a post in at most 2 actions and 15 seconds, and 1 action when the post is in the strip.
- **SC-002**: A signed-in user goes from the chat to reading someone else's post in at most 2 actions, and back to the chat in 1.
- **SC-003**: With 500 published posts, the first page of the list is readable in under 2 seconds on an ordinary phone connection.
- **SC-004**: In a two-user test, 100% of the posts one user publishes appear for the other and for a visitor, and 0 drafts, emails or account identifiers do.
- **SC-005**: A pending account can read posts and still cannot reach chat, drafts or writing in 100% of checks.
- **SC-006**: Searching for a word present in exactly one post returns exactly that post.

## Assumptions

- The public list and post pages already exist and stay as the destination; this feature adds entries, return paths, paging and search.
- The unauthenticated landing page is the sign-in page; there is no separate marketing page.
- The public blog remains readable and indexable by anyone, as already decided; no "members only" mode is added.
- Search is plain-text matching on title, summary and author name; full-text search of the body is out of scope.
- Likes, comments, following an author and notifications are out of scope.
- Admin takedown of public posts is a separate feature.
- The name "Read the blog" is a working label and may be changed in design.
