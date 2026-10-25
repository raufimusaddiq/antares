# #145 model picker, verification

Reproduction and fix evidence. Browser: Chromium 1223 via playwright-core, against a disposable in-script
mock backend (`model-picker-repro.mjs`) serving the built bundle with the server's CSP; synthetic account,
no Hermes, no Kenari, no credentials, nothing production. It is evidence, not a CI check.

Run: `cd web && npm ci && npm run build && cd .. && OUT=docs/design/s145/shots CHROME=... PLAYWRIGHT_CORE=... node docs/design/s145/model-picker-repro.mjs`

## Reproduced on `origin/main` (b364e99) bundle, desktop 1280x800 and phone 390x780

Happy path works on both (open, load, select, persist, reload). The failures are on demand:

- A. One failed `GET /api/capabilities` (live Hermes discovery) leaves `inventory = null` for the whole page
  life: no picker, no message, no retry (`web/src/Chat.tsx` capabilities effect ran once, `[]` deps).
  `logs/before-fix-origin-main-bundle.txt`: `retry button: 0`, and the script waits forever for one.
- B. A failed `GET /api/models?refresh=1` on first open shows an alert with no Retry control; only
  close and reopen retries, which nothing says.
- C. A failed refresh on a later open set `catalog = null`: a good list on screen was thrown away
  (`web/src/ModelPicker.tsx` `setCatalog(r.ok ? ... : null)`).
- Also found: the chat's saved model read failing was swallowed (label stays "Model"); overlapping
  catalogue answers were applied in arrival order; `?refresh=1` refreshed Hermes only, and `fetchedAt` was
  the request time rather than the Kenari read time, so a catalogue served through a Kenari outage
  looked fresh.

Candidate causes from the brief that did **not** reproduce: New chat creates a real chat at once, so the
picker is there (under Options on phone, two taps); nothing overlays or blocks hit testing of the
summary or radios on either viewport.

## After the fix

`logs/after-fix.txt`: A Retry brings the picker; B Retry loads three models; select persists
(`m-alt`), reload keeps it; C a failed refresh keeps the three radios and says the list may be out of
date; a New chat has its own choice (`m-third`) and leaves the other chat's (`m-alt`) alone. Same on both
viewports.

## Tests (fail before, pass after)

- `web/src/ModelPicker.test.tsx` "ModelPicker on demand (#145)" (6) and `web/src/ModelGate.test.tsx`
  "an unreadable discovery can be retried" (2): 7 fail on the original sources, all pass after.
- `internal/kenari` `TestRefreshReadsKenariAgainButIsThrottled`, `TestSnapshotSaysWhenTheAnswerIsStale`,
  `internal/chat` `TestModelsSayHowOldTheCatalogueIs`: fail to build / fail before.
- Unchanged and passing: `TestModelChoiceIsPerChatValidatedAndSent` (the chosen model is what Hermes is
  asked to run), `TestModelSettingsAreScopedToTheOwner` (foreign chat settings denied).
