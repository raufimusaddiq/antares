# UI contract: Share and Story

There is **no new HTTP route**. This contract is the behaviour the design frames and tests must hold.

## Share control (post page)

- Button "Share" in the post's header area, keyboard reachable, with a visible focus ring.
- If `navigator.share` exists: opens the native sheet with `{ title, url }` where `url` is the canonical address.
- Otherwise opens a menu: Copy link, WhatsApp, X, Facebook, LinkedIn, Telegram, Email. Each destination is a plain link
  that opens in a new tab with `rel="noopener noreferrer"`; none loads a third-party script.
- Copy link: writes the canonical address; announces "Link copied" in a polite live region; falls back to selecting a
  read-only field when the Clipboard API is refused.
- Entry "Story image" opens the dialog below.
- Cancelling the sheet or closing the menu returns focus to the Share button and shows nothing.

## Story dialog

- `role="dialog"`, `aria-modal`, labelled "Share as a story"; focus moves in and returns to the trigger on close.
- States: preparing (progress text), ready (preview of the 9:16 picture with an alternative text that reads the title and
  summary), sending, saved, failed.
- Buttons: "Share to an app" (only when `canShare({files})`), "Save image", "Copy link", "Close".
- When the sheet cannot take images: text says the picture was saved and the link copied, and that a link sticker has
  to be added by hand in Instagram.
- Failure message names what failed and keeps Copy link available.

## Picture

- PNG, 1080 × 1920. Text safe zones: no text in the top 250 px or bottom 340 px.
- Contains: title (≤ 4 lines), summary (≤ 6 lines), author name, cover or plain block, site name and short address.
- Text is clamped at a word boundary with an ellipsis; plain text only.

## Author preview (editor)

- A "Share preview" item in Post settings shows the story card and the link preview from the draft's current values,
  labelled "Preview. Not shareable." It never offers send or copy for a draft.

## Kept tests

- The shared URL equals the canonical address for the native sheet and every destination (no query, no fragment).
- No Share control, card or link is rendered for a draft or a non-published post.
- The produced picture is 1080 × 1920 and not blank for: long title, no summary, no cover, right-to-left title, emoji.
