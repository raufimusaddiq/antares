# Turn controls: steer, regenerate, edit, rewind (PRD §21.5, UXP-010/011, issue #84)

Evidence was read, read-only, from the deployed Hermes container (`nousresearch/hermes-agent:latest`, image
id below) under `/opt/hermes/gateway/platforms/`. No Hermes state was changed. Line numbers are of that
image; re-check them when Hermes is upgraded (`scripts/hermes_contract.py` covers the capabilities list, not
these handlers).

Image id at the time of reading: `sha256:9c5561579f3ffdf333401797094ac3a743738d6c25d40f4331c7bf77b694238d`.

| Operation | Hermes route | Evidence | Antares |
|---|---|---|---|
| **Steer** a running run | `POST /v1/runs/{run_id}/steer`, body `input`/`message`/`text`; 200 `{"object":"hermes.run.steer","accepted":true}`; 409 `run_not_accepting_steer` when the run is not `running` or the agent declines | route row `api_server_runs.py:247`; handler `api_server_runs.py:1398-1449` (status gate `:1413`, refusal `:1393-1395`, `agent.steer` `:1442`); advertised as `features.run_steer` in `docs/hermes/fixtures/capabilities.body.json:29,143` | **Built.** `POST /api/runs/{runId}/steer` |
| **Regenerate** an answer | **none** | The whole route table is `api_server.py:1646-1692` (`_http_route_table`): sessions CRUD, `/messages` (GET only), `/fork`, `/chat`, `/chat/stream`, `/model`; `/v1/runs*`; `/v1/responses*`; jobs. No retry/replay route. A second `POST /v1/runs` is not a regenerate (PRD §21.5). | **Not possible.** `POST /api/chats/{chatId}/regenerate` → 501 `CAPABILITY_UNAVAILABLE` |
| **Edit** a prior user turn | **none** | Same table. `/fork` (`api_server.py:3260-3295`) copies the *entire* message list (`db.get_messages` `:3286` → `db.replace_messages` `:3287`) and ends the source as `branched` (`:3285`). It takes no turn/message id, so it cannot branch at a turn. | **Not possible.** `POST /api/chats/{chatId}/edit-turn` → 501 |
| **Rewind** / delete last turns | **none** | Same table: no `DELETE`/`PATCH` on `/messages`; grep of `api_server*.py` for rewind/truncate/delete-message/undo finds no handler. | **Not possible.** `POST /api/chats/{chatId}/rewind` → 501 |
| Fork a whole chat | `POST /api/sessions/{id}/fork` | `api_server.py:3260-3295` | Not built here (not rewind/edit; it also ends the source session, a side effect that needs its own ticket). `POST /api/chats/{chatId}/fork` stays a 501 stub. |

## What the user sees instead

- Regenerate / edit / rewind answer `501 CAPABILITY_UNAVAILABLE` ("Hermes has no way to … a turn; start a
  new chat instead"). The UI must show them disabled with that reason, never as a working button.
- Antares does **not** emulate them: no row deletion, no direct `state.db` or transcript write, no second
  `POST /v1/runs` dressed up as a retry, no Python on the Hermes side. Copying a prompt into a new chat is the
  user's own action and needs no API.
- The routes exist (rather than being left as bare guard stubs) so ownership is checked first: another
  user's chat id is `404`, byte-identical to an id that never existed, and Hermes is never contacted. Only
  the owner learns of the 501.

## Capability flags (issue #109)

`GET /api/capabilities` lists `regenerate`, `edit_turn` and `rewind` with `status: unsupported` and a reason
(`internal/hermes/discovery.go`, `TestDiscoverTurnControlsAreUnsupportedWithAReason`). The client's `offers()`
therefore returns false for them, and the web app renders Regenerate, Edit and Rewind as `aria-disabled` buttons with no handler, each with its reason visible beside it (`TurnControls` in `web/src/Chat.tsx`; newest user turn gets Edit, newest assistant turn gets Regenerate and Rewind; wraps on narrow screens). They become `available`
only when a verified Hermes route (fixture + file:line in the table above), a handler that uses it, and the
destructive-operation guards (confirmation, active-run refusal, search-index invalidation, metadata-only audit
event, ownership re-check) land together. A whole-session `/fork` or a second `POST /v1/runs` does not qualify.

**Upstream status:** regenerate, edit-turn and rewind are `UPSTREAM_BLOCKED` on the deployed Hermes.

## Steer semantics

- Ownership: `store.GetRun(userID, runID)`; a foreign run is `404` before Hermes is contacted.
- The stored run must be non-terminal and have a Hermes run id, else `409 STEER_REFUSED` without a Hermes call.
  Hermes' own `409` (also `run_not_accepting_steer`) maps to the same code. A steer that arrives too late is
  therefore a visible refusal, never a silent success (QUEUE-004).
- Only the backend picks profile and run id; the body accepts `input` alone and unknown fields are a `400`.
- "Landed" = Hermes returned `accepted:true`. Antares then emits `run.steered {runId, accepted}` on the run's
  event journal. It appends nothing to the transcript and does not echo the guidance text; the run's
  status is unchanged.
- Capability: `GET /api/capabilities` lists `steer`, available only when Hermes reports `features.run_steer` and
  advertises `POST /v1/runs/{run_id}/steer` (`internal/hermes/discovery.go`).
- Web control (issue #149): while a reply is streaming and `steer` is available, the composer stays editable and
  shows a **Steer** button beside the disabled **Send**. Steer and Send are separate intents; Enter never steers.
  The outcome is stated in the composer: accepted (draft cleared only if unchanged since it was sent), refused by
  a live run, too late (the refusal came from a run that had already ended, read back with `GET /api/runs/{id}`),
  or another failure in plain words. A refusal keeps the text. Nothing is added to the transcript
  (`web/src/Steer.test.tsx`).
- Not built: `pending_steer` replay (`api_server_runs.py:202`: Hermes returns undelivered steer text on terminal
  events; surfacing it is a follow-up). The queue intent (#110) is a separate composer action, documented in
  [`queue.md`](queue.md).
