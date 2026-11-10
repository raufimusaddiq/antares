# Implementation Plan: Discover and Read Other People's Blog Posts

**Branch**: `003-blog-discovery` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/003-blog-discovery/spec.md`

## Summary

The public list (`/b`) and post pages (`/b/{slug}`) already exist; nothing leads to them. This feature adds the doors and
makes the list scale:

1. **Entries**: a "Read the blog" link on the sign-in page (with a strip of the 3 latest posts as clickable cards, also on the status screens), on the pending /
   suspended / revoked status screens, and in the chat menu and side rail.
2. **Return path** (see R2 for the one extra call this needs): `/` already routes every reader to the right place (anonymous → sign-in, pending → waiting screen,
   approved → chat), so "back" is simply a link to `/`. No state is stored. The public pages learn whether the reader is
   signed in from the existing `GET /api/me` and show "Back to Antares" instead of "Sign in".
3. **"Yours" mark**: the signed-in list asks the existing author-only `GET /api/blog/posts` (Active-guarded) and marks
   the slugs it returns. A pending account gets 401/403 there and simply sees no marks.
4. **Paging and search**: the existing public list route already takes `?page=` (20 per page); it gains two optional query
   parameters, `limit` (1–20, for the 3-post strip) and `q` (search text). **No new route.**

**No new route, no new table, no new dependency, no AI.** `auth.PublicRoutes` / `auth.PublicBlogRoutes` stay as they are
and their kept test is unchanged (constitution Principle I). See [research.md](research.md) for the decisions.

Per AGENTS.md ("Design comes first"), the sign-in strip, the status-screen entry, the chat entries, and the list with
search / Show more / Yours / Back are designed in `docs/design/antares.figcraft` before code (Stage 0 below).

## Technical Context

**Language/Version**: Go (server, one handler/query change), TypeScript + React 19 + Vite (web).

**Primary Dependencies**: none new.

**Storage**: SQLite, read only; no migration. `q` uses `LIKE ... ESCAPE` on the already-joined title, summary, author name.

**Testing**: Go tests for `PublicList` (`limit`, `q`, escaping, bounds, stable paging, approved-only, no new field) and the
kept route-list test; vitest for the strip, the entries, the list (search, Show more, retry), the Yours mark, the reader
state; real-browser checks in `scripts/browser-check.mjs` for sign-in, status screens, chat menu and list at 320/390/820/1280
in both themes.

**Target Platform**: the existing embedded web bundle (`internal/webui/dist`, rebuilt with the change).

**Project Type**: web application, existing.

**Performance Goals**: first list page and the 3-post strip each one read, cached 60 s as today; the strip never blocks
sign-in (rendered after the page, absent on failure); SC-003 first page < 2 s at 500 posts.

**Constraints**: constitution I–V; CSP unchanged; no cookie or storage written for reading; public responses identical
for every reader.

**Known side effect**: a signed-out reader's `GET /api/me` answers 401, which the browser logs as a console error. The kept check "signed out never asks who it is" (`scripts/browser-check.mjs`, blog-public) and the production share check are changed to expect exactly that one request and that one error, and nothing else (task T017a). Sign-in page views draw on the visitor's public-read allowance; a 429 makes the strip absent, nothing more.

**Scale/Scope**: one query parameter pair, one hook, one strip component, two entry links, list controls.

## Constitution Check

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I. Default deny, admin blind | Pass, no exception added | No route added to `auth.PublicBlogRoutes`; `q` and `limit` are query parameters of an existing route, the row filter `publicWhere` is untouched and the new filter is added to the same statement. The response shape is unchanged, so no email/account/chat identity appears. The "Yours" mark uses the author-only route under the reader's own session; the public route never learns who reads. Reads are not recorded, so neither authors nor admins can see them (FR-010, FR-014). The existing limiter covers `q` and paging. |
| II. Front end, not an agent | Pass | No Hermes call. |
| III. Honest, capability-driven UI | Pass | The strip is absent when empty or failed; entries show only what works; empty and error states are stated. |
| IV. Tests guard behaviour | Pass | Tests listed above; the kept route-list guard in `internal/blog/routes_public_test.go` unchanged; a new test proves the list response is byte-identical for an anonymous and a signed-in caller. |
| V. Small footprint | Pass | No dependency, route, table or container. |

Re-checked after design: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/003-blog-discovery/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/ui-contract.md
```

### Source code

```text
internal/blog/public.go          # PublicList(ctx, page, limit, q); handler parses limit and q; bounds; LIKE escaping
internal/blog/public_test.go     # limit, q, escaping, bounds, paging stability, identical for any caller
web/src/blog/blogApi.ts          # listPublic({page, limit, q}); listMine() slugs
web/src/blog/readerSession.ts    # useReader(): "anonymous" | "account" (via loadSession), for Frame and Yours
web/src/blog/Public.tsx          # Frame: Back to Antares / Sign in; PublicIndex: search, Show more, Yours; post: More posts
web/src/blog/LatestPosts.tsx     # the sign-in strip (3 posts), absent on empty/failed
web/src/Screen.tsx               # "Read the blog" link on every Screen; strip on login screens
web/src/Chat.tsx                 # "Read the blog" in the menu and the rail (separate from "Blog posts")
web/src/styles.css               # strip, search, entries
scripts/browser-check.mjs        # views: blog-entry (login/pending/chat), blog-list (search/paging/yours)
docs/design/antares.figcraft     # Stage 0 frames, page "Blog discovery"
docs/PRD.md, docs/design/README.md, docs/runbooks/blog-check.md (only if it names the entry points)
```

**Structure Decision**: extend the existing public blog modules in place; add two small web files. No Go package added.

## Stage 0: Design first, in figcraft (gate)

Owner instruction: every plan starts with design tasks done in figcraft. No code task starts until Stage 0 is approved
by the owner.

- **Where**: `docs/design/antares.figcraft`, new page "Blog discovery" (via the figcraft MCP; save with an explicit path).
  Reuse the existing Antares components, tokens and the blog frames ("Blog" and "Blog share" pages) so nothing is redrawn.
- **Method**: read the existing frames, draw the new ones, screenshot each, compare with the neighbouring screens, then
  iterate with the owner (one round of comments is expected).
- **Frames, phone 390 and desktop 1280** (both themes where colours matter):
  1. Sign-in with the "Latest posts" strip and the "Read the blog" link; sign-in without a strip (empty or failed).
  2. Pending and suspended/revoked status screens with the entry.
  3. Chat menu and chat side rail with "Read the blog" beside "Blog posts".
  4. Public list: signed out; signed in with a "Yours" card; searching; no results; error with Retry; Show more.
  5. Post header: anonymous ("Sign in") vs signed in ("Back to Antares"), with "More posts".
- **Exit**: owner approves the frames; the contract in [contracts/ui-contract.md](contracts/ui-contract.md) and
  `docs/design/README.md` are updated to match; only then do server and web tasks begin.
- **Tasks**: `/speckit-tasks` must put these as the first phase (before Setup), and the browser-check task compares the
  built screens with the frames.

## Complexity Tracking

None. (The one thing that could have needed an exception, a session-aware list, is avoided by using the author-only route.)
