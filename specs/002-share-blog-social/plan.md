# Implementation Plan: Share Blog Posts to Social Media and Stories

**Branch**: `002-share-blog-social` | **Date**: 2026-11-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/002-share-blog-social/spec.md`

## Summary

Published posts get a Share control and a "Story image" flow. The 9:16 picture is **drawn in the reader's own browser**
from data the post page already holds (title, summary, author name, cover), then handed to the device's native sharing
sheet or saved. It is drawn with the same canvas toolkit, fonts and dialog that already make a shared chat's 9:16
picture (`web/src/shareImage.ts`, `ShareImageDialog` in `ShareChat.tsx`, #250/#261), so it looks like Antares
by construction, in its own blog colours so it is told apart from a chat picture (spec FR-020). The colours are one
palette object passed to the kit; the chat picture keeps its current palette. **No new server route, no new database table, no new dependency, no AI.** This is the smallest design
that meets FR-001…FR-018 and makes the spec's new-public-surface requirement (FR-019) unnecessary; see
[research.md](research.md) R1 for why drawing on the server was rejected.

Per AGENTS.md ("Design comes first") the Share menu, the Story dialog (preview, sending, fallback, failure) and the
editor's share preview are designed in `docs/design/antares.figcraft` before code; see Stage 0 (the chat's share-image frames are the base) and
[contracts/ui-contract.md](contracts/ui-contract.md).

## Technical Context

**Language/Version**: TypeScript, React 19 + Vite (web). Go only for the one server-side change in R5 (none required;
see below).

**Primary Dependencies**: none new. Browser APIs: Canvas 2D, `Intl.Segmenter`, Web Share API level 2
(`navigator.canShare({files})`), Clipboard API, the page's already-loaded self-hosted Space Grotesk face.

**Storage**: none. Nothing is stored or cached on the server; the picture is a blob in the page.

**Testing**: vitest for the layout function (text fitting, truncation, RTL, no-cover, no-summary) and the Share/Story
components (feature detection, fallbacks, focus, announcements); real-browser checks in `scripts/browser-check.mjs` for
the dialog at phone/tablet/desktop in both themes and for the produced picture's size (1080 × 1920) and non-blank
content.

**Target Platform**: the existing embedded web bundle (`internal/webui/dist`, rebuilt with the change).

**Project Type**: web application, existing; this feature is front-end only.

**Performance Goals**: preview ready < 3 s on a typical mobile connection for 95% of attempts (SC-002); the cover is
the only network fetch (the same-origin image the page already shows).

**Constraints**: constitution I–V; CSP unchanged (`img-src 'self' blob:` already allows the preview and download);
no third-party script or button (FR-018); no new unauthenticated route.

**Scale/Scope**: one dialog, one menu, one pure layout module, one editor preview.

## Constitution Check

| Principle | Status | How the plan satisfies it |
|-----------|--------|---------------------------|
| I. Default deny, admin blind | Pass, no exception added | No new route. The card is built from what the public post page already received, so nothing is exposed that the post does not show. FR-011/FR-012/FR-013 hold by construction (no endpoint to leak, probe, or flood). The editor preview uses the author's own draft in their own session. `auth.PublicBlogRoutes` and its kept test are **unchanged**. |
| II. Front end, not an agent | Pass | No Hermes run and no AI service. |
| III. Honest, capability-driven UI | Pass | The Story flow detects what the device can do (`canShare` with a file) and shows only what will work; the Instagram/WhatsApp limits are stated in the dialog in plain words (spec Assumptions). |
| IV. Tests guard behaviour | Pass | Layout and component tests plus browser checks listed above; a kept test asserts the share link is the canonical address with no query string. |
| V. Small footprint | Pass | No dependency, route, table or container. |

Re-checked after design: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/002-share-blog-social/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/ui-contract.md
```

### Source code

```text
web/src/blog/
├── share/
│   ├── ShareMenu.tsx        # Share button, native sheet or fallback list, copy link
│   ├── StoryDialog.tsx      # reuses the chat's ShareImageDialog flow (preview, send, save, note) for a post
│   ├── postCard.ts          # the post's card: composes the shared drawing kit below (1080×1920)
│   ├── shareLinks.ts        # destination URLs from title + canonical link
│   └── *.test.ts(x)
├── Public.tsx               # mounts ShareMenu on the post page
└── PostSettings.tsx / PreviewScreen.tsx   # author's share preview (draft data)
web/src/shareImage.ts          # drawing primitives (badge, dots, pill, labels, layout, wrap) move to web/src/cardKit.ts,
                               # exported and reused by both pictures; the chat picture's output must not change
docs/design/antares.figcraft  # new frames, Stage 0
scripts/browser-check.mjs     # share-menu, story-dialog views
```

**Structure Decision**: front-end only inside the existing blog area. `storyCard.ts` is pure (takes a plain object and
a canvas-like context) so it is unit-testable without a browser.

## Stages

0. **Design in figcraft** (blocks code): Share menu (phone sheet, desktop list), Story dialog (preview, sending,
   saved-and-copied, failure), editor share preview; light and dark; record the frames' ids in the design README.
1. **Kit and card**: lift the drawing primitives out of `shareImage.ts` into `cardKit.ts`, with the colours turned from
   the module constant `C` into a `Palette` argument (the chat passes today's values; no change to the chat
   picture: its existing `ShareImage.test.tsx` and a pixel comparison of a fixed sample before and after prove it), then
   `postCard.ts` composing them: brand row (mark, "Antares", date pill), a tilted "POST" sticker, the title as the
   headline with the highlighter bar, a card with the dotted strip labelled "SUMMARY" holding the summary and, after
   diamonds, up to four highlights (the post's headings), the cover in a thick-outlined frame, the author's name, and the
   footer. The canvas is a fixed 1080 × 1920 (the chat picture's `STORY_H`), never sized from content; the card steps the
   type down (as `summaryUnits` does), then drops parts in the order FR-022. Tests: the blog palette's text pairs meet WCAG AA and differ from the chat palette; the output is always 1080 × 1920 for
   no summary, no headings, twelve headings, a very long post, no cover, RTL and emoji; every word drawn is found in
   the post; truncation at a word boundary.
2. **Share**: `ShareMenu`, `shareLinks`, canonical-link test, Share on the post page.
3. **Story flow**: `StoryDialog` (the chat dialog's flow, generalised so both use it; card is produced when the dialog opens so the Send tap is a direct user action, which
   iOS Safari requires), fallbacks, announcements, focus.
4. **Author preview** in the editor from the draft.
5. **Browser checks, docs, deploy, smoke**: real-browser views; `docs/PRD.md` §30 gains a Share paragraph; production
   verification by loading a published post and producing the picture.

## Complexity Tracking

No constitution violation to justify.

Spec follow-up (to apply with this plan): FR-011/FR-013/FR-019 become "holds because there is no picture endpoint";
FR-014 holds because the card is made from the current page. Recorded in [research.md](research.md) R1 so the spec
stays honest.
