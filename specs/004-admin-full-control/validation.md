# Validation: Admin Full Control (spec 004)

Verified (2026-11-10): `go vet`, `go test ./...`, 900 web tests, the Python self-tests, and the full real-browser suite (405 checks: 320/390/820/1280 px, light and dark,
including a check that no button draws over another) before each merge. Production smoke after the takedown deploy: take down, public 404s, author view and notice,
restore, with synthetic fixtures (cleanup clean, real accounts untouched).

| Success criterion | How verified |
|---|---|
| SC-001 takedown hides every public way within a minute | `TestTakeDownHidesAPostFromEveryPublicSurfaceAndRestoreReturnsItUnchanged` (immediate; the 60 s `max-age` bounds browsers, images 5 min) and the production smoke |
| SC-004 no switched-off model is used | chat, title/summary and post-check tests (`admin_defaults_test.go`, `check_test.go`, `models_test.go`); media "off" is guard-enforced (the plugin runs inside Hermes) |
| SC-005 a purpose is never left without a model | `TestAModelThatIsADefaultCannotBeSwitchedOffAndNothingChanges` |
| SC-006 audit rows | `TestOnlyAnActiveAdministratorCanListTakeDownOrRestore`, `TestDefaultsPerPurposeAreSavedAuditedAndOnlyFittingModelsAreAccepted` |
| SC-007 admin never sees drafts, versions, checks, readers | `TestAdminBlogPostRoutesReturnOnlyPublishedFields`, `TestAdminSeesNoPostOrDraftContent`, `TestTheModelsReadListsEveryModelWithItsPurposesAndNoAccountField` |

Accessibility (T044), checked in a real browser by `ONLY=admin-a11y` (phone and desktop): every control on the new screens has an accessible name; every touch target
on a phone is at least 44 px (buttons, fields, selects, the switches as one label-wide target, the post links); focus moves into a form when it opens and onto
the post's heading after a take-down; a switch works from the keyboard and keeps focus while the screen reloads (a reload of the same screen no longer
blanks it); the banner is a named landmark with a `role="status"` message and no animation under reduced motion. Quickstart: steps 1-6 ran as the Go, web and
browser suites; step 7 ran as the two production smoke runs.

SC-002 and SC-003, timed on production on 2026-11-10 with synthetic accounts, driving the real screens in a real browser (search, open, type a reason, confirm; click a
switch). The clock is the software's and leaves out a person's thinking time, so these are floors, not a human trial: **SC-002** find a published post and take it
down: 1.9 s, 6 interactions (limit 60 s); the public address answered 404 as soon as the screen said so. **SC-003** switch a model off and it is gone from another
account's picker: 0.36 s (limit 30 s); switched back on, offered again; the block list was empty before and after.

Not driven: a person timing the tasks, and a real screen reader (VoiceOver/TalkBack), which no script here can stand in for. Media generation cannot honour a switch at generation time (it runs in a Hermes
plugin), so "off" for image and video models is enforced by refusing to switch off a model that is a default.
