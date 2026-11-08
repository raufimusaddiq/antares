# Quickstart: validating the blog end to end

Prerequisites: a dev instance with the mock Hermes used by the existing harness, two test users (A and B,
both ACTIVE), and a signed-out browser. Commands match the repository's usual ones; see `Makefile`.

```bash
go test ./internal/blog/... ./internal/server/...
(cd web && npm test && npm run build)
node scripts/browser-check.mjs   # ONLY=blog-public,blog-editor to focus
```

## Scenarios (each maps to spec stories and success criteria)

| # | Scenario | Expected | Covers |
|---|----------|----------|--------|
| 1 | A writes a formatted post with an image, closes the tab, reopens | Draft intact; image still there | US1, FR-002, SC-006 |
| 2 | A publishes the original; open `/b/{slug}` signed out | Same content; appears in `/b` | US1, FR-019 |
| 3 | A presses Publish with enrichment (mock Hermes returns a valid envelope) | Preview shows AI-added blocks with sources and dates; original text unchanged | US2, US3, FR-008..010, FR-015 |
| 4 | A rejects one block, accepts the rest | Published post lacks the rejected block | US2, FR-009 |
| 5 | Mock Hermes returns a block with script, an unknown component or an `@ToAssistant` action | Block rejected with a notice; never reaches the public API | FR-011, FR-020, SC-004 |
| 6 | Mock Hermes returns a table with no sources | Block dropped, `unsourced` note shown | FR-016, SC-008 |
| 7 | Mock Hermes fails / times out / A cancels | Each shown as itself; nothing published; Retry and Publish original offered | FR-012, SC-010 |
| 8 | A edits during a running check | Check becomes out of date; results not applied | FR-013 |
| 9 | B requests A's draft, post, check, block and asset by id | Identical `404`, no change | FR-006, SC-005 |
| 10 | Admin suspends A | A's posts vanish from `/b` and their addresses 404; reinstating restores them unchanged | Q3 |
| 11 | Admin views any admin screen/API | No draft or post content anywhere | Principle I |
| 12 | Enumerate unauthenticated routes | Exactly the existing set plus the five public blog routes | Complexity Tracking |
| 13 | Public post at 390 px wide, light and dark, keyboard only | No sideways scroll; every block reachable | FR-022 |
| 14 | One block forced to fail rendering | Plain fallback appears; rest of post fine | FR-021 |
| 15 | Hermes or web search not configured | Blog entry shows only what works; enrichment offered as unavailable | FR-029 |

## Before Stage 2 starts (blocking)

Record fixtures from the deployed Hermes (keys redacted): a run created without a chat, its tool events for a
web search, and its final output. Update `check-envelope.md` to the real shape. Until then, Stage 2 uses
the mock only and the PR must say so.
