# Antares web

React + Vite front end. `npm run typecheck`, `npm test`, `npm run build` (the build writes the
bundle into `internal/webui/dist` and stamps the source hash that `go test ./internal/webui` checks).

## Liquid Glass styling

All styling lives in `src/styles.css`. The material is CSS only: `backdrop-filter` blur and saturate,
a translucent fill, a hairline border that is brighter on its top edge, an inset top highlight and a
soft lift shadow. It is not refraction; CSS cannot do that.

Tokens (set under `:root`, redefined for `prefers-color-scheme: light` with the same names):

| Group | Tokens |
| --- | --- |
| Background | `--bg-base`, `--bg-glow-a`, `--bg-glow-b` |
| Glass | `--glass-fill`, `--glass-fill-strong`, `--glass-border`, `--glass-border-top`, `--glass-highlight`, `--lift`, `--blur`, `--saturate` |
| Solid surfaces | `--solid`, `--solid-raised`, `--solid-border` |
| Text and accent | `--text`, `--text-2`, `--accent`, `--accent-ink`, `--focus`, `--focus-halo`, `--warn-*` |
| Shape | `--r-sm`, `--r-md`, `--r-lg`, `--r-pill`, `--gap` |

Glass is on the top bar, sidebar, composer, user turns, buttons and the list status pills. Assistant
turns, code blocks and generated-UI cards are solid surfaces so body text never sits on blur.

Accessibility fallbacks, each at the end of the file:

- `prefers-reduced-transparency: reduce`: every glass surface becomes a solid surface, no blur.
- `prefers-contrast: more`: solid surfaces, 2px full-contrast borders, no shadows, secondary text promoted to primary.
- `prefers-reduced-motion: reduce`: transitions and the streaming progress bar animation stop.
- `@supports not (backdrop-filter)`: solid surfaces.

Focus uses a two-tone ring (`--focus` outline, `--focus-halo` outer ring) so it shows on glass, on
solid surfaces and on the accent button. There is no icon package: icons are inline SVG with
`currentColor` strokes. The CSP forbids inline `style` attributes, so state is expressed with classes.
