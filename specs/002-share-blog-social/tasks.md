---

description: "Task list for Share Blog Posts to Social Media and Stories"
---

# Tasks: Share Blog Posts to Social Media and Stories

**Input**: Design documents from `specs/002-share-blog-social/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ui-contract.md, quickstart.md

**Tests**: Included. AGENTS.md and constitution Principle IV require a failing-then-passing test for every behaviour. Canvas drawing cannot run in the unit-test environment, so the layout is a pure function that is unit tested, and the drawing is checked in a real browser.

**Organization**: Design first (Phase 1, in figcraft), then code built from that design. Front end only: no Go change, no migration, no new route.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1 (share), US2 (story picture), US3 (author preview) from spec.md
- Paths are from the repository root. Web code is in `web/src/`.

## Rules for every implementation task

- Build screens from the figcraft design (`docs/design/antares.figcraft`, page "Blog share"): read its variables and styles, match them in `web/src/styles.css` and in the picture's palette. Where code and design disagree, say so in the PR and fix the wrong side (AGENTS.md "Design comes first").
- Rebuild `internal/webui/dist` with any web change (needs Linux/Docker; `TestBundleIsNotStale` enforces it). Browser code must stay inside the path allowlist in `internal/webui/webui_test.go`.
- No new unauthenticated route, no network request beyond the cover image the post page already shows, no third-party script (FR-013, FR-018, FR-019). `auth.PublicBlogRoutes` stays as it is; its kept test must still pass.
- One ticket, one branch, one PR from `origin/main`; one commit per review round; never rebase or force-push.

---

## Phase 1: Design in figcraft (blocks all code)

**Purpose**: every screen, state and flow of this feature is drawn in `docs/design/antares.figcraft` before code, using the existing `Antares` variables (no new colour without a variable).

- [x] T001 [US2] Design the blog story card (1080 × 1920, warm beige `#e9e4d4`, mint strip, white card, pink mark and bullets, cobalt sticker and bar) on page **Blog share** of `docs/design/antares.figcraft`; owner chose the colours
- [x] T002 [US2] Draw the chat's shared picture beside the blog card for comparison on page **Blog share** (SC-008)
- [x] T003 [US1] Design the Share menu on the desktop post page (Share button in the header; Copy link, WhatsApp, X, Facebook, LinkedIn, Telegram, Email, Story image) on page **Blog share**
- [x] T004 [US2] Design the Story dialog states (ready, saved with no share sheet, failed) and the phone dialog on page **Blog share**
- [x] T005 [US3] Design the Share preview section in the editor's Post settings on page **Blog share**
- [ ] T006 [US1] Draw the missing states on page **Blog share** of `docs/design/antares.figcraft`: the phone Share menu (first entry "Share with your apps", then Copy link and Story image; no named destinations) and update the desktop menu to match FR-002; Share menu "Link copied" confirmation; phone post header with the Share button; Story dialog "preparing"; and the 1080 × 1920 card for each edge case (no cover; no summary; no headings; twelve headings with the extra ones dropped; very long title; right-to-left title; emoji title)
- [ ] T007 Wire the prototype flows on page **Blog share** (Share button to menu, menu "Story image" to dialog, dialog Close to the post, failed "Try again" to ready), run the overlap audit (no stacked or doubled layers), extend the "Blog share" paragraph in `docs/design/README.md` with the new frames, and save the file
- [ ] T008 **Design review gate**: owner reviews the new frames of T006 and T007 in figcraft; fixes applied. No code task below starts until approved. Open the design PR on its own (design file and README only)

**Checkpoint**: design approved and merged; it is now the source of truth for everything below.

---

## Phase 2: Setup

**Purpose**: a safety net before the shared drawing code is touched.

- [x] T009 Add a real-browser check `ONLY=share-image` to `scripts/browser-check.mjs` that draws the chat's "Share image" picture (whole answer and summary) through the real app with the date pinned, prints the SHA-256 of the decoded pixels and fails when `EXPECT_SHARE_IMAGE=hashFull,hashSummary` differs; run it on current `main` first (a pixel hash cannot be committed as a CI gate: it depends on the machine's fonts, so it is a before/after comparison on one machine). The summary picture must be exactly 1080 × 1920. Baseline on main: full `45edc7ca…`, summary `88a70cf1…`

---

## Phase 3: Foundational (blocks all user stories)

**Purpose**: one drawing kit and one dialog that the chat picture and the post picture share.

- [x] T010 Add the failing test first: `web/src/ShareImage.test.tsx` keeps passing unchanged after T011, and the browser baseline of T009 still matches
- [x] T011 Extract the drawing primitives of `web/src/shareImage.ts` (`spaced`, `layout`, `drawLine`, `textUnits`, `pill`, `badge`, `dots`, `wrap`, the `Unit` and `Piece` types, `gap`, `STORY_H = 1920`, `W = 1080`, `PAD = 72`, `SANS`, `MONO`) into `web/src/cardKit.ts`, turning the module constant `C` (colours) into a `Palette` argument; export `CHAT_PALETTE` with today's exact values; `shareImage.ts` imports the kit and passes `CHAT_PALETTE`. Output of the chat picture MUST NOT change (T009 baseline)
- [x] T012 Add `BLOG_PALETTE` to `web/src/cardKit.ts` with exactly: background `#e9e4d4`, card `#ffffff`, ink `#0b0b0b`, strip `#8ef0c0`, mark and diamonds `#ff6fae`, sticker and highlighter bar `#2d4bff` (sticker text `#ffffff`), cover frame `#b3c1ff`
- [x] T013 [P] (Changed in implementation: the chat's dialog is left untouched, and StoryDialog is a separate component built from the same classes; see T027.) Was: generalise `ShareImageDialog` out of `web/src/ShareChat.tsx` into `web/src/ShareImageDialog.tsx`: props for the title ("Share as image" / "Share as a story"), a `render(): Promise<Blob | null>` function, the file name, the note text, the link to copy and the "saved, no share sheet" instruction; behaviour unchanged for chats (preview, `canShare({files})` then `share({files})`, Save, Copy link, failure keeps Copy link); `ShareChat.tsx` uses it; `web/src/Share.test.tsx` and `web/src/ShareImage.test.tsx` pass
- [x] T014 [P] Add `web/src/blog/share/shareLinks.ts`: `canonicalUrl(origin, slug)` returning `origin + "/b/" + slug` with no query or fragment, and `destinations(title, url)` returning the WhatsApp, X, Facebook, LinkedIn, Telegram and Email links (each carries the title and the canonical address and nothing else); test in `web/src/blog/share/shareLinks.test.ts` that the canonical address decoded from every destination equals `canonicalUrl(...)` exactly (FR-003, SC-005)

**Checkpoint**: chat sharing still behaves and looks the same (T009 baseline); the kit and dialog are shared.

---

## Phase 4: User Story 1 - Share a post to a social app or by link (Priority: P1) MVP

**Goal**: any reader can pass a post on from its page (FR-001 to FR-004, FR-016, FR-018).

**Independent Test**: quickstart steps 1 and 2: share from a phone and a desktop, confirm the title and plain canonical link arrive and nothing else is loaded.

### Tests for User Story 1 (write first, must fail)

- [x] T015 [P] [US1] `web/src/blog/share/ShareMenu.test.tsx`: with `navigator.share` present, the menu's first entry "Share with your apps" calls it with `{ title, url }` where `url` is the canonical address, the menu also has Copy link and Story image, and no named destinations are listed; without it, the menu lists WhatsApp, X, Facebook, LinkedIn, Telegram, Email, Copy link and Story image; Copy link writes the canonical address and announces "Link copied" in a polite live region, and falls back to a selectable read-only field when the Clipboard API rejects; cancelling the native sheet (AbortError) shows nothing; Escape and outside click close the menu and return focus to the Share button; each destination opens in a new tab with `rel="noopener noreferrer"`
- [x] T016 [P] [US1] Kept test in `web/src/blog/Blog.test.tsx` (or a new `PublicShare.test.tsx`): the Share control is rendered on a published post page and on no draft, preview or unpublished view (FR-011)

### Implementation for User Story 1

- [x] T017 [US1] `web/src/blog/share/ShareMenu.tsx` built from the T003 frame: Share button, menu with the native-sheet entry, named-destination fallback with `role="menu"`, keyboard operation (arrows, Home/End, Escape), visible focus ring, destinations from `shareLinks.ts`, copy confirmation
- [x] T018 [US1] Mount `ShareMenu` in the post page header of `web/src/blog/Public.tsx` (desktop, tablet and the phone header from T006), with the styles in `web/src/styles.css` matching the design (hard shadow, 2 px ink outline, 14 px radius); no layout shift of the reading column (FR-016)
- [x] T019 [US1] Real-browser check `ONLY=share-menu` in `scripts/browser-check.mjs`: menu opens and closes by keyboard at 390, 820 and 1280 px in light and dark, no horizontal overflow, focus returns, and the only network request is the page's own (nothing third-party, FR-018)

**Checkpoint**: Share works on every published post without any picture.

---

## Phase 5: User Story 2 - Share as a 9:16 story card (Priority: P1)

**Goal**: "Story image" produces an exactly 1080 × 1920 picture of the post in the blog palette and hands it to the device or saves it (FR-005 to FR-010, FR-014, FR-017, FR-020 to FR-022).

**Independent Test**: quickstart steps 3 to 6: phone, desktop, edge-case posts; picture next to a chat picture.

### Tests for User Story 2 (write first, must fail)

- [x] T020 [P] [US2] `web/src/blog/share/postContent.test.ts`: `summaryText(post)` is the author's summary when present, otherwise the opening text cut at a sentence boundary or, failing that, a word boundary, never mid-word; `highlights(doc)` returns the section headings in reading order, at most four, each shortened at a word boundary to at most two lines of the card's width; a post with no headings returns none; every returned word occurs in the post (FR-021, nothing invented); text containing markup characters stays plain text
- [x] T021 [P] [US2] `web/src/blog/share/planPostCard.test.ts` with a stub `measure`: the plan's total height is always exactly 1920 for no summary, no headings, twelve headings, a 20,000-word post, a very long title and no cover; when space runs out parts are dropped in the order of FR-022 (highlights from the last, then the cover, then the end of the summary with an ellipsis) and the title, author, site name and address remain; a right-to-left title gets direction `rtl`; text is broken at word boundaries with `Intl.Segmenter` (a string with no spaces still breaks at a word)
- [x] T022 [P] [US2] `web/src/blog/share/palette.test.ts`: every text colour on its background in `BLOG_PALETTE` (ink on beige, ink on white, ink on mint, white on cobalt, ink on pink) has WCAG contrast of at least 4.5:1, and `BLOG_PALETTE` differs from `CHAT_PALETTE` in background and strip (SC-008)
- [x] T023 [P] [US2] `web/src/blog/share/StoryDialog.test.tsx`: the picture is produced when the dialog opens (so the Send tap is a direct user action); "Share to an app" appears only when `canShare({files})` is true and calls `share({files, title, url})`; otherwise Save image downloads a PNG named for the post and copies the link with the instruction about the link sticker (FR-009); a render failure shows the plain-words message with Copy link still available and the post untouched (FR-017); focus moves in and returns to the trigger; the preview has alternative text with the title and summary

### Implementation for User Story 2

- [x] T024 [US2] `web/src/blog/share/postContent.ts` (pure): `summaryText`, `highlights` per T020, reading the published document JSON the page already holds (data-model.md "ShareCardInput"; quote the constraints: summary "plain text, cut at a sentence or word boundary"; highlights "up to 4; each shortened to one or two lines; none invented")
- [x] T025 [US2] `web/src/blog/share/planPostCard.ts` (pure): from a `ShareCardInput` and a `measure` function, returns the list of units with their heights on the fixed 1920 canvas, stepping the summary type down first and then dropping parts in the FR-022 order; exports the plan type that `postCard.ts` draws
- [x] T026 [US2] `web/src/blog/share/postCard.ts`: `renderPostCard(input): Promise<Blob | null>` drawing the plan with `cardKit.ts` and `BLOG_PALETTE`, exactly as the T001 frame: brand row (badge, "Antares", date pill), tilted "POST" sticker, title headline with the cobalt bar, "BY name" label, white card with the mint dotted strip labelled "SUMMARY", the cover in a thick-outlined frame (same-origin image; if it fails to load use the plain frame, FR-008), the summary, diamond highlights, the short address and "Shared from Antares"; sets `ctx.direction` from the text; returns null where the browser cannot draw (as `renderShareImage` does)
- [x] T027 [US2] `web/src/blog/share/StoryDialog.tsx` using the generalised dialog of T013 with the post card renderer, the title "Share as a story", the note from T004 ("A 9:16 picture of this post: its title, summary and highlights. Nothing is uploaded."), file name `antares-<slug>.png`, and the saved-without-sheet instruction; open it from the menu's "Story image" item in `ShareMenu.tsx` (the same item on every device)
- [x] T028 [US2] Real-browser check `ONLY=story-card` in `scripts/browser-check.mjs`: for the sample posts of quickstart step 6 (long title, no cover, no summary, no headings, twelve headings, 20,000 words, right-to-left title, emoji) the produced PNG decodes to exactly 1080 × 1920, is not blank, and its text lies inside the card (no pixel of the title or summary in the outer 24 px margins); save the PNGs to the scratchpad and compare the main one by eye with the T001 frame (SC-003, SC-008)

**Checkpoint**: Story image works end to end on phone and desktop.

---

## Phase 6: User Story 3 - Authors preview how their post is shared (Priority: P2)

**Goal**: the author sees the story card and link preview for a draft before publishing (FR-015).

**Independent Test**: quickstart step 7 (author preview): edit summary and cover, open Post settings, see the card change; confirm it offers no send or copy.

### Tests for User Story 3 (write first, must fail)

- [x] T029 [P] [US3] `web/src/blog/PostSettings.test.tsx` (extend): the "Share preview" section shows the card made from the draft's current title, summary, headings and cover and the link preview, is labelled "Preview only", has no Share, Save or Copy control, and updates when the fields change; no preview of a post that has no title yet (empty state text)

### Implementation for User Story 3

- [x] T030 [US3] "Share preview" section in `web/src/blog/PostSettings.tsx` built from the T005 frame: the card rendered by `postCard.ts` into an `<img>` from a blob (revoked on change and on unmount), at a size that fits the sheet, with the caption from the frame; the card's date is the draft's last-saved date; debounce rendering while the author types
- [x] T031 [US3] Extend `ONLY=blog-editor` in `scripts/browser-check.mjs`: with Post settings open the preview renders at 390 and 1280 px, in light and dark, without overflowing the sheet

**Checkpoint**: all three stories work independently.

---

## Phase 7: Polish and cross-cutting

- [x] T032 [P] Add a Share paragraph to `docs/PRD.md` §30 (what is shared, that the picture is drawn on the reader's device, the platform limit for Instagram and WhatsApp stories, no new public route)
- [x] T033 [P] Write `specs/002-share-blog-social/validation.md`: quickstart scenarios with results, per-package `vitest --coverage` before and after (AGENTS.md "cutting tests keeps coverage"), and a table of SC-001 to SC-008 with evidence or "not measured"
- [ ] T034 Rebuild `internal/webui/dist` (Linux/Docker), run `go test ./...`, `npm test` and all `ONLY=` browser views (`share-card-baseline`, `share-menu`, `story-card`, `blog-editor`, `blog-public`, `mobile-layout`); confirm `auth.PublicBlogRoutes` and its kept test are untouched
- [ ] T035 Run `/speckit-analyze`; fix findings in the spec files only
- [ ] T036 Deploy (backup first), then verify on production in a real browser: publish a synthetic post, open it signed out, use Share (copy link) and Story image, confirm the PNG is 1080 × 1920 and nothing but the page's own requests were made; unpublish and delete the synthetic post and user; never touch the owner's accounts

---

## Dependencies and order

- Phase 1 (design) blocks everything; T008 is the gate. T006 and T007 may overlap; T006 before T007.
- Phase 2 before Phase 3: the baseline must exist before the refactor (T009 before T011).
- Phase 3 blocks all stories. T011 before T012; T013 and T014 are independent of T011/T012 and of each other.
- US1 needs T014 and T017's menu; US2 needs T011 to T013 and the US1 menu entry (T017) to open it; US3 needs T025/T026 from US2.
- Within a story: tests first (they must fail), then pure code, then components, then the browser check.

## Parallel opportunities

- T013 and T014 together after T009.
- T015 and T016 together; T020, T021, T022 and T023 together (four different test files).
- T024 and T025 are independent of each other; T026 needs both.
- T032 and T033 together.

## Implementation strategy

- **MVP**: Phases 1 to 4 (design, safety net, shared kit, Share). Useful alone: readers can pass a post on.
- **Next**: Phase 5, the story picture (also P1: it is the distinctive part of the request).
- **Then**: Phase 6 (author preview), then Phase 7 and deploy.
