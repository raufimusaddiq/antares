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
  uploading/failed/inserted, post settings, Phone), and the publish flow: choose, unavailable, checking, preview (dark,
  unverified source, refused block), failed, timed out, cancelled, disconnected, out of date, unusable reply, published,
  and Phone checking/preview/failed/published.
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

Blog gaps: the preview shows the first two blocks only (metrics, comparison); chart, accordion and fallback blocks are
drawn on the public post. Sort/hover/expand interactions are shown as static states. The Admin frame is a single card,
not a full Admin screen. Block-level accept/reject wiring is limited to Reject on the first block. The Phone, Tablet and Landscape menus have no Blog entry yet, and My posts and the Editor use a simplified rail (brand, New post, Chats, Blog posts) instead of the full chat rail; build the real rail in code and add the phone entry with the matching design change.

Known gaps: only states the mock backend could show were captured (no live Hermes). Text is placed per rendered line, so
it does not reflow when edited. Not yet drawn: most Notifications and Jobs states, Memory and Skills states, model
picker states, delete-failed and pinned rows, dictation consent, the recorder, share-link variants, the keyboard-open
phone layout, and keyboard shortcuts.
