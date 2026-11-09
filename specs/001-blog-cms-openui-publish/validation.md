# Validation record (T074)

Run on 2026-11-10 against `main` after #305 (`d59d0d64`), with the committed bundle. Production runs `d59d0d64`; the last
full smoke (`d8d85d80`, same backend) passed 19/19 with `cleanup=clean`, including a real check on the author's own assistant.

## The quickstart commands

| Command | Result |
|---|---|
| `go test ./internal/blog/... ./internal/server/...` | pass |
| `(cd web && npm test && npm run build)` | 791 tests pass; bundle builds; `go test ./internal/webui/` confirms the committed bundle matches its sources |
| `node scripts/browser-check.mjs` (all of it) | 199/199 checks pass (phone 390 and 320, desktop 1280, light and dark) |

## The fifteen scenarios

Each scenario is covered by named tests that exist and pass; scenarios that need a real Hermes also have the production smoke.

| # | Evidence |
|---|---|
| 1 Write with an image, close, reopen | Go `TestAutosaveIsCompareAndSet`, `TestImagesAreDraftPrivateAndTypeChecked`; web `Autosave.test.ts` (the text is kept until the server confirms it), `Editor.test.tsx` (restores what the browser kept after a reload); browser check `blog-editor` |
| 2 Publish the original; open `/b/{slug}` signed out | Go `TestPublishNeedsTitleAndAddressAndKeepsThePublishedVersion`, `TestPublicShowsOnlyPublishedPostsOfApprovedAuthors`, `TestBlogPagesCarryTheirOwnMetadata`, `internal/server/blogpages_test.go`; browser `blog-public`; smoke `blog.lifecycle` |
| 3 Enrich: preview shows AI-added blocks with sources and dates, original unchanged | Go `TestACheckRunsOnTheAuthorsOwnAssistantAndShowsWhatCameBack`; web `PreviewScreen.test.tsx`; browser `blog-editor` (review screen); smoke `blog.check` (real Hermes) |
| 4 Reject one block, accept the rest | Go `TestBlockDecisionsAndPublishingTheAcceptedOnes`; web `PreviewScreen.test.tsx` (deciding block by block) |
| 5 A block with script, an unknown component or `@ToAssistant` | Go `TestMarkupIsRefused`, `TestEachBadPartIsDroppedOnItsOwnWithAPlainNotice`, `TestNoticesNeverCarryTheAssistantsWords`; web `ReaderBlocks.test.tsx` (the refusal cases) |
| 6 A table with no sources | Go `TestDataWithoutASourceIsDroppedAndNoted` |
| 7 Hermes fails, times out, or the author cancels | Go `TestEveryFailureIsShownAsItselfAndNothingIsPublished`, `TestCancellingStopsTheRunAndALateAnswerChangesNothing`; web `Blog.test.tsx` (every way a check can stop is shown as itself) |
| 8 Edit during a running check | Go `TestEditingWhileItRunsDiscardsTheAnswer`, `TestEditingStopsTheWorkerOfTheCheckItMadeStale`, `TestEditingTheDraftMakesTheCheckOutOfDate` |
| 9 B asks for A's draft, post, check, block and asset | Go `TestAnotherUsersPostIsA404`, `TestAnotherUsersCheckIsA404AndEachUsesTheirOwnAssistant`; smoke `negative.cross_user` |
| 10 Admin suspends A: posts vanish, reinstating restores | Go `TestSuspendingTheAuthorHidesTheirPostsAndReinstatingRestoresThem`, `TestAnAdminCannotReachAnotherAuthorsPostsAndASuspendedAuthorsAreHidden` |
| 11 Admin sees no post or draft content anywhere | `internal/auth` `TestAdminSeesNoPostOrDraftContent` (every admin GET endpoint, before and after a suspension) and `TestEveryAdminGetRouteIsScannedForPostContent` (a new admin route fails until scanned) |
| 12 The unauthenticated routes are the existing set plus the public blog routes | Go `TestOnlyTheDocumentedRoutesAnswerWithoutASession`, `TestEveryAuthenticatedRouteRefusesTheIneligible` |
| 13 Public post at 390 px, light and dark, keyboard only | Browser `blog-public` (390, 320, desktop; Tab reaches an accordion trigger and Enter toggles it; no sideways scroll; hover on a chart makes no request) |
| 14 One block forced to fail rendering | Web `ReaderBoundary.test.tsx`; browser `blog-public` (a block with an action falls back to its plain text) |
| 15 Hermes or web search not configured | Go `TestCapabilitiesAreOnlyWhatTheAssistantConfirms`, `TestACheckIsNotStartedWhenTheAssistantCannotBeConfirmed`, `TestTheCheckModeSwitch`; web `PublishDialog.test.tsx` (checking not available, original is the one choice) |

"Before Stage 2 starts": the fixtures from the deployed Hermes were recorded (T014) and `check-envelope.md` follows the real shape.

## `go test -cover`, before and after

Before is `a25e8189` (the last commit with no Go blog code); after is `main` with this change.

| Package | Before | After | Change |
|---|---|---|---|
| cmd/antares | 38.0% | 36.5% | -1.5 |
| cmd/antares-smoke | 88.6% | 81.5% | -7.1 |
| internal/auth | 82.1% | 81.2% | -0.9 |
| internal/blog | (new) | 85.5% | |
| internal/chat | 86.0% | 85.8% | -0.2 |
| internal/config, hermes, kenari, push, redact, secrets, store, tenant, webui | unchanged | unchanged | 0 |
| internal/server | 91.8% | **67.6% → about 93%** | +1 or so (see below) |
| internal/smoke | 81.3% | 77.5% | -3.8 |

What the numbers said, and what was done:

- **internal/server fell to 67.6%.** The blog's page shell (`blogpages.go`: the title, description and cover written into the page for search engines and link previews) was tested only from `internal/blog`, and Go counts coverage per package. `blogpages_test.go` now tests it where it lives: the index's title, noindex removed, a stranger's markup escaped (a `$1` stays `$1`), the cover only when it is a relative address, one answer for everything a reader may not see (and no lookup for a malformed address), a lookup failure that is a 500 and never says why, one address per page, HEAD without a body, the request allowance, and `setAttr`. Coverage is about 93% (93.2% to 93.5% by machine). Each security test was shown to fail when the behaviour is broken on purpose (escaping removed; the 500 leaking its cause).
- **A real inconsistency came out of that test.** A post's page redirected a trailing slash to its one canonical address, but the index served `/b/` with a canonical of `/b/`: two addresses. `/b/` now redirects to `/b`.
- **cmd/antares-smoke, internal/smoke (-7.1, -3.8):** the smoke tool grew its production-only paths (the real-Hermes check steps, event capture, its own `main`, the error branches that need a live Hermes). They are exercised by every production smoke run and not by unit tests; accepted.
- **cmd/antares, internal/auth, internal/chat (-1.5, -0.9, -0.2):** wiring and a few new lines in each; accepted.
