# Antares design

`antares.figcraft` is the design source of truth. Open it in FigCraft (File > Open).

It started as a one-time capture of the web UI (`web/src`). From here, changes start in the design file and are then
built in code. Nothing regenerates the file from code.

Pages: Design system (colour and number variables with Light/Dark modes, text and effect styles, components),
Chat, Dialogs, Settings, Admin, Account states, Phone, Tablet, Phone landscape. Frames are bound to the `Antares`
variable collection; dark frames switch the collection's mode. Clickable flows between frames are prototype
interactions (use Present).

Known gaps: only states the mock backend could show were captured (no live Hermes). Text is placed per rendered line, so
it does not reflow when edited. Not yet drawn: most Notifications and Jobs states, Memory and Skills states, model
picker states, delete-failed and pinned rows, dictation consent, the recorder, share-link variants, the keyboard-open
phone layout, and keyboard shortcuts.
