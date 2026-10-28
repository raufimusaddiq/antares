# Runbook — OpenUI in chat (slice 5, PRD §7)

Code: `web/src/openui/*`, `web/src/MessageBody.tsx`. Packages (exact pins): `@openuidev/react-lang@1.0.2`,
`@openuidev/react-ui@0.18.1`, `@openuidev/react-headless@0.18.1`, `zustand@4.5.7`, `zod@4`.

## What is built

Antares renders OpenUI **natively**: the model writes OpenUI Lang against OpenUI's own chat component library
(`openuiChatLibrary` from `@openuidev/react-ui`), and `Block.tsx` renders it with that library's `Renderer`. There is no
Antares-specific component set to keep in step with the prompt.

- **Detection** (`segments.ts`, main bundle, no OpenUI import): a reply that starts with `root =` is one OpenUI block (up
  to 60,000 characters). Otherwise an OpenUI segment is a fenced block whose info string is `openui` or `openui-lang`
  (at most 8 per message); anything else, including such a fence nested in another code fence, stays Markdown.
- **Lazy runtime**: renderer, library, its CSS and the lucide icons are on-demand chunks (`Block-*.js` ~1.9 MB,
  `Block-*.css` ~370 kB, one small chunk per icon). A chat with no rich content never fetches them;
  `TestOpenUIChunksAreNotInTheInitialBundle` (`internal/webui`) fails if the shell preloads or contains `Block`.
- **Strict gate** (`validate.ts`): the parser is permissive, so a block is rendered only if it parses with zero errors,
  is complete, has a `Card` root, uses only components of `openuiChatLibrary`, has no `Query`/`Mutation`/`$state`, no
  unresolved or orphan statements, ≤ 600 nodes, depth ≤ 12, ≤ 300 statements, ≤ 200 items per array and ≤ 20,000
  characters per string. Otherwise the block is shown as its **source, as text**, with a one-line reason.
- **Streaming**: while the reply streams, the block renders progressively (`isStreaming`), but every update is checked
  first (`check(source, true)`): root, unknown components, no query/mutation/state and every size limit. Only
  completeness, parse errors and dangling references wait for the end. A partial program that fails is shown as source
  at once (those failures cannot be fixed by more text). A reply that ends with a fenced block still open shows the source.
- **Earlier blocks**: blocks written for Antares' first component set (`[antares-openui v1]`, root `Stack`) are not
  rendered by the native library; they are shown as source, labelled "made by an earlier version of rich replies".
  At the switch, production held one such block (an owner test), so no v1 renderer was kept.
- **Actions** (`actions.ts`, the choke point; `Block.tsx` routes the renderer's `onAction` through `runAction`):
  - `continue_conversation` / `reply`: the message (and any form state, as a fenced JSON block) is cleaned by
    `cleanReply` (string only, control and bidi characters removed, ≤ 4,000 characters) and put in the composer draft of
    the chat on screen via `ReplyContext`. It never sends; the reader reviews and presses Send.
  - `open_url`: only `http:`/`https:` URLs without user info, ≤ 2,048 characters, opened after a `window.confirm` in a
    new tab with `noopener,noreferrer`. `javascript:`, `data:` and the like are refused.
  - Any other action name (including `constructor`/`__proto__`) is refused. No action calls `fetch`; an action that needs
    the server must come with its secured Go route (session, approval, ownership, CSRF).
- **Theme**: `styles.css` restates OpenUI's font tokens with the app font and its ink/cobalt; `appearance.ts` mirrors the
  app's light/dark choice to `data-openui-theme` on `<html>`, which OpenUI's token sheet reads (system = no attribute,
  so it follows `prefers-color-scheme` like the app). `ThemeProvider` is not used: it injects a `<style>` the CSP refuses.
- **Crash safety**: an error boundary around the renderer, and a load boundary around the lazy import, fall back to the source.
- **Tests**: `native.test.tsx` renders OpenUI's own chat examples through the gate and checks the action refusals;
  `prompt.test.ts` pins that `internal/chat/openui_prompt.txt` is what the library generates.

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

- A sent (not drafted) follow-up from a block. Sending from a block would need the secured Go route first.
- The §7.5 malicious-action rows (`/admin/*` from a block, cross-chat form submit) hold because no action issues a
  request: replies write to a local draft and links only open after a confirm.
- Hermes exposes no OpenUI route (PRD UI-001, `docs/hermes/PHASE0-FINDINGS.md` has none), so the registry stays client-side.

## Visualize toggle (#153, LLM-008)

Per chat, **on** for a new chat (owner decision 2026-10-28; `store.CreateChat` stores and returns `visualize: true`; chats made before keep their setting): `PATCH /api/chats/{chatId}` with `{"visualize": true|false}` (owner only; the chat view
carries `visualize`), stored in `chats.visualize` (migration 0022); the composer shows the checkbox. While it is on,
each run of that chat sends the versioned instruction `[antares-openui v2]` followed by OpenUI's generated chat-library
prompt (`internal/chat/openui_prompt.txt`, embedded by `personality.go`, `openUIInstruction`) as the `instructions` field
of `POST /v1/runs`, before the user's own preferences (#111). Hermes reads it into the run's ephemeral system prompt
(`gateway/platforms/api_server_runs.py` `_resolve_conversation_history` → `ephemeral_system_prompt`), so nothing global
or profile-wide changes. After a library upgrade, regenerate the prompt with `node web/scripts/openui-prompt.mjs`
(`prompt.test.ts` fails until you do) and bump the tag. `TestVisualizeIsPerChatAndReachesOnlyItsRuns` checks the
instruction reaches only that chat's runs, another user cannot set it, and turning it off stops it.
