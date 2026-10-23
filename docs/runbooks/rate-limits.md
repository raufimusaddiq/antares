# Per-account rate limits and quotas (issue #61)

PRD Phase 3 (§16), §15.3. Limits run in the Go request path after session/account/binding
eligibility and same-origin checks, before the downstream handler reads a body or calls Hermes.
The key is the authenticated account ID: tabs, sessions and IP addresses share that account's
budget. Other accounts have independent budgets.

| Environment variable | Default | Enforcement |
| --- | ---: | --- |
| `ANTARES_RUNS_PER_MINUTE` | 30 | Run creation attempts at `POST /api/chats/{chatId}/messages` |
| `ANTARES_MESSAGES_PER_MINUTE` | 60 | Sends at the same endpoint |
| `ANTARES_UPLOADS_PER_MINUTE` | 10 | Upload attempts at `POST /api/assets` |
| `ANTARES_SSE_CONNECTIONS` | 4 | Concurrent `GET /api/runs/{runId}/events` requests |

All overrides must be positive integers; invalid values refuse startup. Restart Antares to apply
environment changes. A message creates a run, so both budgets must admit the request atomically;
the lower limit wins. Requests admitted to a handler count even if validation fails or the
idempotency key replays an existing run. Rejected attempts do not consume additional budget.
Uploads remain unsupported: below quota they return `501 CAPABILITY_UNAVAILABLE`, above it `429`.
This ticket adds no upload capability, storage quota or provider billing quota.

Rates use a fixed one-minute window starting on the account's first limited request. At the window
boundary counters reset; a burst on each side of a boundary is possible. SSE slots remain held across
window resets and release on every handler return, including disconnect, cancellation and failed open.
The existing four-active-runs-per-account and one-run-per-chat rules still apply.

Every `RATE_LIMITED` response uses HTTP `429`, `Cache-Control: no-store`, and this JSON envelope
(the message and request ID vary). A known wait includes an integer `Retry-After` header in seconds:

```json
{"error":{"code":"RATE_LIMITED","message":"Account request limit reached; retry later","requestId":"req_example","retryAfterSeconds":60}}
```

For rate windows the retry hint rounds up to the remaining seconds. For SSE concurrency and the
active-run cap, the wait depends on a connection closing or a run finishing: `retryAfterSeconds` is
`null` and `Retry-After` is absent. Upstream throttling and sign-in flow capacity use the same
unknown-wait envelope; Antares does not invent an upstream deadline.

`EventSource` cannot expose a JSON `429` body to the browser. When a stream is refused or repeated
reconnects fail, the UI checks `GET /api/runs/{runId}` before reporting its state. A `RUNNING` or
`PENDING` run shows that live updates disconnected and the run remains active, with Cancel and
Reconnect available. Only a confirmed terminal status is presented as a completed, failed or
cancelled run; a failed status check leaves the run's outcome unknown.

Counters live only in this single process and reset on restart. A mutex serializes admission and
release. A request-driven sweep runs at most once a minute and removes expired accounts without
open streams. The map holds at most 10,000 accounts; new accounts at capacity receive the same
`429` with an unknown wait (`retryAfterSeconds: null`, no `Retry-After`) until cleanup frees a slot.
Existing accounts keep their budgets.
Multiple replicas would require shared enforcement; this implementation is for the single-container
deployment in the PRD.

Verification: `internal/auth/limits_test.go` covers boundary expiry, two-account separation,
concurrent admission, SSE release and map bounds. `internal/chat/limits_test.go` exercises the
registered routes with authenticated A/B accounts, accepted sends, idempotent replay, refused
requests, unsupported uploads and SSE cancellation/error cleanup. `internal/config/config_test.go`
covers defaults, overrides and invalid input. Existing negative IDOR tests remain in
`TestTwoUsersChatPrivately` and the chat suite.

Phase 0's FAIL verdict is unchanged. These tests use a fake Hermes and do not prove live tool
filesystem isolation.
