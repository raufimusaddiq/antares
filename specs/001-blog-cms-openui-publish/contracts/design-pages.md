# Contract: pages to design in figcraft first

Per AGENTS.md "Design comes first", these are drawn in `docs/design/antares.figcraft` and reviewed **before**
code. Build from the existing variables and styles (`Antares` collection, Light/Dark; Title/Label/Shadow
styles). Each page needs Light + Dark and Phone (390) + Desktop (1280); Tablet where the layout changes.
Add the new pages to `docs/design/README.md` and wire click flows as prototype interactions.

## New figcraft pages

### A. Blog · Public (signed out)
| Frame | States to draw |
|-------|----------------|
| Blog index | populated (cards: cover, title, summary, author, date); empty; loading; error |
| Post · text only | headings, quote, list, code, image, divider |
| Post · enriched | sortable table, chart with hover detail, metric cards, accordion, sources list with retrieved-on date; AI-added marker visible |
| Post · block fallback | one block degraded to plain text, rest unchanged |
| Not found | unknown / unpublished / hidden author: one identical screen |

### B. CMS · My posts (signed in)
| Frame | States |
|-------|--------|
| Post list | populated with every status chip (draft, checking, ready to review, published, unpublished); empty; loading; error |
| Row actions | open, duplicate, unpublish, delete |
| Delete confirm | confirm dialog |
| Entry point | where "Blog" lives in the existing sidebar/menu, and the hidden state when the capability is unavailable |

### C. CMS · Editor
| Frame | States |
|-------|--------|
| Editor | empty; writing with toolbar (headings, bold, italic, link, list, quote, code, image, divider); selection toolbar |
| Save status | saving, saved, offline/unsaved, **conflict** (two versions side by side) |
| Image upload | uploading, failed (type/size), inserted |
| Post settings sheet | title, summary, cover, address with conflict error |
| Phone | toolbar placement with the keyboard closed |

### D. Publish flow
| Frame | States |
|-------|--------|
| Publish entry | the Publish button and the choice "Check and enrich" vs "Publish my original" |
| Checking | progress, what it is doing, Cancel; draft visibly locked-from-overwrite but editable |
| Preview | original vs enriched, notes panel, AI-added badge on each block, per-block Accept / Reject, sources drawer per block with verified/unverified, "Accept all", "Publish my original" |
| Unverified source | explicit acknowledgement required before accepting |
| Failed / timed out / cancelled / disconnected | each worded as in `check-envelope.md`, with Retry and Publish original |
| Out of date | only for old checks; since spec 006 a draft change no longer ends a check |
| Rejected block notice | markup refused, text kept |
| Published | success with the public address, Copy link, View post, Unpublish |
| Unavailable | assistant or web search not configured: Publish original only, with the reason |

### E. Admin (existing page)
| Frame | States |
|-------|--------|
| Suspended user row | note that their published posts are hidden until reinstated; **no** post or draft content shown (admin blind) |

## Click flows to wire (prototype interactions)

1. Sidebar Blog → Post list → New post → Editor → (type) → Publish → Checking → Preview → Accept all → Published → View post → Public post.
2. Preview → Reject one block → Published (without it).
3. Preview → Publish my original → Published.
4. Checking → Cancel → Editor (cancelled notice).
5. Checking → failed state → Retry / Publish original.
6. Editor → conflict → choose version.
7. Post list → row → Unpublish; → Delete → confirm.
8. Public index → Post → Back.

## Out of scope for design

Comments, subscriptions, reader accounts, scheduled publishing, multi-author editing (spec Assumptions).
