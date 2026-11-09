# Research: Share Blog Posts to Social Media and Stories

## R1. Where the 9:16 picture is drawn

- **Decision**: in the reader's browser, on a Canvas 2D surface, from the data the post page already has.
- **Rationale**: the browser already shapes text correctly for right-to-left and non-Latin scripts and renders emoji
  (spec edge cases); it already loads the self-hosted Space Grotesk face; the cover is a same-origin image so the canvas
  is never tainted. It adds no public route, so the constitution's inventory of unauthenticated surfaces does not grow,
  there is nothing to rate limit, cache or flood, and the "not found" behaviour for hidden posts needs no code. The
  author's draft preview uses the same function with the draft's data. No dependency, no storage.
- **Alternatives**: draw on the server in Go (needs a new Go dependency for text, fonts shipped in the binary, and a
  hand-written shaper to get RTL, emoji and non-Latin right; plus a new public endpoint to bound and defend; rejected
  against Principles I and V); a headless browser on the server (new container; rejected); an AI-generated image
  (cost, nondeterminism, and the spec excludes it).

## R2. Handing the picture to Instagram or WhatsApp

- **Decision**: Web Share API level 2: `navigator.canShare({ files:[png] })` then `navigator.share({ files, title,
  url })` where supported; otherwise save the PNG (anchor download of a blob URL) and copy the link.
- **Rationale**: a website cannot post a story or attach a link sticker (no web API; the native story intents need the
  app and a Facebook app id). The sheet is how a web page reaches Instagram and WhatsApp. On Android the sheet lists
  Instagram Stories and WhatsApp; on iOS the sheet reliably offers Photos/WhatsApp but Instagram Stories may need the
  person to save the picture first, so the dialog also offers Save.
- **iOS Safari constraint**: `share()` must run inside the user's tap. The picture is therefore produced when the dialog
  opens, and the Send button calls `share()` directly with the ready file.
- **Alternatives**: custom URL schemes (`instagram-stories://`) need an app id and fail from a browser; third-party
  share SDKs (violates FR-018, adds trackers).

## R3. Text fitting and truncation

- **Decision**: reuse the chat picture's `wrap` and `layout` (measure with `ctx.measureText`, break at spaces and inside
  a too-wide word, end a clamped block with an ellipsis, step the summary's type size down until it fits) and add what the
  chat picture lacks: break at a word boundary with `Intl.Segmenter` for languages without spaces, and set
  `ctx.direction` from the text's dominant direction. The title is clamped to 4 lines.
- **Alternatives**: CSS-to-image libraries (new dependency, and the same measuring problem).

## R4. Card design: the chat picture's style, in blog colours

- **Decision**: reuse the style and the code of the shared-chat picture (#250/#261, `web/src/shareImage.ts`): dotted flat
  background, badge with the A and the date pill, tilted sticker, dotted strip with a mono capital label, white card with a
  thick ink outline and hard offset shadow, highlighter bar, diamond bullets, Space Grotesk and mono. Only the palette
  differs, so a post's picture and a chat's picture are told apart at a glance and both read as Antares.
- **Blog palette (chosen by the owner from options drawn in figcraft; all from the `Antares` variables)**:

  | Role | Chat picture | Blog picture |
  |---|---|---|
  | Background | paper `#f4f1e8` | warm beige `#e9e4d4` (`rail`) |
  | Card strip | periwinkle `#b3c1ff` | mint `#8ef0c0` |
  | Badge, label squares, diamonds | cobalt `#2d4bff` | pink `#ff6fae` |
  | Highlighter bar, sticker | pink / pink | cobalt / cobalt (sticker text white) |
  | Card, ink, text | white, `#0b0b0b` | white, `#0b0b0b` |

  Rejected after review: blush, mint, periwinkle and cobalt backgrounds (too strong against the chat's neutral look) and
  a dark option. The mark's A is ink on pink; all text on the beige background is ink.
- **Always light**, whatever the reader's theme (the chat picture's existing behaviour). A test checks every text and
  background pair of the blog palette at WCAG AA and that the two palettes differ.
- **Rationale**: one drawing kit, one place to change colours; the owner asked for a different colour from chat. The
  palette is a single object, so adjusting it later is one edit.
- **Alternatives**: a second, separately drawn card (drift; rejected); a template picker (out of scope).

## R4b. Summary and highlights without AI

- **Decision**: summary = the author's summary, else the opening text cut at a sentence or word boundary; highlights = the
  post's section headings (up to 4, in order), read from the published document the page already has. Priority when space
  runs out: drop highlights from the last, then the cover, then shorten the summary; the title, author and address
  stay. The canvas height is fixed at 1920 so the picture is 9:16 by construction.
- **Rationale**: a faithful condensation with nothing invented (FR-021), no cost, no Hermes run, no latency. The chat
  picture's summary is AI-written and labelled as such; for a post the author has already written a summary, so
  reusing it is both cheaper and more faithful.
- **Alternatives**: an AI blurb for the story (cost, invention risk, needs a run per share by a reader; deferred); the
  full post text (unreadable on a story).

## R5. Server changes

- **Decision**: none required. The share link is the post's canonical address, which the post page already knows (it is
  in the page's canonical link). The link preview that other apps show already comes from the blog's server-written
  metadata (spec 001, FR-023).
- **Note**: if a later user need is a 9:16 `og:image`-style address, that is the point to revisit R1 and the public
  inventory.

## R6. Link hygiene

- **Decision**: share URLs are built from the canonical address only (origin + `/b/{slug}`); a kept test asserts no
  query string, fragment or identifier is added, for every destination and for the native sheet.
