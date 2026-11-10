---

description: "Task list for Discover and Read Other People's Blog Posts"
---

# Tasks: Discover and Read Other People's Blog Posts

**Input**: Design documents from `specs/003-blog-discovery/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ui-contract.md, quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour.

**Organization**: Design first (Phase 1, in figcraft, owner-approved before any code), then the server change, then one phase per user story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 (visitor), US2 (non-approved accounts), US3 (signed-in user), US4 (find posts) from spec.md
- Paths are from the repository root. Web code is in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design (`docs/design/antares.figcraft`, page "Blog discovery"): read its variables and styles and match them in `web/src/styles.css`. Where code and design disagree, say so in the PR and fix the wrong side (AGENTS.md "Design comes first").
- Rebuild `internal/webui/dist` with any web change (`TestBundleIsNotStale` enforces it). Browser code stays inside the allowlist in `internal/webui/webui_test.go`.
- No new unauthenticated route; `auth.PublicBlogRoutes` and its kept test stay unchanged. Public responses must be identical for every caller (FR-011).
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push.

---

## Phase 1: Design in figcraft (blocks all code)

**Purpose**: every screen, state and flow is drawn in `docs/design/antares.figcraft` on a new page **Blog discovery**, using the existing variables and the "Blog" / "Blog share" frames; the owner approves before Phase 2.

- [x] T001 Read the existing frames (page "Blog", page "Blog share", the login/pending screens, chat menu and rail) with figcraft `inspect_document` and `screenshot`; create page **Blog discovery** in `docs/design/antares.figcraft`
- [x] T002 [US1] Draw sign-in at phone 390 and desktop 1280: with the "Latest posts" strip (3 clickable cards: title, author, date) above the "Read the blog" link, and without a strip (empty or failed); sign-in action stays most prominent (FR-001, FR-012)
- [x] T003 [US2] Draw the pending, suspended and revoked status screens with the latest-posts strip (clickable cards) and the "Read the blog" link, phone and desktop
- [x] T004 [US3] Draw the chat account menu with "Read the blog" beside "Blog posts" and the side rail with a book-icon button beside the "Blog posts" pill (no extra row, so the chat history is not pushed down; menu open on phone, rail on desktop), including a user without writing (only "Read the blog")
- [x] T005 [US1] Draw the public list header and post header in both reader states: anonymous ("Sign in with Google") and signed in ("Back to Antares"), plus "More posts" on a post; phone and desktop
- [x] T006 [US3] [US4] Draw the public list: signed out; signed in with a "Yours" label on one card; search box with clear button; results count; "No posts match"; "Nothing published yet"; error with Retry; "Show more" with the loading state
- [x] T007 Screenshot every frame in light and dark, compare with neighbouring screens (spacing, type, 44 px targets, 320 px width), fix, and ask the owner to approve (one comment round expected, `comment.list`); save with `save_file` and an explicit path Done: owner approved all light and dark frames (page "Blog discovery", dark frames 9000 below the light ones).
- [x] T008 Update `specs/003-blog-discovery/contracts/ui-contract.md` and `docs/design/README.md` (new "Blog discovery" frames) to match the approved design Done: the contract lists the components (secondary pill, quiet pill, latest-post card, dot grid) and every drawn frame.

**Checkpoint**: owner approved the design (T007, T008 done); Phase 2 may start.

---

## Phase 2: Foundational (server and shared web pieces)

**Purpose**: the pieces every story uses. No story starts before this.

- [x] T009 [P] Write failing Go tests in `internal/blog/public_test.go`: `limit` (1–20 honoured, missing/0/21/"x" → 20), `q` (matches title, summary, author name; case-insensitive; `%`, `_`, `\` literal; spaces collapsed; ≤ 80 characters, longer → empty list not an error; blank → no filter), stable ordering across pages, approved-only rows, unpublished never returned, and the response byte-identical with and without a session cookie (also for `GET /api/public/blog/posts/{slug}` and the `/b` and `/b/{slug}` pages), and that reading writes nothing: row counts of every table and any admin read model are unchanged after list and post reads (FR-015)
- [x] T010 Implement `limit` and `q` in `internal/blog/public.go` (`PublicList(ctx, page, limit, q)` and the handler): `AND (v.title LIKE ? ESCAPE '\' OR v.summary LIKE ? ESCAPE '\' OR u.display_name LIKE ? ESCAPE '\')` appended to the same statement as `publicWhere`; response shape and `Cache-Control` unchanged; `routes_public_test.go` untouched and green
- [x] T011 [P] Extend `web/src/blog/blogApi.ts`: `listPublic({ page, limit, q }, signal)` and `listMine(signal)` (published slugs from `GET /api/blog/posts`, any failure → empty); add vitest cases in `web/src/blog/Blog.test.tsx` for the query string and for failure → empty
- [x] T012 [P] Add `web/src/blog/readerSession.ts`: `useReader()` returning `"loading" | "anonymous" | "account"` (+ `active` flag) from `loadSession` in `web/src/api.ts`, no storage written; test in `web/src/blog/Reader.test.tsx` (anonymous, each account state, network failure → anonymous)

**Checkpoint**: server accepts `limit`/`q` and the reader hook exists; stories can proceed.

---

## Phase 3: User Story 1 - A visitor browses the blog from the landing page (Priority: P1) 🎯 MVP

**Goal**: sign-in page offers the strip and the link; public pages offer Sign in.

**Independent Test**: signed out, from sign-in open a post and return, on phone and desktop, with no account.

- [x] T013 [P] [US1] Write failing tests in `web/src/blog/LatestPosts.test.tsx`: three items with title/author/date linking to `/b/{slug}`; absent on empty, on error (including 429) and while loading; never throws
- [x] T014 [P] [US1] Write failing tests in `web/src/Screen.test.tsx`: the login and login-failed views show the strip and a "Read the blog" link to `/b` below the sign-in action, and the sign-in action is first in tab order
- [x] T015 [US1] Create `web/src/blog/LatestPosts.tsx` (fetches `listPublic({limit: 3})` after mount, renders only on a non-empty result) and style it in `web/src/styles.css` per the approved frames Design: card = `card` fill, 1.5 px ink border, hard 3 px shadow, radius 14, title 16 px bold, "author · date" mono 11 px muted; the whole card is one link named by the post title; keep it in `web/src/blog/LatestPosts.tsx` and style with existing tokens only (see contracts/ui-contract.md "Components").
- [x] T016 [US1] Add the "Read the blog" link and the strip to every account and login view in `web/src/Screen.tsx` (not the unreachable and setting-up states); `web/src/styles.css` for the link Design: "Read the blog" is the cobalt filled pill (the app's primary button style, `cobalt` fill and `cobalt-ink` text) with a book icon, so it is not read as a fourth card (owner choice; the sign-in action stays above it and must stay the first control in tab order) after the strip (never a text link); on waiting, suspended and revoked screens "Sign out" becomes a quiet pill (`.quiet`); the strip sits below the sign-in action and the privacy note; the dot grid stays; frames: Sign in, Sign in (strip absent), Waiting, phone versions, light and dark.
- [x] T017 [US1] In `web/src/blog/Public.tsx` use `useReader()` in `Frame` so an anonymous reader keeps "Sign in with Google"; add "More posts" (link to `/b`) on a post page; tests in `web/src/blog/PublicShare.test.tsx` / `Blog.test.tsx` Design: "More posts" is a secondary pill with a left-arrow icon at the end of the post, not a text link.
- [x] T017a [US1] Change the kept check in `scripts/browser-check.mjs` (blog-public, "Signed out means the page never asks who it is"): a signed-out public page may make exactly one `GET /api/me` (401 expected) and its console error is allowed; any other request or console error still fails; update the comment in `web/src/main.tsx`; do the same in the production check (`$SP/prod/prod-share.mjs`); add a test that proves a second request or a different error still fails
- [x] T018 [US1] Add a browser-check view `blog-entry` (login screen with strip, strip absent when the mock list fails, link opens the list, post opens, widened-viewport check at 320/390/820/1280, light and dark) to `scripts/browser-check.mjs` and its CI step in `.github/workflows/ci.yml` Check each built screen against the approved frames in light and dark (theme set through the app's own appearance setting).

**Checkpoint**: US1 works alone.

---

## Phase 4: User Story 2 - Non-approved accounts can read while they wait (Priority: P1)

**Goal**: pending, suspended and revoked accounts reach the blog and come back to their own screen.

**Independent Test**: sign in as a pending account, read a post, come back to the waiting screen, and confirm chat and writing remain closed.

- [x] T019 [P] [US2] Write failing tests in `web/src/Screen.test.tsx`: pending, suspended and revoked views each show "Read the blog" and the strip of cards (absent when empty or failed)
- [x] T020 [P] [US2] Write failing tests in `web/src/blog/Reader.test.tsx`: for a non-active account the public header shows "Back to Antares" (to `/`), no writing or "Blog posts" link, and `listMine` is not called
- [x] T021 [US2] Make the public `Frame` header show "Back to Antares" for any account (`/`), and the list header too, per approved frames (`web/src/blog/Public.tsx`, `web/src/styles.css`) Design: "Back to Antares" is a secondary pill ("Back" below 30rem), in the header of the list and of every post, for any signed-in account.
- [x] T022 [US2] Add a Go test in `internal/server` (or `internal/auth`) proving `/` for a pending session still serves only the waiting screen, `GET /api/blog/posts` as a pending account answers 401/403 (no widening), and no admin route returns post readers or read counts (FR-014, FR-015); extend `blog-entry` browser view with the pending screen round trip

**Checkpoint**: US1 and US2 work independently.

---

## Phase 5: User Story 3 - A signed-in user reaches the blog from the chat (Priority: P1)

**Goal**: approved users see "Read the blog" in the chat menu and rail, find other people's posts, see "Yours" on their own, and return to the chat.

**Independent Test**: approved user → menu → Read the blog → open another user's post → Back to Antares → chat.

- [x] T023 [P] [US3] Write failing tests in `web/src/Chat.test.tsx`: the menu shows "Read the blog" and the rail shows a labelled icon button for every approved user (a full pill when the user cannot write; no extra rail row otherwise), separate from "Blog posts", and activating it navigates to `/b`
- [x] T024 [P] [US3] Write failing tests in `web/src/blog/Reader.test.tsx`: an active reader's own published slugs get a "Yours" label, drafts never appear, no marks when `listMine` fails, public responses unchanged
- [x] T025 [US3] Add `onReadBlog` to `Chat` in `web/src/Chat.tsx` (menu at the existing "Blog posts" button, and the rail) and pass it from `web/src/App.tsx` (`window.location.assign("/b")`); no entry on admin screens Design: the rail gets a 44 px round book-icon button (aria-label "Read the blog", tooltip) beside the "Blog posts" pill and no extra row; a user without writing has no "Blog posts" pill and gets a full-width "Read the blog" pill in that slot; the account menu (desktop and phone) gets a "Read the blog" row with a book icon after "Blog posts"; frames: Chat menu and rail, same without writing, Phone menu both kinds.
- [x] T026 [US3] In `web/src/blog/Public.tsx` (loading `getCapabilities` for an active reader; link hidden when writing is false or the call fails) mark "Yours" on the list card and show, for an active author with writing, a link to `/blog` from the list and from their own post; styles per frames Design: the "Yours" label is a pink chip in the meta row of the card (ink text).
- [x] T027 [US3] Add the browser-check view `blog-list` signed-in section (menu entry, Yours, back to the chat) and the two-user check in `scripts/browser-check.mjs` Also check the chat rail and the phone account menu for a user with and without writing, in light and dark.

**Checkpoint**: US1–US3 work independently.

---

## Phase 6: User Story 4 - Finding something worth reading (Priority: P2)

**Goal**: the list pages and searches.

**Independent Test**: with 60 posts load more and search for a unique word.

- [x] T028 [P] [US4] Write failing tests in `web/src/blog/PublicList.test.tsx`: "Show more" appears only after a full page and appends without repeating a slug (a post published meanwhile shifts the next page); search applies after a pause or Enter and clears; "No posts match"; a failed page keeps the shown posts and Retry continues; result count announced in a polite status; focus stays on the used control; keyboard order and visible labels on search, Show more and Retry (FR-012)
- [x] T029 [US4] Implement search box, "Show more", empty/error states and the status line in `PublicIndex` (`web/src/blog/Public.tsx`) with styles in `web/src/styles.css`, per the approved frames; search text is rendered as text only (markup in `q` is inert) Design: search box with the result count on the right and a quiet "Clear" pill when text is typed; "Show more" secondary pill that shows a dimmed "Loading more…" while loading; empty copy "No posts match" / "Try another word, or clear the search to see every post."; error with "Try again" keeping shown posts.
- [x] T030 [US4] Extend the `blog-list` browser view with 60 mock posts: paging, search for a unique word, no results, error and Retry, at 320/390/820/1280, light and dark Check each built state against the frames (search results, no results, error, Show more loading) in light and dark.

**Checkpoint**: all stories work.

---

## Phase 7: Polish and cross-cutting

- [x] T030a Check in a real browser whether the public blog pages (`/b`, `/b/{slug}`) show the dot grid in light and dark (the app CSS puts it on every non-chat, non-admin page); the approved design draws them flat, so make the design and the page agree (fix the frames or the CSS) and say which in the PR
- [x] T031 [P] Update `docs/PRD.md` §30 (discovery entries, search, "Yours"), `docs/design/README.md`, `docs/runbooks/blog-check.md` (only if it names entry points), and the comments in `internal/auth/routes.go` that describe the public list
- [x] T032 [P] Add `specs/003-blog-discovery/validation.md` with real results: tests, browser views, a production run (visitor, pending account via the smoke fixtures, approved user), SC-001/SC-002 timings if measured, SC-003 measured with 500 seeded posts (first page under 2 s) or stated as not driven, and what was not driven
- [x] T033 Run full checks: `go test ./...`, `npm test`, `npm run build` (bundle committed), `TestBundleIsNotStale`, all browser views, existing views (`mobile-layout`, `desktop-layout`, `blog-public`) still green
- [ ] T034 Open the PR; address review; merge; deploy with backup; run the production check with synthetic accounts; clean up smoke users and fixtures; update the issue and tick this list

---

## Dependencies and execution order

- Phase 1 (design) blocks everything; T007 needs owner approval.
- Phase 2: T009 → T010; T011 and T012 parallel to them.
- US1 (T013–T018, with T017a) needs Phase 2; US2 (T019–T022) needs T012 and T021 shares `Public.tsx` with T017, so do US1 first; US3 (T023–T027) needs T012 and T021; US4 (T028–T030) needs T010 and T011.
- Polish last.

## Parallel examples

- Phase 2: T009 (Go tests) with T011 and T012 (web).
- US1: T013 with T014; US2: T019 with T020; US3: T023 with T024.

## Implementation strategy

MVP = Phase 1 + Phase 2 + US1. Then US2, US3 (same release, all three are P1), then US4. Ship as one PR per phase group if review size grows; each checkpoint is deployable.
