# Runbook — logs and metrics (PRD §13.2, issue #114)

## Every log line is redacted

`cmd/antares` sets `log.SetOutput(redact.Writer(os.Stderr))`, and the CLI admin commands write their messages through
the same writer. `internal/redact` replaces session cookies (`__Host-antares_*`), bearer tokens, JWTs, `sk-…` provider
keys and `api_key|secret|token|password = value` pairs with `[redacted]`. The smoke tool uses the same patterns.
`internal/redact/redact_test.go` proves the redaction and scans the server's source: nothing writes to stdout/stderr
around the log (the one exception, `backup -` streaming the database to stdout, is marked).

No log line carries a prompt, a reply, a draft, a title, a cookie, a header, a query string or a Hermes key.

## What is logged

| Line | Fields |
|---|---|
| `http …` (every `/api` request) | `req` (request id), `method`, `route` (the route pattern, never the raw path), `status`, `ms`, `bytes`, `user` (internal opaque id, or `-`), `error` (the API error code of a refusal, or `-`) |
| `run …` (every run that ends, is cancelled or is disconnected) | `user`, `chat`, `run`, `binding` (the binding id, never its key), `state`, `outcome` (`success`/`failure`/`cancelled`/`unknown`), `error` |
| `run usage …` | `run`, input / output / cache-read tokens, model, when Hermes reported them |
| `chat: history page …`, `chat: search index …` | durations and counts only (#171) |

A `DISCONNECTED` run is logged `outcome=unknown`, never as success (PRD §24.3).

## Metrics

`GET /api/admin/metrics` (admin only; aggregates, no id, name or content):

- `database`: accounts by approval status (pending registrations), tenants by status (Hermes tenant readiness), runs by
  status all time and in the last 24 h (success/failure/cancel), admin decisions by action (approvals/rejections), open
  tool approvals and questions, stored assets and their bytes, queued messages;
- `process` (since `processSince`): streams opened / resumed / ended by the server (`run.disconnected`), denials (401/403
  answers), Hermes calls with mean and max latency to the response headers.

Job reconciliation failures are not reported: the Jobs UI (#105) is not built.
