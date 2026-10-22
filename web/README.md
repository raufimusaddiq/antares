# Antares web

React + Vite front end. `npm run typecheck`, `npm test`, `npm run build` (the build writes the
bundle into `internal/webui/dist` and stamps the source hash that `go test ./internal/webui` checks).

## Liquid Glass styling

All styling lives in `src/styles.css`. The material is CSS only (`backdrop-filter` blur + saturate,
translucent fills, a 1px specular top edge, layered shadows). It is not refraction; CSS cannot do that.
It is also not one pane of glass: there are four tiers, each with its own fill, edge, specular
highlight, blur and shadow tokens.

| Tier | Tokens | Used for | Why |
| --- | --- | --- | --- |
| chrome | `--chrome-*` | top bar, sidebar (a chip strip on narrow screens) | Thinnest and most transparent; it frames the page and should recede. Almost no shadow. |
| raised | `--raised-*`, `--control-shadow` | composer, user turns, buttons, account card | Medium fill, strongest specular edge, contact + mid + wide shadow and an outer rim: these are the things that hover. |
| rest | `--rest-*` | assistant turns | A resting surface the text sits on: lighter and less opaque than raised, one contact shadow, no lift. Its text column is capped at 68ch. Contrast is measured against the real backdrop (the ambient fields and rings) rather than assumed; if a change breaks AA, opacity goes back here. |
| solid | `--solid-*` | code blocks, generated-UI cards | Near-opaque on purpose: dense or structured content never depends on what is behind it. |

The specular edge is a 1px `linear-gradient` background layer on the top edge that fades out towards the
corners. Elevation is physical: a tight contact shadow, a mid shadow and a wide soft one. Only the
composer carries the full stack, because it is the one pane that sits above the transcript.

**Ambient layer, both themes.** Glass needs something behind it to refract: a translucent surface over a
uniform background is indistinguishable from an opaque one. `body::before` holds four large, low-alpha neutral
fields (`--amb-hi|side|lo|mid`) and `body::after` two soft rings. They are fixed, inert and static. Each field is
an eased multi-stop falloff sized in `vmax`, so its edge always lies beyond the viewport.

- Dark: `--bg-base: #08090b` (near-black, cool cast) lit by grey and blue-grey fields.
- Light: `--bg-base: #eceff3` (off-white) with soft blue-grey fields and one white highlight, low in contrast so it
  reads as paper with depth. Light glass fills are real translucency (0.3 chrome, 0.46 raised, 0.4 rest), not
  near-opaque, so the blur does visible work.

The palette is monochrome after `target-mindful.png`. The one accent is the reference's play-button white
(near-black in light) on the send action, the sign-in action and the live-run bar. Notices are inverted
(light on dark, dark on light) instead of coloured.

Other tokens, under `:root` and redefined for `prefers-color-scheme: light` with the same names:
`--text`, `--text-2`, `--accent` (send action and live-run bar only), `--primary` (sign-in action),
`--focus`, `--focus-halo`, `--warn-*`, `--r-sm|md|lg|pill`, `--gap`.

Type is the system sans (the CSP forbids web fonts). Headings are large, light and tight-tracked
(`h1`, the empty-conversation line, the wordmark); body text stays at 16px/1.55.

Accessibility fallbacks reassign tokens at the end of the file, the selectors do not change:

- `prefers-reduced-transparency: reduce`: every tier becomes a solid surface, no blur or sheen.
- `prefers-contrast: more`: solid surfaces, 2px full-contrast borders, no shadows, no ambient colour, secondary text promoted to primary.
- `prefers-reduced-motion: reduce`: transitions and the streaming bar animation stop.
- `@supports not (backdrop-filter)`: solid surfaces.

Focus uses a two-tone ring (`--focus` outline, `--focus-halo` outer ring) so it shows on every tier and
on the accent button. Icons are inline SVG with `currentColor` strokes. The CSP forbids inline `style`
attributes, so state is expressed with classes.

## Brand, type and social card

- **Mark:** `src/brand/AntaresMark.tsx` (B · Rival), generated from `docs/design/logo/b-rival.svg`, `currentColor`, `size` prop.
  The top bar lifts it 1.2px so its centre matches the wordmark's cap-centre (measured at 3x).
- **Assets:** `public/` holds the favicon set, `og.png` (1200x630) and the fonts. Regenerate the icons and card with
  `python3 docs/design/logo/assets.py` (one-shot, needs the Python `playwright` package; not an app dependency).
- **Type:** `--font-ui` is Inter Variable, `--font-display` is Space Grotesk Variable (status headline and empty-state note
  only), `--font-mono` is the system monospace. Both faces are self-hosted latin subsets under `public/fonts/` (OFL texts
  alongside), `font-display: swap`, each with an Arial-matched fallback face (`size-adjust` and ascent/descent overrides
  measured from the font files). There is no remote font host.
- **CSP:** the shell's Content-Security-Policy has no `font-src`, and `default-src 'none'` therefore blocks the woff2 files.
  Until `internal/server/server.go` adds `font-src 'self'`, the app renders in the metric-matched fallback. See the PR.
