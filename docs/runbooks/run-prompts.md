# Runbook — run approvals and clarification (issue #104)

Phase 0 is FAIL (`docs/hermes/PHASE0-FINDINGS.md`). Nothing here is a claim of multi-user readiness.

## What Hermes actually has (probed read-only, Hermes 0.22.5)

Source in the running container, `/opt/hermes/gateway/platforms/api_server*.py`, `gateway/session_pending_controls.py`;
the recorded `GET /v1/capabilities` fixture. No authenticated call was made (that needs a tenant key).

| Need | Hermes | Advertised in `/v1/capabilities`? |
|---|---|---|
| Approval prompt | SSE event `approval.request` on `GET /v1/runs/{id}/events`: `request_id` (or `prompt_id`), `command` (redacted), `description`, `choices`, and in authority mode `execution_generation`. Run status `waiting_for_approval`. | yes: `features.run_approval_response`, `features.approval_events`, `endpoints.run_approval` |
| Approval answer | `POST /v1/runs/{id}/approval`. Legacy body `{choice, request_id}`; authority mode exactly `{request_id, execution_generation, choice}`. `409` when not pending or stale. | yes |
| Clarification prompt | SSE event `clarify.request`: `prompt_id`, `execution_generation`, `question`, `choices`, `multi_select`. Emitted **only** by the session authority. | **no** |
| Clarification answer | `POST /v1/runs/{id}/clarify`, exactly `{request_id, execution_generation, answer}`. Exists **only** in session-authority mode (`respond_run`). | **no** (no `run_clarify` endpoint, no feature flag) |

Not established: whether the deployed Hermes runs in session-authority mode. That needs an authenticated
`GET /v1/runs/{id}` (look for `pending_controls`) and is the follow-up issue.

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
