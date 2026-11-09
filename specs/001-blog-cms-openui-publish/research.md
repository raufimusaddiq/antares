# Research: Blog CMS with AI-Enriched Publishing

Each item: decision, rationale, alternatives. Items R3 and R4 depend on the deployed Hermes and are
**open until fixtures are recorded** (AGENTS.md: "Read the real Hermes API, do not guess it").

## R1. Editor and stored format

- **Decision**: a ProseMirror-based editor (TipTap-class). The post body is stored as **structured JSON**
  (a document tree), never as HTML. The server validates it against a fixed node/mark allowlist (paragraph,
  heading 2-4, bold, italic, link, bullet/ordered list, blockquote, code, code block, image, divider) and
  renders it itself for readers.
- **Rationale**: JSON removes the HTML-injection class (FR-026); one schema is shared by editor, server
  validator and reader renderer; autosave diffs are small.
- **Alternatives**: Markdown textarea (not "native like Medium"); stored HTML + sanitiser (a sanitiser bug
  becomes a stored XSS on a public surface); hand-built contenteditable (data-loss risk, SC-006).
- **Open**: bundle-size budget for the new dependency; measure in Stage 1 and record in the PR.

## R2. Where AI-added blocks live

- **Decision**: blocks are stored **beside** the author's document, not inside it: each accepted block has an
  anchor (after paragraph/heading N of a specific version) and OpenUI Lang markup. The author's document is
  never rewritten by the check (FR-008).
- **Rationale**: keeps "original" and "enriched" trivially distinguishable, makes "publish my original"
  and per-block reject a deletion, and makes an out-of-date check detectable by comparing version ids.
- **Alternatives**: inline blocks as document nodes (mixes author and AI content; harder to prove the
  original was untouched).

## R3. How a check runs on Hermes (resolved 2026-11-09)

- **Decision**: a check is one Hermes run created through the author's binding, with the draft text as input
  and the blog prompt in the per-run `instructions` field (the same mechanism as the OpenUI prompt for chat).
  Antares tracks it with the existing run states (running, completed, failed, cancelled, timed out,
  disconnected). The check's session is a dedicated, non-chat session that is not listed in the author's chats.
- **Rationale**: Principle II (Hermes does the work); reuses stop/steer/status plumbing; no queue needed.
- **Resolved**: Hermes knows sessions and runs, not Antares chats. A chat is an Antares row that points at a Hermes
  session; the list the author sees is that table. A check therefore creates its own Hermes session
  (`Client.CreateSession`) and run (`Client.CreateRun`, with `Instructions`) and records the ids on `post_checks`;
  with no `chats` row it is never listed. No `kind=blog_check` chat is needed. The tool events of a web-search run
  were recorded from the deployed Hermes (`internal/hermes/testdata/blog_check_*`): per search a `tool.started`
  (`label: "web_search"`, `detail`: the query) and a `tool.completed` (`failed`, `durationMs` null, **`summary` empty**),
  then `message.delta`s and `run.completed` with the full output. Not yet run: a session and run created with no chat
  row, end to end; the first Phase 5 test must do that against the smoke account before relying on it.

## R4. Verifying sources (resolved 2026-11-09: events do not expose the results)

- **Decision**: the reply must carry, per data-bearing block, a list of sources `{url, title, retrievedAt}`.
  Antares (a) accepts only `https` URLs, (b) drops blocks with data but no sources (FR-016), (c) when the run's
  tool events expose the URLs the search tool actually returned, marks a source *verified* if it appears there,
  *unverified* otherwise, and requires explicit author acceptance for blocks with unverified sources.
- **Recorded fact**: the run's events carry the search *query* but not what the search returned: `tool.completed`
  has an empty `summary` for `web_search`. There is nothing to cross-check a cited URL against, so **no source can be
  marked verified**. Every source is shown as unverified, every block with data needs the author's explicit
  acknowledgement (`needs_ack` is always true), and the preview says plainly that the assistant's sources were not
  checked. `verified` stays in the model for a future Hermes that exposes results; until a recording shows it does,
  the server never sets it. This is the fallback this item always named, now the actual behaviour.
- **Rationale**: the model cannot be trusted to cite honestly; the cross-check is only as good as the events
  Hermes exposes, so the plan states the weaker fallback instead of claiming more (Principle III).
- **Alternatives**: Antares fetching each URL itself (a server-side fetch of model-chosen URLs is an SSRF
  surface; rejected); trusting citations without any label (violates SC-008).

## R5. Public read surface

- **Decision**: new routes `GET /b/{slug}` and `GET /b` (HTML shells) and `GET /api/public/blog/posts` and
  `GET /api/public/blog/posts/{slug}` (JSON). One query: post is published, version is the published one,
  author status is ACTIVE. Responses carry no author id, email, profile or chat identity; the byline is the
  author's chosen display name only. Rate limited per IP; `ETag` + short public cache.
- **Rationale**: precedent in the shipped `/s/` chat share (hash-only token, no Hermes, no identity). Unlike
  `/s/`, blog pages are meant to be indexed, so the shell does not send `noindex`.
- **Page metadata (FR-023)**: the Go handler injects `<title>`, description, canonical and Open Graph tags
  (escaped) into the HTML shell and an escaped plain-text copy of the post for crawlers, which React replaces
  on load. No server-side JavaScript rendering.
- **Alternatives**: a separate static-site build (a second moving part; rejected by Principle V).

## R6. Reader-side OpenUI

- **Decision**: public posts use a **read-only variant** of the existing library: display components, tables,
  charts, tabs/accordions/toggles and local sort/filter state are allowed; `@ToAssistant`, forms that submit,
  and any action that calls an Antares API are removed from the allowlist. `@OpenUrl` is allowed only for
  `https` URLs that also appear in the block's sources.
- **Rationale**: FR-020 and PRD 7.4: model markup is presentation, never an action path. Validation runs on
  the server at check time, again at publish, and the reader renderer validates once more before drawing
  (defence in depth); anything that fails becomes the plain fallback (FR-021).
- **Alternatives**: iframe sandbox (extra surface, CSP `frame-src` relaxation); rejected.

## R7. Concurrency and autosave

- **Decision**: autosave sends the draft with the base version number; the server accepts it only if the base
  is the latest (compare-and-set). A stale base returns a conflict and the editor shows both versions
  (spec edge case on two tabs). Local copy is kept in the browser until the server confirms (SC-006).
- **Alternatives**: last-write-wins (silently loses text, rejected).

## R8. Suspension

- **Decision**: no stored "hidden" flag. The public query joins the author's current status, so suspend hides
  and reinstate restores with no state to drift (Q3 answer). The admin gains no new read power; there is no
  separate admin takedown in v1.

## R9. Images

- **Decision**: a post image belongs to its post and author; before publish it is served only to the author
  through an authenticated route; at publish it becomes readable through the public route under the post slug
  and is removed from public view at unpublish. Types limited to PNG/JPEG/WebP/GIF, size limited, content
  type sniffed server-side, re-served with `nosniff`.
- **Alternatives**: reuse chat `assets` table directly (couples blog images to chat ownership and retention;
  new table is simpler to reason about for the public exception).
