# Runbook — OpenUI renderer foundation (slice 5, PRD §7)

Code: `web/src/openui/*`, `web/src/MessageBody.tsx`. Package: `@openuidev/react-lang@1.0.1` (exact pin), `zod@4`.

## What is built

- **Detection** (`segments.ts`, main bundle, no OpenUI import): an assistant message is split into Markdown and
  OpenUI segments. An OpenUI segment is a fenced block whose info string is `openui` or `openui-lang`. Anything
  else, including such a fence nested inside another code fence, over 20,000 characters, or beyond 8 per message,
  stays Markdown (shown as inert code).
- **Lazy runtime**: the renderer, Zod and the component library are one on-demand chunk (`Block-*.js`, ~200 kB,
  ~61 kB gzip). A chat with no rich content never fetches it (checked in `scripts/browser-check.mjs`).
- **Allowlist** (`library.tsx`): `Stack, Card, Text, Heading, Badge, Metric, BulletList, Table, Divider, Chart, Button, Input, Choice`.
  Every value is rendered as React text. There is **no** link, form submit, image, iframe, media or URL component, so a
  block cannot navigate, fetch or mutate anything. `onAction` is a no-op.
- **Safe actions** (`reply.ts`): `Button`, `Input` and `Choice` do one thing: add text (≤ 500 chars) to the composer draft
  of the chat on screen through `ReplyContext`, provided by `Chat.tsx`. They never send; the reader reviews and presses
  Send, so the follow-up is an ordinary authenticated message in the current chat (PRD §7.4). Without a provider the
  buttons are disabled. No Go route is involved, so there is no new server surface to authorize.
- **Action choke point** (`actions.ts`, shipped in the on-demand `Block-*` chunk because only `library.tsx` and `Block.tsx` import it): everything a block can do goes through `runAction(name, payload, sinks)`.
  The name must be an own key of the action table (so `constructor`/`__proto__` are refused); the only action is `reply`.
  The payload is rebuilt by `cleanReply` (string only, control and bidi-override characters removed, trimmed, 1-500
  characters) before a handler sees it; that function is the sanitisation point. `Block.tsx` routes the renderer's
  `onAction` through `runAction` with **no sinks**, so a model-declared action never does anything. An action that needs the
  server must be added to the table together with its secured Go route (session, approval, ownership, CSRF), never by a
  component calling `fetch`. `actions.test.tsx` pins the refusals, that a payload reaches only the composer sink, and (by
  scanning the renderer's sources) that no DOM sink or dynamic code exists in `web/src/openui/*` or `MessageBody.tsx`.
- **Charts** (`Chart.tsx`): bar, line and pie as plain SVG from `kind`, `title`, `labels`, `values` (1-24 points, finite
  numbers, labels and values the same length; checked in `validate.ts` because the parser does not enforce schema
  min/max). The numbers are also shown as a text table. The renderer is its own on-demand chunk (`Chart-*.js`, ~2 kB)
  behind `React.lazy`; `TestOpenUIChunksAreNotInTheInitialBundle` (`internal/webui`) fails if the shell preloads, or the
  entry script contains, the `Block` or `Chart` chunk.
- **Strict gate** (`validate.ts`): the parser is permissive by design (it drops what it cannot place). A block is
  rendered only if it parses with zero errors, is complete, has a `Stack` root, contains only allowlisted components
  in allowed positions (`Card` not in `Card`, no `Stack` inside `Card`), no dynamic props, no `Query`/`Mutation`/`$state`,
  no dangling or orphan statements, ≤ 300 nodes, depth ≤ 6, ≤ 40 children per container, ≤ 100 items per list and ≤ 4,000
  characters per string. Otherwise the block is shown as its **source, as text**, with a one-line reason — never blank,
  never half-built.
- **Streaming**: an unterminated block while the reply streams is a quiet placeholder ("Preparing rich content…"); it is
  validated and rendered only once its fence closes, and the result is memoised by source, so frequent deltas do no
  repeated work. If the reply ends with the block still open, the source is shown.
- **Crash safety**: an error boundary around the renderer falls back to the source.

## CSP and the OpenUI runtime

The shell's CSP is `default-src 'none'; script-src 'self'; style-src 'self' 'sha256-…'; connect-src 'self'; …`.
Two things in the OpenUI stack had to be handled rather than relaxed:

1. `Renderer` injects one inline `<style>` (spinner keyframes) when it mounts. It is allowed **by hash of that exact
   text** (`openUIStyleHash` in `internal/server/server.go`), not by `'unsafe-inline'`. `web/src/openui/csp.test.ts`
   fails if a library upgrade changes the text, so the hash is updated on purpose.
2. Zod probes `new Function` (a `script-src` violation under our CSP) unless JIT is off; `zodConfig.ts` turns it off and
   **must be imported first**.

`@openuidev/lang-core` also contains opt-in usage telemetry (PostHog). Runtime telemetry is off unless
`OPENUI_RUNTIME_TELEMETRY_ENABLED` is set (never, here), and `connect-src 'self'` would block it anyway; a test pins
that nothing is sent. The browser bundle must also not *contain* a vendor analytics key (the bundle guard in
`internal/webui/webui_test.go` refuses `api_key`/`apiKey`, and was not loosened), so `web/vite.config.ts` removes the
sender at build time (`strip-openui-telemetry`; each rewrite must match exactly once or the build fails, so an upgrade
cannot silently bring it back). **Install-time** telemetry is different: its `postinstall` script posts to PostHog when the
package is installed. `web/.npmrc` therefore sets `ignore-scripts=true`.

## Not done here (explicit)

- The versioned OpenUI instruction that tells the model the language (PRD §7.2) is **not** sent. Hermes' per-request
  instruction mechanism has no captured fixture (`docs/hermes/PHASE0-FINDINGS.md` §2), and inventing one is how an
  endpoint's assumed shape becomes a production bug. Until it exists, models only produce OpenUI if a user asks for it.
- Images/video/audio, source lists, approval cards and a sent (not drafted) follow-up (§7.3 beyond the above). Sending
  from a block would need the secured Go route first.
- The §7.5 malicious-action rows (`/admin/*` from a block, cross-chat form submit) hold because no component can issue a
  request: the only action writes to a local draft. `Rich.test.tsx` pins that no `fetch` happens on click.
- Hermes exposes no OpenUI route (PRD UI-001, `docs/hermes/PHASE0-FINDINGS.md` has none), so the registry stays client-side.
