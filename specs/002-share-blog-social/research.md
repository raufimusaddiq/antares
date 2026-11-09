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

- **Decision**: measure with `ctx.measureText`, break lines at word boundaries using `Intl.Segmenter` (granularity
  `word`, so languages without spaces still break at a word), set `ctx.direction` from the text's dominant direction,
  clamp title to 4 lines and summary to 6, and end a clamped block with an ellipsis. Font sizes step down once before
  clamping.
- **Alternatives**: CSS-to-image libraries (new dependency, and the same measuring problem).

## R4. Card design and contrast

- **Decision**: one design, two colour variants taken from the site's light and dark tokens; a test checks every
  text-on-background pair at WCAG AA (4.5:1) from the token values. The cover sits in a rounded frame in the upper part
  when present; without a cover, a plain brand block takes its place. Story-safe zones: the top 250 px and bottom 340 px
  hold no text (Instagram overlays its own controls there).
- **Alternatives**: a template picker (out of scope per spec).

## R5. Server changes

- **Decision**: none required. The share link is the post's canonical address, which the post page already knows (it is
  in the page's canonical link). The link preview that other apps show already comes from the blog's server-written
  metadata (spec 001, FR-023).
- **Note**: if a later user need is a 9:16 `og:image`-style address, that is the point to revisit R1 and the public
  inventory.

## R6. Link hygiene

- **Decision**: share URLs are built from the canonical address only (origin + `/b/{slug}`); a kept test asserts no
  query string, fragment or identifier is added, for every destination and for the native sheet.
