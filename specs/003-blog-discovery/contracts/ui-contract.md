# UI contract: blog discovery

Design: `docs/design/antares.figcraft`, page **Blog discovery** (approved by the owner; each frame exists in light and dark).
Entries are buttons or menu rows, never bare text links.

## Components (all from the existing design system)
| Name | Look | Used for |
|------|------|----------|
| Secondary pill (`.btn`) | white (`card`), 1.5 px ink border, hard 3 px offset shadow, radius pill, 16 px bold | "More posts" (left-arrow icon), "Show more", "Back to Antares" |
| Filled pill (cobalt) | `cobalt` fill (`cobalt-ink` text and icon), 1.5 px ink border, hard 3 px shadow, 16 px bold; the app's existing primary button | "Read the blog" (book icon) on the sign-in and status screens, so it is not mistaken for a fourth post card (owner choice) |
| Quiet pill (`button.quiet`) | no fill, 1.5 px ink border, no shadow, 15 px bold (13 px when small) | "Sign out", "Clear" |
| Latest-post card | `card` fill, 1.5 px ink border, hard 3 px shadow, radius 14, title 16 px bold, meta (author · date) mono 11 px muted; the whole card is the link | the strip |
| Dot grid | the app's `--dots` background (14 px tile, 16% ink in light, light dots in dark) | sign-in and status screens (already in CSS) |

## Entries
| Where | Control | Goes to | Notes |
|-------|---------|---------|-------|
| Sign-in page (login, login-failed) | strip "Latest posts" (≤3 cards) below the sign-in action and the privacy note, then a "Read the blog" cobalt pill | `/b`; cards `/b/{slug}` | strip absent when empty or failed (the pill stays); never blocks or delays sign-in |
| Waiting / suspended / revoked screens | the same strip and pill, then a quiet "Sign out" pill | `/b`; cards `/b/{slug}` | strip absent when empty or failed |
| Chat account menu (approved) | "Read the blog" row (book icon) after "Blog posts" (pencil icon); a user without writing sees Settings, Read the blog, Sign out | `/b` | desktop menu and phone menu are the same rows |
| Chat side rail (approved) | a 44 px round book-icon button right of the "Blog posts" pill (label "Read the blog", tooltip); when the user cannot write there is no "Blog posts" pill and a full-width "Read the blog" pill takes that slot | `/b` | adds no row, so the chat history keeps its space (owner: a second full-width pill made history discovery worse) |
| Admin screens | none | | |

## Public pages (`/b`, `/b/{slug}`)
- Header: anonymous → "Sign in with Google" (as today); any account → "Back to Antares" secondary pill (`/`; "Back" on phones). The list and a post each have it.
- List: search box (placeholder "Search posts", ≤80 chars, applied on Enter or after a short pause) with the result count on the right (`4 posts`) and, when text is typed, a quiet "Clear" pill; results newest first; "Show more" secondary pill when a full page came back, which shows a dimmed "Loading more…" while the next page loads; states "Nothing published yet", "No posts match" (with "Try another word, or clear the search to see every post."), and the load error with "Try again" that keeps the posts already shown.
- Card of a post the reader wrote (ACTIVE reader only): a pink "Yours" chip in the meta row; no filter.
- Post: a "More posts" secondary pill (left-arrow icon) after the text; existing share controls unchanged.
- Frames drawn: sign-in (with strip, without strip, phone), waiting (desktop, phone), chat rail and menu (writer, user without writing), phone menu (writer, user without writing), list (signed in, search results, no results, error, Show more loading, phone), post (desktop, phone).
- Accessibility: every entry is a button or link with visible text (icon-only controls carry a label and tooltip); the result count is announced in a polite status; focus stays on the control that was used (Show more keeps focus and new items are added after it); 44 px targets at 320 px; cards are one link each with the post title as its name.

## Server (existing route, new optional query)
`GET /api/public/blog/posts?page=&limit=&q=` → the same JSON array of PublicSummary. Cache-Control as today (public, 60 s).
