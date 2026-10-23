# Issue #85 verification

Scope: PRD §24.2 and the scroll/keyboard parts of §22.2 (UXP-015).
Existing palette, typography, surfaces, sidebar strip, focus styles and reduced-motion
rules are preserved. No dependency was added.

The list now compares the reader's position with the previous viewport height, so
a shrinking composer/keyboard viewport retains bottom following. The jump control
also updates when a render arrives before the browser's scroll event. Scrolling up
retains the existing message anchor; Jump to latest resumes following.

Alt+Shift+M focuses Message outside input, textarea, select and contenteditable
elements. It ignores IME composition, already-handled events and extra Ctrl/Meta
modifiers. A modified shortcut avoids taking over ordinary character keys. Its
description is on the composer, which is also reachable through ordinary Tab order.

The phone header wraps its existing controls instead of overflowing. The composer
does not shrink, long message words wrap, the transcript can shrink, and the shell
uses dynamic viewport height plus CSS `env(safe-area-inset-*)` padding. The viewport
tag retains the exact Phase 1 shell contract; it does not request `viewport-fit`
or `interactive-widget` behavior. Keyboard viewport resizing remains browser-dependent.

## Regression evidence

With the final tests and the original `Chat.tsx` / `VirtualList.tsx` from `3ebf0a8`,
the targeted suite produces three failures: shortcut focus, the missing jump
control before a scroll event, and following after viewport shrink (800 instead
of 950). Restoring the implementation passes all four tests. The full frontend
suite passes 225 tests across 27 files.

Commands run from `web/`:

```sh
npm ci
npm run typecheck
npm test -- src/ScrollBehavior.test.tsx src/Keyboard.test.tsx
npm test
npm run build
npm run dev -- --host 127.0.0.1 --port 5185
```

Commands run from the repository root:

```sh
PLAYWRIGHT_MODULE=/tmp/c05-browser-tools/node_modules/playwright-core/index.mjs \
CHROMIUM_EXECUTABLE=/home/ubuntu/.cache/ms-playwright/chromium-1223/chrome-linux64/chrome \
node docs/design/t85/verify.mjs
go build ./...
go vet ./...
go test ./internal/webui ./internal/server
git diff --check
```

The browser harness uses the already-installed browser tooling, not a project
dependency. It runs the real React app against intercepted local API fixtures and
dispatches assistant delta events through an EventSource fixture. It asserts
hold/follow/jump behavior, shortcut focus, typing protection, Tab access, page
overflow and composer bounds. A wait for bottom alignment lets browser resize
and measurement settle before streaming assertions.

Browser: headless Chromium **148.0.7778.96**, CSS viewport pixels, reduced motion.
Screenshots were opened and inspected:

- [Phone, dark, 390 × 844](phone.png): scrolled up during a stream, jump available,
  composer focused, all header actions visible.
- [Desktop, dark, 1440 × 1000](desktop.png): same state, sidebar and composer fit.
- [Phone, light, shortened viewport, 390 × 500](phone-light-short.png): Tab-focused
  composer remains inside the viewport.

Additional overflow/composer assertions pass at widths 320, 640, 768, 1024 and
1920. The notification panel also fits at 390px. Baseline phone overflow was
511px at a 390px viewport; after the fix it is 390px.

## Scoped antislop gate

- Hard gate PASS: inspected screenshots and browser assertions show fitting controls,
  visible composer focus and working jump/shortcut/Tab paths; fixture text is labeled.
- Purpose gate PASS: wrapping prevents clipping, dynamic height keeps the composer
  available, and previous viewport dimensions preserve the reader's scroll choice.
- Identity/liveliness PASS: existing chat language retained, ENERGY 1 / RHYTHM 1 /
  MOTION 1; no new visual assets, decoration, animation or theme changes.
- Craft/quality PASS: regression tests, real-browser checks and the production
  bundle build cover the changed behavior; existing state and security flows remain
  outside this UI-only change.

Not built: a drawer redesign, sidebar resizing, shortcut settings, smooth scrolling
or additional hotkeys; none is needed for this ticket's behavior-only deliverable.
No deployment or live Hermes checks. Physical phone keyboards, Safari and Firefox
were not tested; browser support for keyboard viewport resizing varies.

## PR #94 fix round 1

Restored exactly `<meta name="viewport" content="width=device-width,initial-scale=1" />`
and rebuilt the embedded bundle with `cd web && npm ci && npm run build` (exit 0).
`TestPhase1Gate/5` failed before this change and passes afterwards; the gate test
is unchanged. CSS, including `100dvh`, `min-height: 0`, control wrapping and
`env(safe-area-inset-*)` padding, is unchanged. No JavaScript keyboard-viewport
workaround was added; physical keyboard/browser behavior remains unverified.

Round results:

- `go test ./internal/chat -run TestPhase1Gate -v`: PASS, all six subtests.
- `cd web && npm test`: PASS, 225 tests / 27 files.
- `go test -race -count=1 ./...`: FAIL in the existing
  `TestMeasureOpenAndPageBack500TurnChat` timing guard: open p50 339ms and page-back
  p50 449ms exceed 200ms. Every other package passed. No Go source or timing
  threshold was changed.
- The ticket's `verify.mjs`, copied to `/tmp/t85-fix-layout/` to keep the committed
  screenshots intact, passes scroll/follow/jump, shortcut/typing/Tab and layout
  assertions, including 320px, 390px and 1440px. Fresh phone/desktop captures were
  opened and inspected. Chromium 148.0.7778.96.
- `PLAYWRIGHT_CORE=/tmp/t85-fix-browser.cjs ONLY=mobile MOBILE_WIDTH=320 node
  scripts/browser-check.mjs --out /tmp/t85-fix320-final`: 4/5 pass; zero overflow,
  16px composer text, 44px Send target, 550px transcript, no console/CSP errors;
  maximum frame gap 367ms exceeds 250ms.
- The same command with `MOBILE_WIDTH=390` and output `/tmp/t85-fix390-final`:
  4/5 pass; the same layout assertions pass, maximum frame gap 333ms.
- `PLAYWRIGHT_CORE=/tmp/t85-fix-browser.cjs ONLY=desktop node
  scripts/browser-check.mjs --out /tmp/t85-fix-desktop-final`: 5/9 pass. Timing,
  traversal sampling and lazy-chunk assertions fail (the latter sees service-worker
  precaching). No console/CSP/off-origin errors. The local module wrapper selects
  the installed Chromium 148 executable; it does not change checks.
- `git diff --check`: PASS.

The existing browser fixture now serves public files with their actual MIME types:
previously `/sw.js` returned the HTML shell and caused an unsupported-MIME console
error. `MOBILE_WIDTH` selects the requested phone test width. Browser assertions
and timing limits are unchanged. These broader local checks are not claimed green;
their failures remain visible for review.
