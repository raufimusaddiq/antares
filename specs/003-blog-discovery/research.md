# Research: Discover and Read Other People's Blog Posts

## R1. Where "back" goes
**Decision**: a plain link to `/`. **Rationale**: App already redirects an anonymous reader to `/login`, a pending one to
`/pending`, a suspended or revoked one to its screen, and an approved one to the chat. That is exactly "where the reader
started" with no stored state. **Alternatives**: remember an origin in the address or storage (more code, can go stale,
breaks on shared links); history.back() (wrong when the reader arrived from a link).
**Cost**: leaving the chat for `/b` is a full page load, so the open chat is reloaded on return (the sign-in and chat state
are server side; unsent text is already kept as a draft).

## R2. Knowing whether the reader is signed in
**Decision**: the public pages call the existing `GET /api/me` once after first paint. Anonymous → keep "Sign in with
Google"; an account in any state → "Back to Antares". **Rationale**: no new route, server-side public routes remain session
free, the first paint is unchanged. **Alternative**: read a cookie client side (not readable; HttpOnly). **Side effect accepted**: an anonymous reader gets a 401 logged in the console; the kept browser check and the production check are updated to allow that one request and error on public pages, and still fail on any other request or error.

## R3. The "Yours" mark
**Decision**: after R2 says ACTIVE, call the existing `GET /api/blog/posts` (author-only, Active-guarded) and mark list
cards whose slug is in the returned published posts. Failure or non-active → no marks. **Rationale**: nothing new on the
server, public responses stay identical for all callers (tested). **Rejected**: personalising the public list (breaks
Principle I and caching); matching by display name (names are not unique).

## R4. Search
**Decision**: optional `q` on `GET /api/public/blog/posts`; the same statement gets `AND (v.title LIKE ? ESCAPE '\' OR v.summary LIKE ? ... OR u.display_name LIKE ?)`
with `%`, `_` and `\` escaped, trimmed, collapsed whitespace, capped at 80 characters (longer → empty result, not an
error), case-insensitive (SQLite LIKE is ASCII-insensitive; non-ASCII matched as typed). **Rationale**: simple,
bounded by the existing page size and `maxPublicPage`, same limiter. Note: a post whose author has no display name is shown as "Author" but is not found by that word. **Rejected**: FTS table (migration, upkeep: over the
footprint for a few hundred posts); client side filter (needs all posts downloaded).

## R5. Paging and "Show more"
**Decision**: reuse `page`; the client shows "Show more" when a page returned a full 20 and appends the next page,
de-duplicating by slug. **Rationale**: the order is `published_at DESC, id` so it is stable; a new post published while
reading shifts later pages by one, so the client drops a repeated slug and never shows one twice. No server change
beyond R4/R6.

## R6. The strip
**Decision**: `GET /api/public/blog/posts?limit=3` (new optional `limit`, 1–20, default 20, anything else ignored to the
default). Rendered by the sign-in screen after mount, title + author + date, no cover (keeps it light and fast), absent on
empty or failure. **Rationale**: one cached read, never blocks sign-in. **Alternative**: reuse page 1 and slice (fetches 20
rows for 3).

## R7. Pending, suspended and revoked screens
**Decision (changed at the owner's request)**: `Screen` renders the same entry link for every view, and the latest-posts strip (clickable cards) on every view except the unreachable and setting-up states, where the strip is skipped. It is a plain link
to `/b`; reading grants nothing and the public pages are session free.

## R8. Chat entries
**Decision**: `Chat` gets `onReadBlog` (always provided), shown in the menu and the rail as "Read the blog" next to
"Blog posts" (writing-gated, unchanged). Admin screens get none (spec edge case).
