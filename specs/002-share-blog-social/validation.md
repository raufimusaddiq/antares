# Validation: Share Blog Posts to Social Media and Stories

Run on 2026-11-10 against the branch build, in chrome-headless-shell 138 (the version CI uses), with the browser checks of
`scripts/browser-check.mjs` driving the built app against its mock backend.

## Quickstart scenarios

| # | Scenario | Result |
|---|---|---|
| 1 | Native share (phone): Share, then "Share with your apps" | Unit-tested (`ShareMenu.test.tsx`: the first entry calls `navigator.share({title, url})` with the canonical address, and no services are listed). A real phone's sheet was not driven. |
| 2 | Fallback share (desktop): menu, Copy link, destinations | `ONLY=share-menu`: 24/24 (phone, tablet, desktop, light and dark: opens inside the viewport, no sideways scroll, "Link copied", the copied text is the canonical address, Escape returns focus to Share, no console error, no off-origin request). |
| 3 | Story image on a phone, sent to Instagram Stories and WhatsApp Status | Not driven on a real device. The dialog's logic (draw on open, `canShare({files})`, `share({files, title, url})`, Save, Copy link, failure) is unit-tested (`StoryDialog.test.tsx`). **Open: the owner should try it once on a phone.** |
| 4 | Story image on a desktop: Save and link sticker note | Unit-tested (Save saves the file, copies the link and shows the instruction). |
| 5 | Same product, different colours | Looked at beside a chat picture (design file page "Blog share", and the rendered PNGs). Layout, mark, cards, labels and type match; the colours differ. |
| 6 | Edge cases | `ONLY=story-card`: 18/18 over nine posts (ordinary, no cover, no summary, no headings, twelve headings, 20,000 words, very long title, right-to-left, emoji): each PNG decodes to exactly 1080 x 1920, is not blank and has no ink in the outer 24 px. |
| 7 | Privacy | `PublicShare.test.tsx`: Share only on a published post page; none on a missing or failed post or the index; the editor never mounts it. The browser checks fail on any request to another origin. |
| 8 | Automated | `npm test` 837 passed (98 files); `tsc --noEmit` clean; `go test ./internal/webui ./internal/auth` pass (no change to `auth.PublicBlogRoutes`); `ONLY=share-image` shows the chat picture byte-identical (decoded pixels) before and after moving its drawing code. |

## The chat picture did not change

`ONLY=share-image` prints the SHA-256 of the decoded pixels of the chat's "Share image" (whole answer and summary), date
pinned. Before: full `45edc7ca4cb4e452474e065cfc3ebf862912f05d2e182f938c3d55b1b1bf3257`, summary
`88a70cf1951d2f76c9718230a2f9b908fd97e530458f94789cf2ced9366cfc23`. After the move to `cardKit.ts`: the same two hashes
(`EXPECT_SHARE_IMAGE=...` passes). The summary picture is exactly 1080 x 1920.

## Success criteria

| SC | Evidence |
|---|---|
| SC-001 | Not timed on a phone. The menu is two taps from the post (Share, Story image) and the picture is drawn on open. |
| SC-002 | Not measured on a real connection. The nine posts above, drawn in headless Chrome, finish well inside the check's wait. |
| SC-003 | `ONLY=story-card` (nine posts, 100% exactly 1080 x 1920 with no clipped ink at the edge) and `planPostCard.test.ts`. Overlap inside the card is covered by the pure layout tests, not by pixel inspection. |
| SC-004 | No route or storage was added; the picture is made from data the page already holds. `PublicShare.test.tsx`. |
| SC-005 | `shareLinks.test.ts`: every destination carries exactly the canonical address. |
| SC-006 | Not applicable: nothing is served by the server, so there is nothing to flood. |
| SC-007 | `ONLY=share-menu` (keyboard: Escape and focus return) and `ShareMenu.test.tsx`; contrast of the picture's colours in `palette.test.ts`. A screen reader was not driven. |
| SC-008 | Looked at beside the chat picture; `palette.test.ts` (differs from the chat palette, AA contrast). |

## Coverage (web, `vitest --coverage`, all of `web/src`)

| | lines | statements | branches | functions | tests |
|---|---|---|---|---|---|
| `main` before | 79.7% | 77.2% | 73.8% | 77.0% | 791 |
| this branch | 78.9% | 76.8% | 74.3% | 77.8% | 838 |

Branches and functions rose; lines and statements fell by under one point. The cause is the canvas drawing code
(`cardKit.ts`, `postCard.ts`, and the part of `shareImage.ts` that draws), about 600 lines that a unit-test environment
cannot execute: they are covered by the real-browser checks instead (`share-image`: the chat picture pixel for pixel;
`story-card`: nine posts). The pure parts are covered: `planPostCard.ts` 99%, `postContent.ts` 97%, `shareLinks.ts` 100%,
`ShareMenu.tsx` 96%, `StoryDialog.tsx` 95%, `RowMenu.tsx` 97%. `PostSettings.tsx` rose from 6% to 75% and `Public.tsx` from
28% to 88%. No test was removed. (`@vitest/coverage-v8` is not a dependency of `web/`; it was installed with `--no-save`.)

## Production (2026-11-10, release `a724e6bb`)

Deployed after a backup; `/api/version` and `/readyz` answered. A real-browser run against `https://antares.musaddiqr.com`
under the smoke fixtures (synthetic accounts, approved and removed by the tool): account A published a synthetic post, a
signed-out browser opened it, used Share, Copy link and Story image on a desktop (1280 px) and a phone (390 px), and the post
was deleted. Results: Copy link put exactly `https://antares.musaddiqr.com/b/<slug>` on the clipboard; the story picture was
exactly 1080 x 1920 and drawn, on both. The one failed check is not from this feature: Cloudflare injects its analytics beacon
(`static.cloudflareinsights.com`) at the edge and the page's CSP (then `script-src 'self'`) refused it, which my check counted as a
console error and an off-origin request; Antares itself loaded nothing from another origin. The fixtures were removed
(`cleanup=clean`), the post is gone (404), and the four real accounts are untouched.

## Analysis against the finished code

Every requirement FR-001 to FR-022 maps to code and a test above. Left open, not hidden: no real phone was driven (Share to an
app, Instagram Stories, WhatsApp Status), no screen reader was driven, and SC-001, SC-002 and SC-007 were not measured. FR-018
(no third-party script) holds for Antares; the Cloudflare beacon is the host's. The spec needed no change.

## Things found on the way

- Copy link first lost keyboard focus when its label changed to "Link copied" (React re-created the element): fixed by a
  stable item identity, with a test.
- On a phone the public post and the editor widen the layout viewport (about 736 px on a 390 px phone) when they hold the
  browser check's hostile content (a very long link and code line). This was already so on `main`. Fixed afterwards in #314: the
  app's grid item (`#root`) could not shrink below its content; the checks now compare the layout viewport with the device width.
  The same stricter check then found two faults of the Share work itself at 320 px (the header was too wide and the menu left the
  screen), also fixed.
