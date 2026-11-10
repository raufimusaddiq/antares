# UI contract (drawn in `docs/design/antares.figcraft`, page "Writing rules", before code)

| Screen | Entry | States to draw (light + dark, 1280 + 390) |
|--------|-------|-------------------------------------------|
| Admin → Capabilities, card "Writing rules" | existing Capabilities nav item | on (version, credit, last 24 h: N with, 0 without), switched off, on with "rules were not applied to N runs" warning, saving, error |
| Post check result, style notes | existing preview screen of the publish flow | one style note, several, none, a note dismissed; kind label "Style", where it is, Dismiss button |

Reused components: the switch (spec 004), the notes list and kind chips of the preview screen, the notice card, the quiet pill. Accessibility: the switch is a named checkbox (role switch) in a 44 px label; Dismiss buttons are named ("Dismiss this note"); focus moves to the next note after a dismissal; targets are at least 44 px on a phone.

## As approved (owner, 2026-11-10)

Drawn states: Capabilities card on / off / on with the warning / save failed (desktop; phone and dark: on); style notes with three, none, one dismissed (desktop; phone and dark: with notes). "Saving" and "saved" are not separate frames: the switch is disabled while saving and the card shows no other change. Warning text: "The rules were not applied to N runs since this feature shipped." Off counts read "with the rules / without (switched off)". New components: none (chip "Style" reuses the pink chip).
