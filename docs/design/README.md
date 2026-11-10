# Antares design

`antares.figcraft` is the design source of truth. Open it in FigCraft (File > Open).

It started as a one-time capture of the web UI (`web/src`). From here, changes start in the design file and are then
built in code. Nothing regenerates the file from code.

Pages: Design system (colour and number variables with Light/Dark modes, text and effect styles, components),
Chat, Dialogs, Settings, Admin, Account states, Phone, Tablet, Phone landscape. Frames are bound to the `Antares`
variable collection; dark frames switch the collection's mode. Clickable flows between frames are prototype
interactions (use Present).

## Blog (built; the frames are the reference)

Spec and plan: `specs/001-blog-cms-openui-publish/`. Two pages and one Admin frame:

- **Blog public** (signed out): index (populated, dark, empty, loading, error), post (text only, enriched, enriched dark,
  one block in plain-fallback state, not found), and Phone versions.
- **Blog CMS** (signed in): My posts (all five status chips, dark, empty, loading, error, row actions, delete confirm,
  Phone), Blog entry shown/hidden, Editor (empty, writing, dark, selection toolbar, saving, offline, conflict, image
  uploading/failed/inserted, post settings, Phone), and the publish flow: choose (with a Limits note: what a check reads, blocks, checks per 24 h, images), too long to check (the check
  disabled, the original still allowed), unavailable, checking, preview (dark,
  unverified source, refused block), failed, timed out, cancelled, disconnected, out of date, unusable reply, published,
  and Phone checking/preview/failed/published.
- **Blog share** (designed for spec `specs/002-share-blog-social/`, not built yet): the chat's shared picture beside the
  blog's story card (1080 × 1920, warm beige `#e9e4d4` background, mint strip, white card, pink mark and bullets, cobalt
  "POST" sticker and highlighter bar; same layout language as the chat picture, different colours), the Share menu on
  the post page (desktop: Copy link, WhatsApp, X, Facebook, LinkedIn, Telegram, Email, Story image; phones open the
  device's own sheet instead), the Story dialog (ready, preparing, saved with no share sheet, failed; Phone ready), the Share menu "Link copied" state, the
  Phone Share menu ("Share with your apps", Copy link, Story image), the editor's Share preview in Post settings, and
  the story card's edge cases (no cover, no headings, very long title, right-to-left title, emoji title; the design
  renderer has no emoji font, so emoji show as boxes here and are system emoji in the app). Prototype links: menu "Story
  image" to the dialog, Close back to the menu, Try again back to ready, Copy link to the copied state.
- **Admin / Suspended user (posts hidden)** on the Admin page: states that posts are hidden and no content is shown.

Flows are wired as prototype interactions: index to post and back, sign in to My posts, My posts to Editor, row actions
to delete confirm, Editor to Settings and Publish, Publish choose to checking (and on a timer to preview), preview to
published, every failure to retry / publish original / back to editing, published to the post and My posts, and the
Phone equivalents.

How the blog joins the existing flow: a "Blog posts" button directly under "New chat" in the rail of every desktop
chat, settings and admin-account frame opens My posts (search and list sit below it); the Account menu on Chat ("Account menu" and "Account menu (admin)") has a new
"Blog posts" row that opens My posts; the rail's "Chats" item on every My posts frame goes back to Conversation; the
public "Sign in with Google" button opens the existing Login frame; Published "View post" opens the public post.

Sources: Antares never verifies a source. The assistant names sources, the server stores every one as `verified: false`
(the database enforces it), and the product never shows a "verified" state. So the frames show: an `unverified` chip on
every source, a "2 unverified sources" chip on every block that lists sources, the line "Antares has not checked these
sources. To accept this block, confirm you have checked them yourself." with the tick "I have checked these sources
myself" (Accept stays gated on it), and on the public post "Sources named by the assistant, not independently checked:"
in front of each block's source chips. Earlier versions of these frames showed green VERIFIED pills; they are gone.
The public post's closing "Sources" card (a list of every source) is not built in code yet.

Behaviour the frames cannot show (built, and tested):
- The post row's "⋯" is a menu button: Enter, Space or ArrowDown opens it onto the first item, ArrowUp onto the last; the arrows,
  Home and End move (wrapping); Escape closes it and returns to the button; Tab leaves it; a press outside closes it.
- Choosing an item leaves focus on that button (a dialog the action opens returns to it). The list is not replaced by "Loading…"
  when it reloads, so Duplicate and Unpublish do not lose the keyboard user's place. After a Delete, focus goes to the row that took
  the deleted one's place, or to New post when there are none.
- Publish is a modal dialog over the editor (focus in, Tab kept inside, Escape or Cancel back to editing, the editor behind it inert);
  each later full-screen step takes focus to its heading, and closing Publish returns focus to the editor's Publish button.

Publish / preview as built: Review before publishing has the frames' three parts (your original, the enriched preview with Accept and
Reject on each block, the assistant's notes with "Sources for the selected block"). One state the frames do not draw: once the author has
decided a block, "Accept all and publish" becomes "Publish with N blocks" (publishing what was accepted); "Accept all and publish" waits
for the "I have checked these sources myself" tick on every block, since the server refuses an unticked acceptance. The steps after the
dialog (checking, preview, failure) are the page itself, below a "Back to editing" bar, with the editor hidden while they show.
(They used to be a fixed full-screen layer; on phones that made the browser widen its layout to 832px.)

My posts as built differs from its frames in two ways, on purpose. Phone rows also show an Open button (the title is plain text, so
opening a post is one control and not two tab stops). The rail's account line shows the email and has no Menu button: Settings and Sign
out stay in the chat's account menu, one click away through Chats.

Blog: where the frames and the code still differ (kept honest; each is a gap to close in the design or the code):
- The preview frames draw two blocks and only Reject on the first one is wired as a prototype interaction; the code handles any
  number of blocks, each with Accept and Reject.
- The public post's closing "Sources" card (a list of every source) is in the frames and not built.
- Publish / published and the failure steps (failed, timed out, cancelled, disconnected, out of date, unusable reply) keep their own
  plain headers in code below a "Back to editing" bar; the frames draw them inside the editor's top bar (My posts, Saved, Settings,
  Publish).
- The Editor frames have a top bar and no rail, as built. The phone Menu has a "Blog posts" row in code; the Phone, Tablet and
  Landscape frames do not show it yet.
- Sort, hover and expand interactions in blocks are static states in the frames; the reader opens accordions and switches tabs (tested),
  and sorting and hover are not yet proven (T059).
- The AI-added marker sits above a block in code, and the frames put it beside the block's title. Kept above, by decision (T062):
  a block need not have a title, and every AI-added block must carry the marker, so it belongs to the block and not to what is
  inside it. The frames' inline placement is the drawn variant for blocks that have a title.
- The Admin frame is a single card, not a full Admin screen.

Known gaps: only states the mock backend could show were captured (no live Hermes). Text is placed per rendered line, so
it does not reflow when edited. Not yet drawn: most Notifications and Jobs states, Memory and Skills states, model
picker states, delete-failed and pinned rows, dictation consent, the recorder, share-link variants, the keyboard-open
phone layout, and keyboard shortcuts.
