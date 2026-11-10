# Validation: Discover and Read Other People's Blog Posts

Run on 2026-11-10 against the build in this branch. Only what was actually run is claimed.

## Automated

| Check | Result |
|-------|--------|
| `go test ./...` | all 17 packages ok |
| Go: `limit` honoured and bounded, `q` over title, summary and author, `%` `_` `\` plain, blank and over-long (81) search, markup as text, paging never repeats or skips | `internal/blog/public_test.go` (3 new tests), pass |
| Go: the public list, a post and `/b` answer byte-identically for a signed-in reader and a visitor; reading writes no row in any table | `TestPublicListIsTheSameForEveryCallerAndReadingWritesNothing`, pass |
| Go: a pending, suspended or rejected reader gets 401/403 from the author list and can still read the public post | `TestAReaderWhoIsNotApprovedStillCannotUseTheAuthorRoutes`, pass |
| Go: no admin route names posts, readers or reads (FR-014, FR-015) | `TestNoAdminRouteNamesPostsReadersOrReads`, pass; the existing route-inventory and admin-blind tests are unchanged and pass |
| `npm test` | 867 tests, 100 files. The first run had one timeout in `PreviewScreen.test.tsx` while Go tests, a build and vitest ran together; that file passes alone (15/15) |
| Web: strip (3 cards, absent on empty, failure and 429), a Read the blog button on sign-in, waiting, refused and suspended and not on a broken workspace, reader header (visitor, pending, suspended, approved), writer's workspace link, Yours (published only, never drafts), Show more with a repeated slug, search and Clear, page failure with retry | `web/src/blog/Discovery.test.tsx` (25), pass |
| Web: chat rail and menu entries, with and without writing, no extra rail row | `web/src/ChatBlogEntries.test.tsx` (4), pass |
| Browser `blog-discovery` (new): sign-in, waiting, chat (with and without writing) and the public list at 320, 390, 820 and 1280 px, light and dark, plus sign-in and waiting on short and landscape screens (390x520, 320x420, 568x320, 1024x600) with an unbroken 50-character title and author name | 56/56; every page fits the device, the top is not cut off, the last button can be scrolled to, every card is inside; the only console error is the visitor's one 401 from `/api/me` |
| Browser, existing views re-run | mobile-layout 101/101, desktop-layout 50/50 (earlier run, before the late CSS edits; mobile-layout re-run after), blog-public 8/8, blog-editor 34/34, share-menu 30/30, share-preview 6/6, story-card 18/18, images 20/20 |
| `TestBundleIsNotStale` | pass, bundle rebuilt |

## Success criteria

- **SC-003** (500 published posts, first page under 2 s): measured server-side with 500 posts by 50 authors, worst of 20 requests: list 2.8 ms, `?limit=3` 0.2 ms, search 2.1 ms, `?page=25` 1.7 ms. Network time on a phone was not measured. The throwaway timing test was deleted (AGENTS.md: no measuring code).
- **SC-004, SC-005, SC-006**: covered by the Go and web tests above (published posts of one user appear for another and for a visitor, no draft, email or identifier in the list; a pending account reads and gets no author route; a unique word returns exactly that post).
- **SC-001 and SC-002** (actions and seconds): the paths are one link and one click in the browser check; no stopwatch run with a person.

## Findings while building

- The status screens' width was `100% - 2rem` with a `9vw` left margin, which overran a 390 px phone by 3 px once a full-width card was on the page. Width now leaves the same room on the right. (Pre-existing; the old screens had no content wide enough to show it.)
- The phone's chat rail is a strip and hides the Blog posts button (the menu has it); the new rail row is hidden there too, and the phone reaches Read the blog from the Menu.
- The public pages ask `/api/me` once, shared between the header and the list through one provider; the first version asked twice.
- `T030a`: the public blog pages (`/b`, `/b/{slug}`) in production show flat paper, not the dot grid: the dots come from `body:not(.chat):not(.admin)`, and `.blog-public` paints its own `background: var(--canvas)` over it. The design agrees (flat), so nothing changed.

- Read the blog touched the last latest-post card: `.screen a.btn` (more specific) set a 0.25rem margin that beat the button's own top margin. The spacing is now the container's (`.blog-entry` gap); found by the owner on the sign-in page.
- An unbroken long title or author name stretched the card column (sign-in strip and public list) and pushed the page sideways by up to 94 px; cards now wrap such text (`min-width: 0`, `overflow-wrap`). The Hermes Review's blocker (a Show more page in flight landing in a newer search) is fixed with an epoch and an abort, with a test.

## Motion (design-motion-principles, Emil first, Jakub second)

Weighting: a productivity tool, so restraint. The sign-in strip and list cards ease in once (opacity and an 8 px rise, 220 ms, stagger 45 ms, at most nine steps), only when they first appear and never while a search is typed; cards press (2 px down, shadow to 1 px, 60 ms) and lift 1 px on hover (120 ms), like the app's other hard-shadow controls. Menu rows, buttons, search results and every keyboard path have no motion. Only `transform` and `opacity`; all of it is inside `prefers-reduced-motion: no-preference`, and the existing global reduced-motion rule also zeroes it.

## Not driven

- A production run (visitor, pending account through the smoke fixtures, approved user): done at deploy time (T034).
- A real phone, a screen reader, and a person timing SC-001/SC-002.
- Dark mode was checked by the browser view and by eye on screenshots; contrast was not measured with a tool.
