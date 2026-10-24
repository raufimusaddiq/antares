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
the log, a log under 40% of the screen, a composer outside the viewport, a control under 44 CSS px, or overlapping controls.

What it does **not** prove: this is an emulator. The on-screen keyboard is a smaller viewport, not the real iOS/Android keyboard;
browser-chrome collapse and the real notch/home-indicator are not exercised (the inset is Chromium's override). The scratch Hermes
has no image catalogue, so the capability probe is answered in the browser to lay out Attach/Take photo; a send with an image is
not exercised. Those need a physical phone. Screenshots from real accounts are private: never attach them to GitHub.
