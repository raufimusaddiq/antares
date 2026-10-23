# Issue #48 verification

PRD §21.1 UXP-001 and §21.2. All checks used a loopback preview of the built bundle through
`server.New(...).WithAssets(webui.FS()).Routes()`, with a fabricated anonymous `/api/me` response.
No production service, account, or Hermes endpoint was used.

## Installability

Lighthouse 11.7.1, Chromium headless, local trustworthy origin `http://127.0.0.1:18448/`:

```text
PWA: 100/100
installable-manifest: 1 (PASS)
  Web app manifest and service worker meet the installability requirements
splash-screen: 1 (PASS)
themed-omnibox: 1 (PASS)
maskable-icon: 1 (PASS)
content-width: 1 (PASS)
viewport: 1 (PASS)
runtimeError: null
Chromium Page.getInstallabilityErrors: {"installabilityErrors":[]}
```

The audit command used a temporary CLI installation, outside project dependencies:

```sh
lighthouse http://127.0.0.1:18448/ --only-categories=pwa \
  --chrome-flags='--headless --no-sandbox' --output=json
```

Lighthouse's manual cross-browser, page-transition and per-page URL checks are unscored. This proof
does not claim Safari/iOS installation or verification of the deployed HTTPS origin.

## Browser proof

- A clean browser installed and claimed the worker without an update notice.
- Cache Storage contained only `/offline.html` and six hashed public JS/CSS assets in this build.
  No API response or user data was present.
- A second worker was served with different bytes and cache version; `registration.update()` put it
  into waiting. The update notice appeared while a marker on the current window remained intact.
- Focusing **Reload to update** and pressing Enter activated the waiting worker and reloaded exactly
  the requesting tab. The marker disappeared and only the new public cache remained.
- Offline navigation to `/chats/private-proof` returned the anonymous offline document. Keyboard
  retry remained anonymous and announced the offline state. Chromium may reopen the public React
  document on retry; it shows an offline notice and unknown account status, with no private chat.
- Reconnecting and retrying returned to sign-in. Both themes, 390px and 1280px viewports, keyboard
  focus, and reduced motion were exercised. No horizontal overflow or page JavaScript errors.

Screenshots:

- [Offline, dark desktop](offline-dark.png)
- [Offline, light mobile](offline-mobile-light.png)
- [Waiting update, dark desktop](update-dark.png)
- [Waiting update, light mobile with keyboard focus](update-mobile-light.png)

## Automated checks

`npm run typecheck`, `npm test` (116 tests), `npm run build`,
`go test ./internal/server ./internal/webui`, `go build ./...`, and `go vet ./...` passed.

The new worker tests exercise API/admin/cross-origin/write bypass, no runtime cache writes,
anonymous precaching, failed/redirected/no-store asset refusal, offline fallback, defensive purge,
explicit activation and old-cache retirement. Client tests cover waiting-worker discovery, cleanup,
explicit reload, and purge on successful logout, expired session and observed suspension.

The pinned CSP test was also run with a Go overlay containing the original `server.go`: it failed
on all four app routes because `worker-src` and `manifest-src` were absent. It passes with this change.

## CSP before and after

Before:

```text
default-src 'none'; script-src 'self'; style-src 'self' 'sha256-vai9tBS1dbqDlLt2vYYQwJl16j1hMfOCO5KhCK8LRBg='; connect-src 'self'; img-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
```

After, also applied to the offline document:

```text
default-src 'none'; script-src 'self'; style-src 'self' 'sha256-vai9tBS1dbqDlLt2vYYQwJl16j1hMfOCO5KhCK8LRBg='; connect-src 'self'; img-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
```

## UI delivery gate

Design read: private chat status surfaces in the existing monochrome Antares language;
ENERGY 1 / RHYTHM 1 / MOTION 1. The heading is the offline focal point; the reload action is the
update focal point. Spacing separates identity, state and recovery action. The contrast-filled action
and rounded control repeat the existing product motif. No extra artwork or motion was introduced.

- R-02 PASS: new UI copy has no em dashes or decorative text.
- R-03 PASS: browser overflow assertions at 390px; mobile screenshots attached.
- R-17/18/36/38 PASS: no statistics, testimonials or readiness claims in the new UI.
- R-23 PASS: owner explicitly requested rendering the existing SVG at 192px and 512px.
- R-24/26/35 PASS: offline retry and update reload clicked, including keyboard activation and reconnect.
- R-25 PASS: new surfaces use opaque light/dark colours with high-contrast text and actions; screenshots inspected.
- R-27 PASS: offline state and pending update are explicit, and failed account refresh remains visible.
- R-28 PASS: no FAQ added.
- R-32 PASS: real button/link controls, visible focus, status announcements; focus shown in mobile update screenshot.
- R-33 PASS: UI and CSS live in source files; stamping only generates versioned distribution assets.
- R-34 PASS: both scheme variants inspected; no theme toggle added.
- R-37 PASS: existing brand direction reused with the dials stated above.
- R-01/04/06/07/08/09/10/12/13/14/19/22 PASS: no new gradients, glow, icons, display fonts, grids,
  arrows, badges, glass, shadows, repeated cards, animations or illustrations. Existing account styling is reused.
- Liveliness PASS: explicit dials, state headline, structural spacing, one contrast action and existing control motif.
- C-1..C-5 and consistency locks PASS: each addition reports real browser state or offers a tested recovery action;
  existing tokens, control shape and copy conventions are reused. Screenshots and automated evidence are above.
- Copy/comment checklist PASS: concise state and action copy; the worker comment explains the scope limitation.

## Limits

No offline private chat, background sync, push notifications or new runtime dependency. First install
requires network access. A root-scoped worker explicitly bypasses API paths; registration scope itself
cannot exclude subpaths. Update activation may replace the worker for other tabs, but those tabs are
never automatically reloaded. An explicit reload can interrupt a running reply, as the notice states.
