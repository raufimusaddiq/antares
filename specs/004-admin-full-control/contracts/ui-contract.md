# UI contract (drawn in `docs/design/antares.figcraft`, page "Admin control", before code)

Reused components (no new ones planned): state chip, table-to-cards on phone, confirm form with required reason (as Suspend), secondary white pill and cobalt pill (spec 003), quiet outline button, status banner.

| Screen | Entry | States to draw (light + dark, 1280 + 390) |
|--------|-------|-------------------------------------------|
| Admin → Posts | new admin nav item "Posts" | list, filtered, search no result, loading, error with retry, empty |
| Post view | row title | visible, hidden by account, taken down; take-down confirm (reason required, 500 counter, busy, error); restore confirm; already-taken-down notice |
| Admin → Models | new admin nav item "Models" | switch rows on/off with usage and "default for" chips; purpose defaults (5) with warning; refused switch-off naming purposes; catalogue unavailable; saving; saved |
| Author posts page | existing | row "Taken down" with reason, publish disabled with reason, edit allowed |
| Author banner | app shell, once | one takedown, several, dismissed |

Accessibility: switches are real checkboxes with names; banner is `role="status"`; focus returns to the row after a confirm; all targets ≥ 44 px on phone; reduced motion honoured.

## Drawn frames (approved 2026-11-10)

Page "Admin control" in `docs/design/antares.figcraft`, listed in `docs/design/README.md`. Components: state chip (Visible mint,
Hidden · account periwinkle, Taken down salmon), switch (46 x 26: mint on, rail off, ink knob), select pill (ink 2 px border,
warning variant in the orange chart colour), confirm form (as Suspend), alert strip (salmon for a refusal, mint for a result),
quiet pill, cobalt pill. Admin Posts and Models are new nav items between Users and Capabilities. Dark versions exist for the main
frames only; the others reuse the same components.
