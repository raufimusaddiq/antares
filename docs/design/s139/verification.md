# #139 stage 1 — verification (tokens, real shell markup, rail, composer, turns)

Owner direction: the approved C2 preview (see the thread on issue #139). This is the **first of several PRs**; the owner still has to accept
the visual result of every changed state. Nothing here claims device validation: every browser check is a **mock browser** (Chromium
against the mock backend in `scripts/browser-check.mjs`, synthetic account, no model).

## Before / after (authenticated, synthetic account, same content)

`before/` is the parent commit `9959e53`'s bundle, `after/` is this PR's bundle, both rendered by the same script
(`web/` mock, 1280x800 and 390x844 @2x, light and dark). States: chat (newest turn), top (the note above the history), attached (an
image attached), reason (a capability reason shown by a press), menu (account actions), empty (starting card), error (history
unavailable notice), pending (account awaiting approval), and on a phone options (the Options drawer).
`before/` has no `desktop-*-menu` (the desktop Menu toggle did not exist), no `*-reason` that is visible after a press, and its `top`
state shows the pinned note over text (that is the defect fixed here). 34 files each; JPEG q80 to keep the repository small.

Not restyled in this PR (later stages of #139, shown before their PR): image thumbnails/strip and lightbox (`media.css`), model picker,
usage panel, templates manager, search results list, notifications panel, OpenUI forms/charts, admin pages, dialogs, offline page, PWA
colours and icons, `theme-color`, the unused Inter font file.

## What was run (outputs in `logs/`)

The four #139 browser checks were run on **both** a current Chromium 148 (`logs/browser-check.txt`) and the **CI-pinned chrome-headless-shell 138.0.7204.94 with playwright-core 1.54.2** (`logs/browser-check-chromium-138-ci-pin.txt`): mobile-layout 79/79, desktop-layout 48/48, starting-card 2/2, images 17/17 on both. (Round 1 had images 15/17 on 138: it words the blocked-thumbnail console line differently and ends it with a newline; see "Review round 1" below.)

| check | result |
|---|---|
| `npm run typecheck` | clean |
| `npx vitest run` | 50 files, **547 tests passed** (`logs/vitest-and-typecheck.txt`); new: `cssContract.test.ts` (34), `TurnControls.test.tsx` (4), `ShellMarkup.test.tsx` (3), `scripts/known-csp.test.mjs` (26) |
| `go build ./...`, `go vet ./...`, `go test ./...` | exit 0 (tail in `logs/go-test-tail.txt`); `internal/webui` recomputes the source hash of the rebuilt bundle |
| `ONLY=mobile-layout` (320/375/390/430 + 568x320, keyboard open/closed, Menu real clicks, search under the keyboard, token-limit drawer) | **79/79** |
| `ONLY=desktop-layout` (1280x800, 1024x700, 700x700, 641x800; rail, hit-tests, overlaps, 24px targets, popover, the note and a shown reason against every text node) | **48/48** |
| `ONLY=starting-card` (desktop, phone) | **2/2** |
| `ONLY=images` (failed upload -> Retry/Remove/blocked Send; two images; Remove -> server delete; Send posts exactly the remaining id + idempotency key; reply shown completed; phone layout verdicts with images attached; touch tap shows a reason) | **17/17** |
| `ONLY=mobile`, `ONLY=rich`, `ONLY=drafts`, `ONLY=desktop` (scroll-back etc.) | 84/84, 3/3, 4/4, 9/9 |

The Phase-1 gate `ONLY=prepend` cannot run with the Playwright in the local scratch toolchain (`page.waitForFunction` is blocked by the
page's CSP): it fails identically on the parent commit's bundle, so it is not a regression of this PR; CI does not run it.

### The checks were shown to fail (not only to pass)

* Occlusion: `logs/desktop-layout-on-parent-build-fails.txt` is `ONLY=desktop-layout` run with `DIST=` the **parent commit's** bundle:
  `the note above the history sits on message text: Paragraph 4 of answer 49` (and "is sticky: it must be in the flow").
* `cssContract.test.ts` fails if `.vstatus` becomes `sticky`, and if a `data:` URL appears in the stylesheet (both tried and reverted).
* The first draft of the stylesheet failed `mobile-layout` (a 113px message field at 320px; fixed) and `desktop-layout` (a composer that
  collapsed at 641px; targets under 24px; fixed) before they went green.

## Findings worth the owner's attention

1. **Pre-existing bug found by the new image test (not fixed here, not caused by #139):** the attached-image thumbnail is a `blob:` URL and
   the page's CSP is `img-src 'self'`, so the browser blocks it ("Loading the image 'blob:...' violates ... img-src 'self'"). The file name, size,
   Retry and Remove still work; only the preview picture is missing. Fixing it means adding `blob:` to `img-src` in `internal/server/server.go`
   (and its pinned test), a security-policy change that belongs in its own reviewed change. The image check allows exactly that message and fails on
   any other.
2. The CSP forced a design constraint worth knowing: the C2 preview painted icons from `data:` URIs, which the production CSP blocks. The implementation
   uses same-origin files under `public/icons/` and a test rejects `data:` URLs.
3. The Start-of-conversation note was `position: sticky` and sat on top of message text on a wide screen (and the pinned "Jump to latest" is a floating
   control by design). The note is now the first row of the log, in the flow.

## Limits

No physical device, no real keyboard / safe-area / camera, no real model reply (the mock completes a run), no Google sign-in. #138's
physical-device check is still open. The desktop shell relies on `display: contents` for the header (documented in `web/README.md`).

## Review round 1

CI (Chromium 138) failed `images` 15/17: the known blob: thumbnail exception matched only the current wording ("Loading the image ... violates ...").
138 logs `Refused to load the image 'blob:ORIGIN/ID' because it violates the following Content Security Policy directive: "img-src 'self'".` followed by a
newline. The matcher is now `web/scripts/known-csp.mjs`: both exact wordings (the blob URL must be the page's own origin with a UUID id, the directive must be
`img-src 'self'`, the trailing newline is allowed) and the structured `securitypolicyviolation` forms (`img-src`/`img-src-elem` + `blob` or the page's blob URL).
`web/scripts/known-csp.test.mjs` (runs in `npm test`) proves both wordings pass and that other origins, non-blob and `data:` images, other directives
(`default-src`, `media-src`), other resource types (script, style, frame), trailing text and unrelated errors are all still reported. Production CSP is unchanged.
