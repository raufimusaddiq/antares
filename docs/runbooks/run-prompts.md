# Runbook — run approvals and clarification (issue #104)

Phase 0 is FAIL (`docs/hermes/PHASE0-FINDINGS.md`). Nothing here is a claim of multi-user readiness.

## Published controls (source inspection, Hermes 0.22.5)

Source in the running container, `/opt/hermes/gateway/platforms/api_server*.py`, `gateway/session_pending_controls.py`;
the recorded `GET /v1/capabilities` fixture. No authenticated call was made (that needs a tenant key).

| Need | Hermes | Advertised in `/v1/capabilities`? |
|---|---|---|
| Approval prompt | SSE event `approval.request` on `GET /v1/runs/{id}/events`: `request_id` (or `prompt_id`), `command` (redacted), `description`, `choices`, and in authority mode `execution_generation`. Run status `waiting_for_approval`. | yes: `features.run_approval_response`, `features.approval_events`, `endpoints.run_approval` |
| Approval answer | `POST /v1/runs/{id}/approval`. Legacy body `{choice, request_id}`; authority mode exactly `{request_id, execution_generation, choice}`. `409` when not pending or stale. | yes |
| Clarification prompt | SSE event `clarify.request`: `prompt_id`, `execution_generation`, `question`, `choices`, `multi_select`. Emitted **only** by the session authority. | **no** |
| Clarification answer | `POST /v1/runs/{id}/clarify`, exactly `{request_id, execution_generation, answer}`. Exists **only** in session-authority mode (`respond_run`). | **no** (no `run_clarify` endpoint, no feature flag) |

## Authenticated disposable-profile probe (#125, 2026-10-24)

At 17:13-17:15 WIB, a separately provisioned disposable profile advertised
`features.run_unknown_resolution: true`. Its authenticated `GET /v1/runs/{id}` returned
`pending_controls: []`, `admission_id` and `execution_generation`. Session-authority mode is
therefore confirmed for this deployment. The sanitized responses are
[`t125-capabilities.body.json`](../hermes/fixtures/t125-capabilities.body.json) and
[`t125-authority-run.body.json`](../hermes/fixtures/t125-authority-run.body.json).

The source-verified `POST /v1/runs/{id}/clarify` was reached with an intentionally nonexistent
prompt on that same owned run and returned `409 stale_generation`:
[`t125-stale-clarify.body.json`](../hermes/fixtures/t125-stale-clarify.body.json).
This negative response proves the handler is reachable; it does not prove a successful answer.
The live capability document still omits `run_clarify`, so Antares keeps clarification unsupported.

Both attempts to start a clarification-producing turn failed before a tool could ask anything.
The configured `custom` model provider returned `HTTP 401: invalid key`, also observed by the
full deployment smoke. The actual terminal SSE is recorded in
[`t125-failed-run.sse`](../hermes/fixtures/t125-failed-run.sse). No real `approval.request` or
`clarify.request` frame was observed. Empty `pending_controls` is not evidence of a working prompt.
The helper policy's effective toolsets did not expose a clarification tool either. No tool policy,
shared credential or capability gate was changed to manufacture an event.

Remaining upstream work: make the selected model provider usable, advertise `run_clarify` with
its verified POST path, and expose the clarification tool for the disposable profile. Then capture
real approval/clarification prompts and exercise exact-generation answers, denial and stale replay.
This probe does not close #125 or claim multi-user isolation.

Commands used: `docker exec -u hermes hermes` to read the deployed
`gateway/platforms/api_server*.py` and `gateway/session_pending_controls.py`; the authenticated
host helper `POST /provision` for one new random slug; scoped `GET /v1/capabilities`,
`GET /v1/toolsets`, `POST /v1/runs`, `GET /v1/runs/{id}`, `GET /v1/runs/{id}/events`, and the
negative clarification request above. Credentials stayed in protected host files and headers.
Raw evidence: `/var/backups/antares/probe-125-20261024T101329Z`. The production image remained
`458820cee34c1b280e3eb3d5d76fe9241be2f2cf`.

## What Antares does

- `follow()` sees `approval.request` / `clarify.request`, records a row in `run_prompts` (migration 0013) and
  emits `run.prompt`; the stream stays open and **nothing answers for the user**: no timer, no default, no
  "remember". Hermes itself blocks the tool call, so the run cannot proceed until a decision arrives.
- The prompt is shown only after `Advertises()` confirms Hermes lists the answer route (`run_approval`,
  `run_clarify`). Today that is true for approvals and false for clarification, so a `clarify.request` fails the
  run closed (`CAPABILITY_UNAVAILABLE`) exactly as an unsupported approval did before. A prompt with no request
  id, a clarification without a generation, or an approval that cannot be denied also fails closed.
- `GET /api/runs/{id}` returns `prompts` read from the database, so a reload or a restart shows them again.
  `POST /api/runs/{id}/approve {promptId, choice}` and `/clarify {promptId, answer}` answer them. The upstream
  request id and generation are stored server-side and never leave it; a body that carries them is a 400.
- Only `once` and `deny` are accepted. `session` and `always` are standing grants, never offered or sent.
- Ownership is in the SQL (`user_id` on every prompt query). A foreign, unknown or wrong-kind prompt is the same
  `404` as a missing one; no session is `401`. The gate runs before Hermes, so a suspended account sends nothing.
- A claim (`PENDING → SENDING`) lets exactly one request send; an undelivered answer reopens the prompt, a Hermes
  `409` settles it (`PROMPT_STALE`), and a finished or cancelled run settles all its prompts.
- A clarification answer's text is not stored. Approval commands are stored as Hermes redacted them, clipped.
- Admin: `GET /api/capabilities` carries `run_approvals` and `clarification` verdicts, shown in Admin → Capabilities.

## Not model actions

`web/src/openui/actions.ts` keeps `ALLOWED_ACTIONS = ["reply"]`. The approval card is the app's own UI
(`web/src/RunPrompts.tsx`), not a component the model can render, because a model-written button must never be
able to approve the model's own tool call. A test pins the allowlist.

## Tests

`internal/chat/prompts_test.go` (blocked-until-approved, foreign and unauthenticated, clarification round trip,
fail-closed, stale, suspended, undelivered), `internal/store/prompts_test.go` (ownership in the queries),
`internal/hermes/prompts_test.go` and `discovery_test.go` (wire shapes, advertised routes), `web/src/RunPrompts.test.tsx`.

## Clarification route on Hermes v0.22.5

Hermes registers `POST /v1/runs/{run_id}/clarify` but leaves it out of `/v1/capabilities`' endpoint table, so
clarification prompts were reported unsupported. The rule that an answer goes only to an advertised route has one
explicit exception (`hermes.Client.proven`, list `provenRoutes`, only `run_clarify`): the route is accepted when a probe
about a run that cannot exist (`run_0000000000000000antaresprobe`) gets Hermes' own answer, 404 with code
`run_not_found`. A bare 404 or a 405 is not proof, and no other unlisted route is probed. The `clarify` tool itself is
enabled for API chats by the toolset policy (tenant-toolsets.md), so the model can ask.
