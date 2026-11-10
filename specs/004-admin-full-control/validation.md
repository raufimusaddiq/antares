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

Not driven: SC-002 and SC-003 (timed tasks with a person); a real screen reader. Media generation cannot honour a switch at generation time (it runs in a Hermes
plugin), so "off" for image and video models is enforced by refusing to switch off a model that is a default.
