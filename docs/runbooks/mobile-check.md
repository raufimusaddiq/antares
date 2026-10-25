# Runbook — mobile layout check (#138)

`web/scripts/mobile-check.mjs` drives the real chat UI in Chromium with a mobile device profile, signed in to a scratch Antares
(fake IdP and Hermes, fabricated accounts, loopback only: [`capacity.md`](capacity.md) has the setup). It refuses a non-loopback `BASE`.

```sh
node web/scripts/mobile-check.mjs <state.json> --out <private dir>
# env: PLAYWRIGHT_CORE=<playwright-core>, CHROME=<chromium binary>, BASE, ORIGIN
```

At 320, 375, 390 and 430 CSS px it opens a chat, scrolls history, types a multiline message with the viewport shrunk to
approximate the keyboard, sends and waits for the reply, opens the account menu, search and options, attaches and removes an image,
switches chats and applies a bottom safe-area inset. Search is also focused and the viewport then shrunk by the keyboard: the
input and Close search must stay visible and focused, the page must not scroll, and the composer must return after closing.
The multiline draft is typed with Shift+Enter (Enter sends). The send must produce a NEW, non-empty reply that the server has
completed and the page shows as completed (no streaming, reconnecting, failed, cancelled, partial or alert); a timeout fails.

Negative proof that a missing reply fails the check (exit 0 means the fault was caught): `--fault drop-send` (the send request is
aborted) and `--fault stuck-run` (the scratch Hermes holds the run past the timeout; needs the loopback fake at `UPSTREAM`,
default `http://127.0.0.1:18081`). `--widths 390,375` limits the widths. A fault run leaves a pending or held run on the scratch server: recreate the scratch fixture before the next normal run. It fails (exit 1) on page horizontal overflow, a page that scrolls instead of
the log, a log under its share of the screen (40% closed, 35% with the keyboard up, 20% and at least three lines on a viewport of 512px or less) or whose newest message is clipped when scrolled to its end, a top-row label (Menu, New chat, Search, the open chat) cut off at an edge, a composer outside the viewport, a control under 44 CSS px, or overlapping controls.

What it does **not** prove: this is an emulator. The on-screen keyboard is a smaller viewport, not the real iOS/Android keyboard;
browser-chrome collapse and the real notch/home-indicator are not exercised (the inset is Chromium's override). The scratch Hermes
has no image catalogue, so the capability probe is answered in the browser to lay out Attach/Take photo; a send with an image is
not exercised. Those need a physical phone. Screenshots from real accounts are private: never attach them to GitHub.

## The CI-runnable layout check

`ONLY=mobile-layout node scripts/browser-check.mjs` (job `mobile-layout` in `.github/workflows/ci.yml`) runs the same measurements
and verdicts (`web/scripts/mobile-layout.mjs`) against the mock backend, so it needs no scratch server: 320/375/390/430 portrait
and a phone on its side (568x320), keyboard closed and open, the account menu, and a focused search under the keyboard. It also
requires New chat, Search, Menu, Options and the composer's Attach image / Take photo / Send to be reachable on screen in every
state (a control that is `display: none` fails). `CHROME` selects the browser; CI uses a pinned chrome-headless-shell and
playwright-core. The rest of `browser-check.mjs` (500-turn scroll, prepend, OpenUI) is the Phase 1 gate and is timing-sensitive;
CI does not run it.

Short viewports (the keyboard is up, or the phone is on its side) keep every control: spacing shrinks, the composer's buttons share
one row, an attached image becomes one compact row, and with a search open the message area steps aside until Close search.

The keyboard is approximated by a shorter viewport: 45% of the height, 300px at most (375x667 becomes 375x367). A phone on its side
(568x320) is checked with the keyboard closed only.

Also required in every state: after scrolling up, `Jump to latest` is on screen (and returns to the newest turn); at the top, the note
above the history ("Scroll up for older messages", with its Retry on an error) is visible. `ONLY=mobile-budget` serves a reached token
limit: the Options drawer opens once, says so, and its toggle closes it again so the conversation grows back.

Taps, not just rectangles: every control outside the conversation is hit-tested at the centre of its visible part (something sitting on
top fails it), and with the account menu open, in both the scratch-server and the CI check, a real mouse click is sent to every menu
button (Notifications, Export Markdown, Export PDF, Admin, Sign out) with a capture listener that records which control it reached and
stops it (nothing runs, Sign out included), keyboard closed and open. The header needs its own `z-index` because its backdrop filter
makes it a stacking context.

## Desktop layout, the note above the history, capability reasons and images (#139)

All four run in CI against the mock backend in `scripts/browser-check.mjs` (same `CHROME`, `PLAYWRIGHT_CORE`), in the job
`mobile layout (real browser)`:

```sh
ONLY=desktop-layout node scripts/browser-check.mjs --out <dir>   # 1280x800, 1024x700, 700x700, 641x800
ONLY=starting-card  node scripts/browser-check.mjs --out <dir>   # the empty-conversation card, desktop and phone
ONLY=images         node scripts/browser-check.mjs --out <dir>   # attach / retry / remove / send, desktop and phone, plus a touch tap
```

`desktop-layout` fails on: horizontal overflow or a scrolling page; a control under 24px (WCAG 2.2 target size; closed `<details>`
content is not rendered, so it is not counted); a control covered by another element (hit-test of its centre); overlapping
controls; the brand / conversation list / account overlapping, or the pane not beside the rail; New chat, Menu, Attach image or
Send not on screen; a composer dock outside the viewport; **the note above the history sitting on message text or being
`sticky`/`fixed`/`absolute`** (every text node in the log is tested against the note at the newest turn, a third of the way up, mid
history, near the top and at the top of the loaded history: there is no exemption list); the Menu popover not opening, an action in it
not reachable, or Escape not closing it; a pressed capability reason not showing, covering any text, or Escape not putting it away.

`images` drives the real attach input with synthetic files against a mock that implements `POST /api/assets`, `DELETE /api/assets/{id}`,
`POST /api/chats/{id}/messages` (recording the body and `Idempotency-Key`) and a completing run stream. It proves: a failed upload shows
Retry and Remove and blocks Send; removing it clears the block; two images become ready; Remove tells the server and keeps the other;
Send posts the text with exactly the remaining asset id; the composer and strip empty and the reply shows as completed (not partial, failed
or cancelled); the phone layout verdicts hold with images attached; and on the phone a **touch tap** (no hover, no focus) shows the
capability reason without covering text. It also reports every CSP / console message: the only ones allowed are the deliberate 415 and the
**known, pre-existing** `img-src 'self'` block of the attached-image `blob:` thumbnail (see the PR for #139); anything else fails.

`DIST=<dir>` runs the same checks against another build, e.g. the parent commit's `internal/webui/dist`, which is how the note-on-text
check was shown to fail before the fix (`Paragraph 4 of answer 49` under the note).

What none of this proves: a real iOS/Android browser, keyboard, safe areas, or a camera; the mock has no model. Say "mock browser", not "device".

### Reproducing a slow runner (`MOCK_DELAY_MS`)

The `images` check reads state the mock holds (uploads registered, deletes received, sends recorded) while the page runs ahead of requests still
on their way. A slow CI runner exposes any assumption about that ordering, a fast machine hides it (main CI went red on the merge of #141 while
the identical tree had passed on the PR head). `MOCK_DELAY_MS=<ms>` adds that latency to the mock's upload, delete and send endpoints, and makes
the first file's upload the slower one so two concurrent uploads register in the opposite order:

```sh
MOCK_DELAY_MS=150 ONLY=images node scripts/browser-check.mjs --out <dir>   # also try 400
```

Rules the check follows, so it stays deterministic: files are distinct (the mock identifies an upload by the file name appended after the PNG's
last byte), the test never assumes which upload registers first, server-side facts are awaited by polling the mock (not read straight after the
page changes), and each send uses a text no earlier run produced (the mock keeps the turns of the desktop pass, so a fixed text is already on the
phone page's history). Unset, the delay is 0 and CI is unchanged.
