# Feature Specification: Blog CMS with AI-Enriched Publishing

**Feature Branch**: `001-blog-cms-openui-publish`

**Created**: 2026-11-08

**Status**: Draft

**Input**: User description: "i want antares serve my blog too, it has cms page for user, content write page native support like wordpress or medium. but the difference is when user want to publish the page, the writing is being check by llm, and llm put the native styled OpenUI to the page and add the data based on web search (may using hermes too). so the user published has some data or more interactive with open ui"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Write and publish a plain post (Priority: P1)

An approved user opens a writing page inside Antares, composes a post with a title, rich text (headings, emphasis, lists, quotes, links, images, code) and saves it as a private draft. Drafts autosave and can be resumed later. When ready, the user publishes the post and it appears at its own public address and in the blog index, exactly as written.

**Why this priority**: Without a working write-and-publish loop there is no blog. This slice is a complete, useful product on its own (a Medium-style writing tool) before any AI step exists.

**Independent Test**: Sign in as an approved user, write a post with formatting and an image, close the browser, return and find the draft intact, publish it, then open the public address in a signed-out browser and see the same content.

**Acceptance Scenarios**:

1. **Given** an approved user on the writing page, **When** they type and format content, **Then** the draft is saved automatically and survives closing and reopening the page.
2. **Given** a saved draft, **When** the user publishes it, **Then** the post is reachable at a stable public address and listed on the blog index.
3. **Given** a published post, **When** the author edits it and republishes, **Then** readers see the new version at the same address.
4. **Given** a published post, **When** the author unpublishes it, **Then** its public address no longer shows the content and it leaves the blog index.
5. **Given** a user who is not approved, suspended or signed out, **When** they try to open the writing page or any draft, **Then** access is refused.

---

### User Story 2 - Review before publishing: AI check and enrichment (Priority: P1)

When the author chooses to publish, Antares first has the assistant read the writing. The assistant checks it (clarity, factual claims it can verify, missing context) and proposes an enriched version: native, site-styled interactive blocks (for example comparison tables, charts, metric cards, collapsible sections, source lists) placed where they support the text. The author sees a preview of the enriched post next to their original, and decides what is published.

**Why this priority**: This is the feature's reason to exist and what separates it from WordPress or Medium. It is P1 together with Story 1 but is independently testable once a draft exists.

**Independent Test**: Take a draft that mentions a comparison and a few numbers, press Publish, and verify a preview appears with at least one interactive block that the original text did not contain, with every proposed figure marked as AI-added; verify the original text is untouched until the author accepts.

**Acceptance Scenarios**:

1. **Given** a draft, **When** the author presses Publish, **Then** they see a visible "checking and enriching" state, and the draft stays editable and unchanged underneath.
2. **Given** the check has finished, **When** the preview opens, **Then** the author sees the proposed post with each added block clearly marked as AI-added, plus the assistant's notes on the writing.
3. **Given** the preview, **When** the author accepts all, **Then** the enriched post is published.
4. **Given** the preview, **When** the author rejects an individual block, **Then** that block is removed and the rest is kept.
5. **Given** the preview, **When** the author chooses "publish my original", **Then** the post is published without any AI-added block.
6. **Given** the check fails, times out or is cancelled, **When** the author looks at the result, **Then** it is shown as failed, nothing is published, and the author can retry or publish the original.

---

### User Story 3 - Data in enriched blocks comes from sources (Priority: P2)

Where the assistant adds data (figures, charts, tables, quotes of facts), it gathers that data from web search through the assistant, and every data-bearing block carries visible sources and the date the data was retrieved. Data the assistant could not source is not added.

**Why this priority**: It makes enrichment trustworthy. Interactive blocks without honest data would be decoration, but the publish flow can ship first with blocks that use only the author's own numbers.

**Independent Test**: Publish a draft that makes a claim about a public statistic; verify the resulting block shows the figure, its source links and a retrieved-on date, and that a claim with no findable source produces a note to the author instead of an invented figure.

**Acceptance Scenarios**:

1. **Given** a draft claim that can be sourced, **When** the check runs, **Then** the proposed block shows the data with at least one source link and a retrieved-on date.
2. **Given** a claim that cannot be sourced, **When** the check runs, **Then** no figure is invented; the author receives a note instead.
3. **Given** a published enriched post, **When** a reader opens a data block, **Then** they can see where the data came from and when.
4. **Given** the author edits the text after enrichment, **When** they republish, **Then** the check runs again on the changed post and blocks tied to removed or changed text are re-proposed or dropped, never left stale.

---

### User Story 4 - Readers read and interact with the published post (Priority: P2)

Readers, without needing an account, open a published post and see the author's text and the native interactive blocks (sortable tables, toggles, charts with hover detail, expandable sections). The page is fast, readable on phones, supports light and dark themes, and still reads correctly if a block cannot be shown.

**Why this priority**: Delivers the visible payoff for readers, but depends on Stories 1 and 2.

**Independent Test**: Open a published enriched post on a phone-width screen signed out; interact with each block; then simulate a block that fails to render and confirm the text remains readable with a plain fallback.

**Acceptance Scenarios**:

1. **Given** a published enriched post, **When** a signed-out reader opens it, **Then** text and interactive blocks display and respond to input.
2. **Given** a block the page cannot display, **When** the post loads, **Then** a plain fallback of that block's content appears and the rest of the post is unaffected.
3. **Given** a reader using only a keyboard or a screen reader, **When** they move through the post, **Then** every block is reachable and understandable.
4. **Given** a reader on a phone, **When** they open the post, **Then** the layout fits the screen without sideways scrolling.

---

### User Story 5 - Manage my posts (Priority: P3)

The author has a CMS list of their own posts with status (draft, checking, ready to review, published, unpublished), last-edited time, and actions to open, duplicate, unpublish or delete. Authors can set a title, summary, cover image and address for each post.

**Why this priority**: Needed for ongoing use, but a single post can be published without it.

**Independent Test**: Create three posts in different states and confirm the list shows the right status and that each action works and affects only the chosen post.

**Acceptance Scenarios**:

1. **Given** an author with several posts, **When** they open the CMS list, **Then** they see only their own posts with correct status.
2. **Given** a post in the list, **When** the author deletes it after confirmation, **Then** it disappears from the list and its public address stops working.
3. **Given** two authors, **When** one tries to open, edit or delete the other's post by guessing its identifier, **Then** the request is refused and nothing changes.

---

### Edge Cases

- The assistant is unavailable or the author's assistant binding is unhealthy: publishing the original remains possible only if the author chooses it explicitly; nothing is enriched, and the failure is shown plainly.
- The assistant returns malformed or oversized block markup: those blocks are dropped with a notice; the author's text is never lost.
- The assistant returns markup containing script, raw HTML, unknown components or links to unapproved destinations: rejected, shown as a rejected block to the author, never published.
- The author changes the draft while a check is running: the running check is marked out of date and results are not applied to the changed text without a rerun.
- Web search returns conflicting or low-quality sources: the author is told, and the block is not added unless the author accepts it explicitly.
- Two browser tabs edit the same draft: the later save does not silently overwrite the earlier one; the author is told about the conflict.
- Very long posts, many images, or many blocks: limits are stated to the author before publishing, not after a failure.
- Two posts want the same address: the author is asked to pick a different one.
- An author is later suspended or revoked: their published posts are hidden from readers (public addresses stop showing content and the posts leave the blog index) until the account is reinstated, then reappear unchanged; the administrator does this by suspending the account and never reads the posts or drafts
- A published post contains a block whose data has become out of date: the retrieved-on date stays visible; no silent refresh changes a published post.

## Requirements *(mandatory)*

### Functional Requirements

**Writing and CMS**

- **FR-001**: Approved users MUST be able to create, edit, preview and delete posts in a built-in writing page offering headings, bold/italic, lists, quotes, links, code, images and dividers.
- **FR-002**: The system MUST autosave drafts and restore the latest saved state after the page is closed, refreshed or the connection drops.
- **FR-003**: Drafts MUST be visible only to their author; no other user, including an administrator, can read a draft.
- **FR-004**: Authors MUST be able to set a post's title, summary, cover image and public address, and be told of address conflicts before publishing.
- **FR-005**: Authors MUST be able to publish, republish, unpublish and delete their own posts.
- **FR-006**: Every create, read, update and delete of a post MUST be restricted to its author; requests for another user's post MUST be refused without revealing whether it exists.

**AI check and enrichment**

- **FR-007**: Pressing Publish MUST start an assistant check of the draft before anything becomes public, unless the author explicitly chooses to publish the original. (Built as a dialog over the editor: "Check and enrich" or "Publish my original".)
- **FR-008**: The check MUST produce notes on the writing and a proposed set of interactive blocks placed within the post, without modifying the author's original text.
- **FR-009**: The author MUST see a preview of the enriched post and be able to accept all, accept or reject each block individually, or publish the original.
- **FR-010**: Every AI-added block MUST be visibly marked as AI-added to the author in the preview and to readers on the published post.
- **FR-011**: Blocks MUST be built only from the site's approved set of native styled components; markup using anything else MUST be rejected and never published.
- **FR-012**: A check that fails, times out, is cancelled or loses its connection MUST be shown as exactly that, MUST NOT publish anything, and MUST offer retry or publish-original.
- **FR-013**: Editing a draft during a check MUST invalidate that check's results until it is rerun.
- **FR-014**: The check MUST be performed by the assistant the author is already bound to; no other user's assistant context, history or memory is used.

**Sourced data**

- **FR-015**: Data the assistant adds MUST come from web search performed through the assistant, and every data-bearing block MUST show its sources and a retrieved-on date. Sources are shown as unverified (the assistant's run exposes the search query, not what a search returned), and the author MUST tick "I have checked these sources myself" for a block before it can be accepted.
- **FR-016**: The system MUST NOT present a figure as sourced when no source was found; unsourced claims MUST yield an author-facing note rather than an invented value.
- **FR-017**: The author MUST be able to see, per block, which sources were used before accepting it.
- **FR-018**: Published data MUST NOT change after publication unless the author republishes.

**Reading**

- **FR-019**: Published posts and the blog index MUST be readable by anyone on the internet without an account. This is an unauthenticated read surface, alongside the shared-chat link (constitution Principle I, v1.1.0): it serves published, non-hidden posts and their published assets, and nothing else; drafts, chats, memory and every other route remain default-deny.
- **FR-020**: Readers MUST be able to interact with native blocks (for example sort, filter, expand, hover for detail) without being able to trigger any action that reads or changes account data, chats, memory or files.
- **FR-021**: A block that cannot be rendered MUST fall back to a plain readable version of its content without breaking the rest of the post.
- **FR-022**: Published pages MUST work on phone, tablet and desktop, in light and dark themes, and be usable with keyboard and screen reader.
- **FR-023**: Blog index and posts MUST include page titles, summaries and sharing previews so that links shared elsewhere show the title and cover image.

**Access and safety**

- **FR-024**: Writing MUST be available to every approved user, with no separate author role.
- **FR-025**: Pending, rejected and suspended users MUST NOT be able to write, check or publish.
- **FR-026**: Pasted or uploaded content MUST be handled so that scripts or raw markup in a post's text can never execute for any reader or author.
- **FR-027**: Uploaded images MUST be limited in type and size, and an image attached to a draft MUST NOT be reachable by anyone but its author until the post is published.
- **FR-028**: Credentials, internal addresses and other users' data MUST NOT appear in any post, block, preview, note or error.
- **FR-029**: The blog, writing and check features MUST each show as unavailable, rather than appear to work, when the assistant or its web search capability is not configured.

### Key Entities *(include if feature involves data)*

- **Post**: A piece of writing owned by one author. Has title, summary, cover image, public address, status (draft, checking, ready to review, published, unpublished) and timestamps.
- **Draft content**: The author's own text and formatting; never altered by the assistant.
- **Version**: A saved state of a post; the published version is a specific version the author approved.
- **Check**: One assistant review of a specific version: its state (running, ready, failed, cancelled, out of date), notes and proposed blocks.
- **Block**: A native interactive component proposed or accepted for a post: kind, position in the post, content, AI-added marker, accepted or rejected state.
- **Source**: A web reference supporting a block's data: link, title, retrieved-on date.
- **Image/asset**: A file attached to a post, private until its post is published.
- **Author**: An approved Antares user who owns posts.
- **Reader**: Any visitor who opens a published post.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An author can write a short post and publish the original in under 3 minutes from opening the writing page.
- **SC-002**: For a typical post (up to 1,500 words), the enriched preview is ready within 2 minutes in at least 90% of attempts, and the author can see progress throughout.
- **SC-003**: 100% of data-bearing blocks in published posts show at least one source and a retrieved-on date.
- **SC-004**: 0 published posts contain a block outside the approved set, and 0 reader-triggered interactions read or change any account, chat, memory or file.
- **SC-005**: 0 attempts by one user to read, edit, publish or delete another user's draft or post succeed, across all post operations.
- **SC-006**: A draft is never lost: after a closed tab, crash or dropped connection, at least the last 5 seconds of work before the interruption is restored.
- **SC-007**: A published post opens and becomes readable in under 2 seconds on a typical mobile connection for 95% of loads.
- **SC-008**: In a review of published enriched posts, no block shows a figure without a listed source and a retrieved-on date, and no block with sources was accepted without the author's acknowledgement. (Sources are never marked verified.)
- **SC-009**: At least 80% of authors who try the enrichment step accept at least one proposed block.
- **SC-010**: When the assistant is unavailable, 100% of authors see a clear failure state and none have a post published as checked.

## Assumptions

- Writers are signed-in, approved Antares users; the existing Google sign-in and administrator approval remain the only way in.
- The blog is part of the same Antares deployment (same sign-in, same footprint) rather than a separate service.
- The assistant (Hermes) is the only source of the check, the block markup and the web-searched data; Antares adds no second AI pipeline of its own, and the author's own assistant binding performs the check.
- Interactive blocks use the same native styled component set as Antares chat; a block catalogue specific to the blog may be a subset or extension of it, to be settled in planning.
- Administrators can approve, revoke and suspend accounts but cannot read drafts; they have no takedown of a public post in v1 (research R8): the only remedy is suspending or rejecting the author, which hides their posts. A takedown is a candidate for a later spec.
- Publishing is always an explicit author action; nothing is published automatically.
- Comments, reader accounts, subscriptions, multi-author collaboration, scheduled publishing, paid content and an importer from other CMSs are out of scope for this feature.
- Post text language is the author's choice; the assistant checks in the language written.
- The one-user trusted-use caveat from the project's isolation gate applies: the feature MUST NOT be described as safe for mutually untrusted authors until that gate is demonstrated.
