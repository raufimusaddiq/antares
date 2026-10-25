# #146 authoritative Kenari usage, verification

## Endpoint verification (Kenari's own documentation, 2026-10-25)
- `https://kenari.id/llms.txt` and `https://kenari.id/openapi.json` list `GET /v1/account/balance`, `GET /v1/account/quota`,
  `GET /v1/usage` (also `/v1/usage/daily`, `/v1/usage/log`). Per-endpoint docs: `https://kenari.id/docs/account.md`,
  `account-balance.md`, `account-quota.md`, `usage.md`. Shapes, units and key rules are in `docs/runbooks/kenari-usage.md`.
- **Not verified live:** no provider key is available to this checkout. It lives in root-only operator files
  (`/opt/antares/provisioner.env`, mode 600 root; unreadable as the agent user, and not Antares' input). No privilege was escalated,
  no key was read, nothing was called with a credential. Parsing is therefore proven against the documented shapes served by a
  fake server (`internal/kenari/testdata/account/*.json`: documentation example values, fabricated).
- Open risk: if the key the owner configures is restricted or shared, Kenari answers `403` for balance/quota. That is
  reported on the page as "refused" with Kenari's code, never as a number.

## Tests (fail before, pass after)
- `internal/kenari` `account_test.go` (8): documented shapes, refused key, wrong shapes (missing figure, non-IDR) → unavailable,
  cache + 15 s refresh throttle, stale then gone after an outage, not configured never calls out, redirects refused,
  key absent from formatted snapshots and from echoed provider error bodies.
- `internal/auth` `admin_usage_test.go` (3): ordinary user and anonymous get 401/403 and Kenari receives **zero** requests;
  a client-chosen `profile` is 400; admin sees OK/REFUSED sections; response and headers carry no key; not configured and
  outage show no figure. Fails before: route absent (404).
- `internal/chat` `TestModelsNeverCarryTheSharedAccountQuota`: Hermes' shared `usage.windows`/`limit` no longer reach
  `GET /api/models` (any signed-in user). Fails before: body carried `quota`, `42.5`, `usedPercent`, `limitedUntil`.
- `internal/config` `TestKenariAPIKeyIsOptionalAndNeverPrints`: optional, Secret, redacted in every verb/JSON/slog.
- `internal/webui` `TestBrowserCodeReachesOnlyTheIdentityAPI`: the one new path is listed explicitly; the "credential-shaped"
  guard was not narrowed (the UI text was reworded instead).
- `web/src/ProviderUsage.test.tsx` (7), `ModelPicker.test.tsx` (the picker shows no usage/quota text).

## Browser (Chromium 1223, desktop 1280x900 and phone 390x780)
`usage-ui.mjs` serves the built bundle with the server's CSP against an in-script mock; `usage-ui.log` and `shots/*.png`.
States: ok, stale, refused, not configured, unavailable, error (retry), forbidden. 14/14 views without horizontal page
overflow; Refresh sends `GET …?refresh=1`; no request carries an Authorization header; the only page errors are the
expected 503/403 console lines of the error and forbidden cases. A rounding bug found in the first pass (Rp 1,5 shown as
Rp 2) was fixed: micro-Rupiah amounts keep two decimals.
