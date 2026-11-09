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

## R4. Card design: Antares's own look, reused

- **Decision**: reuse the look and the code of the shared-chat picture (#250/#261, `web/src/shareImage.ts`). The paper
  background with dots, the cobalt badge with the A, the date pill, the tilted sticker, the dotted periwinkle strip with
  a mono capital label, the white card with a thick ink outline and hard offset shadow, the pink highlighter bar, cobalt
  diamond bullets, and the Space Grotesk and mono faces. The picture is always on the light paper (the existing
  behaviour), so there is no dark variant and no per-theme contrast work: the pairs are the ones already used by the chat
  picture. Layout for a post: brand row, "POST" sticker, title headline, card labelled "SUMMARY" with the summary and
  the cover, author name, footer "Shared from Antares". Story-safe zones: the top 250 px and bottom 340 px hold no
  text-critical content beyond the brand row and footer, which the chat picture already treats as chrome.
- **Rationale**: the owner asked for it to look like sharing a chat; reusing the primitives makes that true by
  construction and keeps one place to change the look. The drawing primitives are exported from a shared module rather than
  copied.
- **Alternatives**: a second, separately styled card (drift, duplicated code; rejected); a template picker (out of scope).

## R5. Server changes

- **Decision**: none required. The share link is the post's canonical address, which the post page already knows (it is
  in the page's canonical link). The link preview that other apps show already comes from the blog's server-written
  metadata (spec 001, FR-023).
- **Note**: if a later user need is a 9:16 `og:image`-style address, that is the point to revisit R1 and the public
  inventory.

## R6. Link hygiene

- **Decision**: share URLs are built from the canonical address only (origin + `/b/{slug}`); a kept test asserts no
  query string, fragment or identifier is added, for every destination and for the native sheet.
