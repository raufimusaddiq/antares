# Runbook: built-in writing rules (spec 005, issue #331)

## What is built in

One text, `writing-rules v1` (`internal/writing/rules.md`, at most 3,000 characters), condensed from the copywriting rules of
anti-slop (github.com/miqdadbadjuber/anti-slop, MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)). The credit is part of the
text and a test pins it. It asks for plain writing in what the assistant writes *for* the person (no em dashes, no filler or
hype words, no invented numbers, quotes or sources). It does not apply to answers, explanations, code, tables or quotations,
and the person's own saved instructions about tone win.

It goes to every account the same way: as the first part of the run `instructions`, before the OpenUI instruction and the
personality. Summaries (shared picture) use the same text. The post check adds a short paragraph and may return notes of kind
`style`, which the author can dismiss in the browser (nothing is stored). Titles are not covered.

## Why run instructions and not a Hermes skill

Antares must not write Hermes data directories beyond profile create and delete (constitution, principle II). Run instructions
are already how personal instructions and OpenUI reach Hermes, so nothing new is written and every account, old or new, gets
the rules on its next run.

## Changing the text

Never edit it in place. A change is a release: edit `rules.md`, bump `Version` in `internal/writing/writing.go` and the first
line of the text, update `contracts/rules-text.md` in the spec, and the invariants test.

## The switch

Admin → Capabilities → "Writing rules" (`GET/PUT /api/admin/writing-rules`). No row means on. A change applies to the next
reply or post check and is audited as `writing_rules.set` (detail `enabled=true|false`). A failing read of the setting never
switches the rules off.

## "Not applied"

`runs.writing_rules` and `post_checks.writing_rules` hold `v1` (attached), `off` (switched off on purpose) or NULL (expected
and not attached, or older than migration 0029). The card warns only for NULL on rows created after migration 0029 was applied.
Counts cover chat runs and post checks of the last 24 hours, not titles and summaries, and never name a person, chat or post.
If the text cannot be loaded, chat still works without it and the card shows the warning.
