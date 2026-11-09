# Feature Specification: Share Blog Posts to Social Media and Stories

**Feature Branch**: `002-share-blog-social` (no git branch is created by this command)

**Created**: 2026-11-10

**Status**: Draft

**Input**: User description: "support share blog in social media and support native instagram/wa story blog shared (generate 9:16 image from blog summarize)."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Share a post to a social app or by link (Priority: P1)

A reader (or the author) finishes a published post and wants to pass it on. A Share control on the post offers the device's own sharing sheet where it exists (so WhatsApp, Instagram, Telegram, Messages and so on appear as the person has them installed), and otherwise a short list: copy link, WhatsApp, X, Facebook, LinkedIn, Telegram and email. Whatever is chosen carries the post's title and its one canonical address.

**Why this priority**: This is the core of the request and is useful on its own, with no image at all.

**Independent Test**: Open a published post on a phone and on a desktop; share it through each offered route; confirm the receiving app shows the title and the same canonical link, and that the link preview shows the title, summary and cover (already provided by the blog's sharing previews).

**Acceptance Scenarios**:

1. **Given** a published post on a phone with a native sharing sheet, **When** the reader taps Share, **Then** the device's sheet opens with the post's title and canonical link prefilled.
2. **Given** a desktop browser with no native sheet, **When** the reader opens Share, **Then** they see copy link and the listed destinations, and copy link confirms visibly and to a screen reader that it worked.
3. **Given** a reader closes or cancels the sheet, **When** they return to the post, **Then** nothing changed and no error is shown.
4. **Given** the post's address contains tracking of any kind, **When** shared, **Then** the shared link is still the plain canonical address (no personal identifier of the sharer is added).

---

### User Story 2 - Share as a 9:16 story card (Priority: P1)

A reader or author wants to post the article to an Instagram or WhatsApp story. From Share they choose "Story image". Antares produces a vertical 9:16 picture built from the post: its title, its summary, the author's display name, the cover image when there is one, and the site name with a short readable address. On a phone the picture is handed to the native sharing sheet so the person can pick Instagram Stories or WhatsApp Status; where that is not possible the picture is saved to the device and the link is copied, with a one-line instruction on what to do next.

**Why this priority**: This is the distinctive part of the request.

**Independent Test**: On a phone, choose Story image on a published post, see the preview, and send it to Instagram Stories and to WhatsApp Status; on a desktop, download it; check the picture is 9:16, legible and correct.

**Acceptance Scenarios**:

1. **Given** a published post with a summary and a cover, **When** the reader chooses Story image, **Then** they see a preview of a 9:16 picture containing the title, the summary, the author's name, the cover and the site's name and address, all legible at phone size.
2. **Given** a post with no cover or no summary, **When** the picture is made, **Then** a clean layout is still produced (the post's opening text is used if there is no summary) and nothing looks broken or empty.
3. **Given** a very long title or summary, **When** the picture is made, **Then** the text is shortened at a word boundary with an ellipsis, never cut mid-word, never overflowing the card.
4. **Given** a phone whose sharing sheet accepts pictures, **When** the reader confirms, **Then** the picture goes to the chosen app as an image, together with the link where the app accepts one.
5. **Given** a device that cannot hand a picture to an app, **When** the reader confirms, **Then** the picture is saved and the link is copied, and the screen says plainly that Instagram's link sticker has to be added by hand.
6. **Given** a post that is unpublished, hidden or does not exist, **When** anyone opens its address, **Then** they get the post's own "not found" and there is no Share control or picture to reach.

---

### User Story 3 - Authors see and control how their post is shared (Priority: P2)

In the editor the author can see a preview of the story card and the link preview for their post (what a shared link will show), and can choose which cover and which summary are used. Changing the title, summary or cover changes the card from then on.

**Why this priority**: It lets authors get the share right before publishing, but sharing works without it.

**Independent Test**: Edit a post's summary and cover, open the share preview in the editor, confirm the card and link preview match what a reader later gets after publishing.

**Acceptance Scenarios**:

1. **Given** a draft, **When** the author opens the share preview, **Then** they see the story card and the link preview built from the draft's current title, summary and cover, marked as a preview and not shareable by anyone else.
2. **Given** the author edits the summary after publishing and republishes, **When** a reader shares afterwards, **Then** the new text is on the card; earlier pictures already sent are not changed.

---

### Edge Cases

- Languages: right-to-left scripts, non-Latin scripts and emoji in the title or summary render correctly on the card or are replaced by a readable fallback, never as empty boxes.
- A post's title or summary contains markup-like text: it appears as plain text on the card; nothing in it is ever interpreted.
- A cover image that is very wide, very tall, tiny or unreadable: it is cropped or replaced by a plain background without distorting text.
- Many readers request the card for the same post at once: they all get the same picture, and the site's responsiveness for other pages does not degrade.
- A post is unpublished or its author is suspended after a card was made: nothing is stored on Antares, so there is nothing left to serve; pictures already saved on devices are out of Antares's reach.
- A reader with no JavaScript or a blocked sharing API still sees the post's plain link and a visible way to copy it.
- The feature is not shown to someone viewing a draft they do not own (no draft is ever shareable).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every published post MUST offer a Share control to any reader, signed in or not.
- **FR-002**: Share MUST use the device's native sharing sheet when the device offers one, and otherwise MUST offer copy link plus at least WhatsApp, X, Facebook, LinkedIn, Telegram and email.
- **FR-003**: Every shared link MUST be the post's canonical address and MUST NOT carry any identifier of the sharer or the reader.
- **FR-004**: Copy link MUST confirm success to sight and to assistive technology, and MUST work without the sharing sheet.
- **FR-005**: Antares MUST produce a 9:16 vertical picture for a published post from public data only: title, summary (or the post's opening text when there is none), author's display name, cover image when present, and the site name and short address.
- **FR-006**: The picture MUST be legible on a phone screen: text meets accessible contrast against its background in both light and dark card variants, and the longest allowed title and summary fit without overflow.
- **FR-007**: Long text MUST be shortened at a word boundary with an ellipsis; text MUST appear as plain text only.
- **FR-008**: Posts without a cover or summary MUST still produce a complete, good-looking picture.
- **FR-009**: On devices that can send a picture to an app, Story image MUST send the picture (and the link where accepted) through the native sharing sheet so the person can choose Instagram Stories or WhatsApp Status; on other devices it MUST save the picture and copy the link, and say what to do next.
- **FR-010**: The feature MUST NOT require any account, token or permission from Instagram, WhatsApp or any other social service, and MUST NOT post on a person's behalf.
- **FR-011**: The picture and Share MUST be offered only on a post page that is published and visible; a draft, unpublished or hidden post MUST show neither, and no address MUST exist from which a picture can be requested.
- **FR-012**: Producing the picture MUST NOT use the assistant (Hermes) or any AI service, MUST NOT read anything not already public, and MUST NOT reveal the author's account, profile, chat, run or email.
- **FR-013**: Making the picture MUST NOT cause any unbounded work or storage on the server (none at all is expected), and MUST NOT make any request beyond the cover image the page already shows.
- **FR-014**: The picture MUST reflect the post's current published title, summary and cover; after the author republishes, new requests get the new picture.
- **FR-015**: In the editor, the author MUST be able to preview the story card and the link preview for a draft from its current content, visible to the author only.
- **FR-016**: The Share control and story flow MUST work on phone, tablet and desktop, in light and dark themes, by keyboard and with a screen reader, and MUST NOT shift the reading layout.
- **FR-017**: A failure to make or send the picture MUST be shown in plain words with the link still copyable; it MUST NOT affect the post itself.
- **FR-018**: Antares MUST NOT add third-party scripts, trackers or buttons that load from a social service on the post page; sharing destinations are plain links or the device's sheet.
- **FR-019**: The feature MUST NOT add any new unauthenticated route; the picture is made on the reader's own device from what the post page already shows, so the project's inventory of public surfaces (constitution Principle I) is unchanged.

### Key Entities

- **Share card**: A derived 9:16 picture of one published post at one moment: title, summary or opening text, author's display name, cover, site name and address. It holds no data that the public post does not already show.
- **Share destination**: A route a reader picks (native sheet, copy link, a named service, email, story image); carries only the title and canonical link, or the picture.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A reader can open Share on a phone and be in the target app's compose screen in two taps from the post.
- **SC-002**: From a published post, a reader gets the story picture previewed in under 3 seconds on a typical mobile connection for 95% of attempts.
- **SC-003**: In a review of 20 varied posts (long and short titles, no cover, no summary, RTL and emoji), 100% produce a picture with no clipped, overlapping or empty text.
- **SC-004**: 0 pictures are produced for a post a reader may not see, and 0 pictures or links contain any account, email, chat or run identifier.
- **SC-005**: 100% of shared links are the plain canonical address.
- **SC-006**: A sustained burst of requests for the picture for one post does not slow other public pages by more than 10% in a controlled test.
- **SC-007**: The share controls pass the same keyboard, screen-reader and contrast checks as the post page, in both themes.

## Assumptions

- Instagram and WhatsApp offer no way for a web page to post a story directly or to attach a link sticker; the best a website can do is hand the picture to the device's sharing sheet, where the person picks the story option. Where the sheet cannot take images (many desktop browsers), the picture is saved instead. This limit is stated to the person, not hidden.
- "Summarize" means the post's own summary, which the author already controls, or the opening text when there is none; the picture is not written by an AI. Using the assistant to write a shorter story-specific blurb is out of scope and can be a later feature.
- Anyone who can read a post may share it and produce its card; sharing is not limited to the author.
- The card has one fixed design in two colour variants (light and dark) following the site's look, with no template picker in this version.
- The existing link previews (title, summary, cover) from the blog spec remain the way a shared link is displayed by other apps; this feature adds the Share control, the story picture and the author's preview.
- Share counts, analytics, scheduled posting and posting on a person's behalf are out of scope.
- The one-user trusted-use caveat from the project's isolation gate applies, as for the blog.
