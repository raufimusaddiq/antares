# Quickstart: validating Share and Story

Prerequisites: a published post with a summary and cover, one without either, one with a right-to-left title.

1. **Native share (phone)**: open the post, tap Share; the device sheet shows the title and the canonical link.
2. **Fallback share (desktop)**: open the post, Share lists the destinations; Copy link announces "Link copied";
   each destination opens the right service's compose page with title and canonical link only.
3. **Story image (phone)**: Share → Story image; the preview shows a 9:16 picture in under 3 s; "Share to an app"
   opens the sheet with an image; send it to WhatsApp Status and to Instagram Stories (on iOS use Save, then add it from
   the camera roll; the dialog says so).
4. **Story image (desktop)**: Save image downloads a 1080 × 1920 PNG and the link is copied; text explains the link
   sticker.
5. **Same product**: put the post's picture beside a shared chat's picture; background, mark, cards, labels and the pink
   bar match (SC-008). The chat picture is unchanged by the refactor (its test and a before/after comparison).
6. **Edge cases**: the three prepared posts, one with twelve headings, one of 20,000 words, an emoji title and a very long title: every picture is exactly 1080 × 1920, legible, with highlights dropped before anything overflows.
7. **Privacy**: a draft or unpublished post shows no Share control; no network request carries anything beyond what the
   post page already fetched (check in the browser's network panel: only the cover image).
8. **Automated**: `npm test` (layout and components), `ONLY=share-menu,story-dialog node scripts/browser-check.mjs`
   for both themes at 390, 820 and 1280 px; `go test ./...` passes with no change to the public-route inventory.
