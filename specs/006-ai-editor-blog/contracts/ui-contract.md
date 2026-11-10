# UI contract (drawn in `docs/design/antares.figcraft`, page "AI editor", before code; light + dark, 1280 + 390)

| Screen | States to draw |
|--------|----------------|
| Editor toolbar | each button on / off / mixed (bold partly applied), focus, pressed; phone: wraps without overlap, 44 px |
| Editor with the panel closed | the "Check with the editor" button in the top bar next to Post settings and Publish (phone: in the bar's overflow row, never covering Publish) |
| Panel: progress | step list (Reading your post / Looking things up, N lookups / Writing suggestions) with done, current, pending; elapsed time; "Last sign of life 4 s ago"; Cancel; stalled ("Still working, nothing new for 45 s"); timed out; failed; cancelled; limit reached |
| Panel: suggestions | a `replace` card (the passage, the proposed text, the reason, Accept, Reject), an `insert` card ("Written by the assistant", the proposed paragraphs, Accept, Reject), accepted (with Undo), rejected, no longer applicable, none ("Nothing to suggest"), too little to edit |
| Review screen at Publish | the same progress during the check; suggestions listed with the notes; blocks as today; skipped-block sentence |
| Phone | panel below the draft as a sheet from the bottom, not covering the toolbar; progress and cards at 390 and 320 |

Reused components: the notes list and chips, the notice card, the quiet pill, the AI-added chip ("Written by the assistant" is the same chip). New components: step list, suggestion card, mixed toggle state.
Accessibility: toggle buttons expose `aria-pressed` true/false/mixed and a name; a polite live region announces only a step change, the clock is silent; Accept/Reject/Undo are named for the passage ("Accept suggestion 3"); focus moves to the next card after a decision, and to the panel heading when it opens; reduced motion removes the card entrance; all targets at least 44 px on a phone.
