# Validation: Built-in writing rules (spec 005)

Verified before the first merge of the implementation (2026-11-10): `go vet`, `go test ./...`, 905 web tests (including the new style-note and admin card tests), the
Python self-tests, and the real-browser views `blog-editor` (a style note shown, dismissed, Publish still enabled, 44 px targets, no overlap) and `writing-rules`
(320/390/820/1280 px, light and dark: state, version, credit, counts, the warning, switch from the keyboard keeping focus, a refused save, no control over another).

The rules text is 2,194 characters (limit 3,000), pinned by `internal/writing/writing_test.go`.

| Success criterion | How verified |
|---|---|
| FR: every account's run carries the rules first | `TestRunsCarryTheWritingRulesFirst` (an account that existed before and one created after; order rules, OpenUI, personality) |
| Off means off, recorded as `off` not NULL | `TestSwitchedOffRunsRecordOffNotNull` |
| The text cannot be loaded: chat still works, NULL recorded (SC-006) | `TestChatWorksWhenTheRulesTextIsUnavailable`, `TestACheckCarriesTheWritingRulesAndRecordsIt` |
| Summaries use the same text, the old inline paragraph is gone | `TestSummaryUsesTheSharedRules` |
| Post check: `blog-check v2`, rules and style instruction only when on, `style` notes accepted, publishing unaffected, draft unchanged | `TestACheckCarriesTheWritingRulesAndRecordsIt`, `TestAStyleNoteIsShownAndDoesNotStopPublishing`, `TestAStyleNoteIsAccepted` |
| Admin only, strict body, audited, counts only | `internal/auth/admin_writing_test.go`; the route is in the admin-blindness scan (`scanned()`) |
| "Not applied" never from switched-off runs or runs before migration 0029 | `TestWritingRulesCounts`, `TestTheNotAppliedWarningNeedsRunsThatShouldHaveHadTheRules` |
| Migration upgrades an existing database with all runs NULL | `TestWritingRulesMigrationUpgradesWithNullColumns` |

Not yet driven (follow-up after the deploy, recorded here when done): the production quality run with synthetic accounts (T027: writing and non-writing prompts, the four
edge prompts, counts of em dashes and banned words with and without the rules) and the quickstart on production (T028, T029 smoke). A model's behaviour under the rules is evidence, not a guarantee.
