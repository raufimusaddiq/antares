# Quickstart: validating the writing rules

1. **Tests**: `go test ./...` and `npm test` in `web/`: the new tests (rules attached and ordered, switch, counts, `style` notes, guards) fail before and pass after.
2. **Chat**: a synthetic account asks for an email draft and for an explanation. The draft has no em dash and no word from the list; the explanation is unchanged in style. Switch the rules off as administrator: the next draft is a baseline. Switch on again.
3. **Existing and new accounts**: both are covered by the same run (no backfill); confirm with an account created before the deploy and one after.
4. **Post check**: run the check on a clean draft, a draft full of filler and a draft with a made-up statistic: style notes only on the filler draft; no proposed block carries an unsourced number; the draft is unchanged; Publish stays available.
5. **Admin card**: shows on, `v1`, the credit and the counts; switching off and on is audited; a run started with the rules off shows as "without" and, with the setting on, produces the warning.
6. **Browser**: `ONLY=writing-rules node scripts/browser-check.mjs` at 320/390/820/1280, light and dark, including the no-overlap check.
7. **Quality run (production, synthetic accounts)**: the fixed prompt set with and without the rules (SC-001, SC-003, SC-004), counted with regular expressions; results and the measured size of the rules in `validation.md`.
8. **Production** (after merge): backup, deploy, smoke with fixtures; cleanup; never touch real accounts.
