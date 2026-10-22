# Antares web

React + Vite front end. `npm run typecheck`, `npm test`, `npm run build` (the build writes the
bundle into `internal/webui/dist` and stamps the source hash that `go test ./internal/webui` checks).

## Liquid Glass styling

All styling lives in `src/styles.css`. The material is CSS only (`backdrop-filter` blur + saturate,
translucent fills, a 1px specular top edge, layered shadows). It is not refraction; CSS cannot do that.
It is also not one pane of glass: there are three tiers, each with its own fill, edge, specular
highlight, blur and shadow tokens.

| Tier | Tokens | Used for | Why |
| --- | --- | --- | --- |
| chrome | `--chrome-*` | top bar, sidebar | Thinnest and most transparent; it frames the page and should recede. Almost no shadow. |
| raised | `--raised-*`, `--control-shadow` | composer, user turns, buttons, status pills, account card | Medium fill, strongest specular edge, contact + mid + wide shadow and an outer rim: these are the things that hover. |
| solid | `--solid-*` | assistant turns, code, generated-UI cards | Near-opaque on purpose. **This is a design decision:** body copy is read for minutes, so it never depends on what is behind it. Glass here would trade legibility for decoration. |

The specular edge is a 1px `linear-gradient` background layer on the top edge that fades out towards the
corners. Elevation is physical: a tight contact shadow, a mid shadow and a wide soft one. Only the
composer carries the full stack, because it is the one pane that sits above the transcript.

**Ambient layer.** `body::before` holds four large, low-alpha colour fields (amber, plum, rust, sand)
and `body::after` two soft ring shapes. They are fixed, inert and static, and they are what the glass
blurs: the same tier looks plum-warm near the plum field and amber-warm near the amber one. Tokens: `--bg-base`, `--amb-*`.

Other tokens, under `:root` and redefined for `prefers-color-scheme: light` with the same names:
`--text`, `--text-2`, `--accent` (amber; send action and live-run bar only), `--primary` (sign-in action),
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
