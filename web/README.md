# Antares web

React + Vite front end. `npm run typecheck`, `npm test`, `npm run build` (the build writes the
bundle into `internal/webui/dist` and stamps the source hash that `go test ./internal/webui` checks).

## PWA

`public/manifest.webmanifest` installs at `/` with 192/512 B · Rival icons. The manifest uses the dark
launch background; the light/dark `theme-color` media queries in both HTML documents follow the device scheme.
Regenerate the icons with the existing `docs/design/logo/assets.py` script.

The production build stamps `public/sw.js` with the source fingerprint and a list of hashed JS/CSS
assets plus `/offline.html`. The offline stylesheet is hashed during stamping. The stamp script itself
is included in the fingerprint. Development mode does not register a worker.

Only that public list is precached, with anonymous requests. There is no runtime cache write. Every API
request (including admin endpoints) and every foreign-origin request bypasses interception. A root-scoped
worker cannot exclude subpaths from its registration scope; the fetch handler excludes them explicitly.
Navigations use the network and fall back to the separate anonymous offline document on network failure.
No account status, chats, sessions, drafts, settings or uploaded assets are saved for offline access.
Logout, expired sessions and observed suspension send `PURGE_USER_STATE` to all registered workers;
the worker removes unexpected entries from its cache. Server authorization remains the boundary.

A waiting update displays a status notice and a keyboard-accessible reload button. Only that explicit
action activates the update and reloads the requesting tab, including when a reply is running. Other tabs
keep running. The notice and offline shell introduce no animation and use both existing theme colours.
Browsers without service workers keep the normal online app; a first visit requires a network connection.

CSP retains `default-src 'none'`, with exactly `worker-src 'self'` and `manifest-src 'self'` added.
`internal/server/server_test.go` pins the complete policy, including the offline document. The worker and
manifest revalidate; the app document and API responses retain `no-store`.

## Visual contract (#139)

All styling lives in `src/styles.css` (plus `src/media.css` for image attachments, restyled in a later stage of #139). The look is
flat and retro: tinted paper, one saturated action colour, one highlighter colour, a few meaningful pastels, hard offset shadows,
crisp ink outlines **only where a box means something**. It is not glass: there is no blur, no translucent tier, no ambient layer,
no gradient surface. `src/cssContract.test.ts` pins this (see "What is tested").

**Composition.**

- *Wide screen:* a left rail beside the conversation. Brand on top, the conversation list in the middle (numbered tiles, three
  pastels in turn; the open one is a filled pill), the account (address, Menu) at the bottom. The Menu opens an actions popover
  upward. There is no top bar. The header element is `display: contents` here, so its two children, `.brand` and `.account`, are
  grid items of `.chat-app` (areas `brand` / `side` / `account` / `pane`); the rail's tinted dotted backdrop is `.chat-app::before`.
- *Phone (max 40rem):* one slim header row (mark, address, Menu), a one-line conversation strip (chips are a 32px pill inside a
  44px hit area), the conversation, an `Options` toggle, a one-row dock (attach, camera, message, send). Options opens the same card panel
  as on a wide screen. The header is a flex row here and the account actions open as a panel over the log.
- *Reading:* the assistant's answer is open text on the page (tag: mono caps and a small star tile). The user's message is the
  headline: large, tight, with a marker stroke behind its first line, no box. Code is an ink block, tables are two ink rules, a
  badge is a tilted sticker.
- *Boxed, on purpose:* the composer dock (2px outline, hard shadow), generated-UI (OpenUI) cards, notices (status marigold, alert
  blush), popovers and panels, the starting card (`.hero`, the empty conversation) and the status-screen account card.
- *Note above the history* ("Start of the conversation", "Scroll up for older messages") is the first row of the log, **in the flow**
  (never sticky, fixed or absolute), so it cannot cover text. `scripts/browser-check.mjs` (`ONLY=desktop-layout`) tests every text
  node in the log against it at every scroll position, with no exemption.

**Tokens** (`:root`, redefined under `prefers-color-scheme: dark` with the same names): `--canvas` `--rail` `--card` `--ink`
`--ink-2` `--hair`; `--cobalt` / `--cobalt-ink` (send, sign-in, the live-run bar); `--mark` / `--mark-ink` (the highlighter,
stickers, hover, status notices); `--peri` `--mint` `--blush` (panels and tiles, by meaning); `--code`; `--focus` / `--focus-halo`;
`--r-sm|md|lg|pill`; `--hard` / `--hard-sm` (the offset shadow). Light: canvas `#f0f2f7`, ink `#11142b`, cobalt `#2b50ff`,
marigold `#ffc93c`. Dark: canvas `#0d1024`, ink `#eef0ff`, cobalt `#7c93ff`. Every text pair is at least 4.5:1 in both themes
(`cssContract.test.ts` computes them). Type: `--font-ui` and `--font-display` are Space Grotesk Variable (self-hosted); `--font-mono`
is the system monospace (tags, labels, table heads, code). No new font file was added. In dark, the marker is a translucent block
behind light text (`--marker-ink`), because dark text above a stripe would vanish on the dark page.

**Icons.** 16 small stroke icons, painted with a CSS mask in `currentColor` from `public/icons/*.svg`, switched on by a
`data-icon` attribute on the button (`[data-icon="plus"]` and so on). They are decorative: the accessible name is always the
button's own text. On a phone and in a narrow window the attach and camera labels are visually hidden (`.btn-label`) and stay the
names. **The CSP is `img-src 'self'`, so every `url()` in the stylesheet is a same-origin file under `/icons` or `/fonts`; a
`data:` URI is blocked in production and the contract test rejects one.** (A mask of an SVG that works in a CSP-less preview
does not work in the shipped page.)

**The chat's extras (#139 stage 2):** at every width only `Options`, the model chip and the usage chip sit above the input. Options
opens one card (`--card`, ink outline, small hard shadow) holding usage details and limits (its disclosure is "Usage details and
limits", never a second copy of the chip's figure), Visualize, voice (Dictate and Record side by side, in a group named "Voice"),
templates and the draft-policy note as a footnote. Helper text there is the UI face at 13px. No control has a dashed border:
disabled is a solid, faded outline.

**Capability reasons** (Regenerate / Edit / Rewind, which the backend cannot do) are muted small mono actions with no underline,
in the same row and size as the action that works (Ask again / Edit as new message, in ink with a hover fill). The reason
is always in the document and is the button's `aria-describedby`; a **press** (tap or click) shows it in the flow under the controls
(and keyboard focus or a mouse hover does too), and Escape or a press elsewhere puts it away. A press never performs an action: the
button stays `aria-disabled`. iOS Safari does not focus a tapped button and has no hover, which is why a press, not `:focus` or
`:hover`, is the contract.

Accessibility fallbacks reassign tokens at the end of the file; the selectors do not change:

- `prefers-contrast: more`: `--hair` and `--ink-2` become `--ink`, thicker underlines.
- `prefers-reduced-motion: reduce`: transitions and the streaming bar animation stop.
- Focus is a two-tone ring (`--focus` outline, `--focus-halo` outer ring) on every surface. The CSP forbids inline `style`
  attributes, so state is expressed with classes and `data-*`.

**What is tested.** `src/cssContract.test.ts` (flat surfaces, CSP-safe assets, tokens in both themes, contrast),
`src/TurnControls.test.tsx` (tap shows the reason), `src/ShellMarkup.test.tsx` (the markup the grid relies on), and in a real browser
`scripts/browser-check.mjs`: `ONLY=mobile-layout` (phone), `ONLY=desktop-layout` (rail, hit-tests, overlaps, popover, the note and a
shown reason against every text node), `ONLY=starting-card`, `ONLY=images` (attach, retry, remove, send, and a touch tap on a
reason). 

## Brand, type and social card

- **Mark:** `src/brand/AntaresMark.tsx` (B · Rival), generated from `docs/design/logo/b-rival.svg`, `currentColor`, `size` prop.
  It sits on a marigold tile in the brand row.
- **Assets:** `public/` holds the favicon set, `og.png` (1200x630) and the fonts. Regenerate the icons and card with
  `python3 docs/design/logo/assets.py` (one-shot, needs the Python `playwright` package; not an app dependency).
- **Type:** `--font-ui` and `--font-display` are Space Grotesk Variable, `--font-mono` is the system monospace. The face is a
  self-hosted latin subset under `public/fonts/` (OFL text alongside), `font-display: swap`, with an Arial-matched fallback face
  (`size-adjust` and ascent/descent overrides measured from the font files). There is no remote font host. The Inter file in
  `public/fonts/` is no longer referenced by the stylesheet or the preload; it is removed with the PWA/brand stage of #139.
- **CSP:** the shell's Content-Security-Policy carries `font-src 'self'` and `media-src blob:` (the latter lets the recorder preview play; `default-src` stays `'none'`) (`internal/server/server.go`, pinned in
  `server_test.go`), so the two woff2 files and the preload are allowed and nothing is fetched from another origin.
- **Bundle guard:** `scripts/stamp.mjs` hashes `src/` and `public/` (and `internal/webui/webui_test.go` mirrors both), so
  editing a font, icon or the OG card without rebuilding fails `go test ./internal/webui`.
- **Deployment origin:** `index.html` hardcodes `https://antares.investdx.biz.id/` for the canonical link and the
  Open Graph / Twitter URLs, while the backend takes its origin from `ANTARES_PUBLIC_ORIGIN`. A static host file is fine for
  a single deployment, but an operator running Antares elsewhere must change them (canonical, `og:url`, `og:image`, `twitter:image`); the OG card image itself contains no URL.
