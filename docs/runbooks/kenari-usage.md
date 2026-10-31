# Runbook — authoritative Kenari account usage (issue #146)

Phase 0 is still FAIL: trusted-user operation only. Nothing here is a multi-user security claim.

## What is shown, and where each number comes from

| Number | Source | Where it is shown | Who may read it |
|---|---|---|---|
| Account balance, reserved, available (micro-IDR → Rp) | **Provider-authoritative.** `GET https://kenari.id/v1/account/balance` | `/admin/usage` | Admin only |
| Plan quota windows (`five_hour`/`week`/`month`), coupon (whole Rp) | **Provider-authoritative.** `GET https://kenari.id/v1/account/quota` | `/admin/usage` | Admin only |
| Requests, tokens, charged cost per model and billing kind, last 30 days (UTC) | **Provider-authoritative.** `GET https://kenari.id/v1/usage` | `/admin/usage` | Admin only |
| Per-chat token counts, "estimated cost" | **Local estimate** from Hermes run `usage` and a static price list (`internal/chat/usage.go`) | Chat usage panel | The chat's owner |
| Model prices | Kenari public catalogue `GET /v1/models` (list price, not a bill) | Model picker | Any approved user |

A local estimate is never labelled Kenari usage, and the provider account is **shared** (one key for every Antares
account): its figures are never per-user or per-chat and imply no per-user cost.

## The endpoints (Kenari's own documentation)

Read 2026-10-25 from `https://kenari.id/openapi.json` (paths `/v1/account/balance`, `/v1/account/quota`, `/v1/usage`),
`https://kenari.id/docs/account.md`, `account-balance.md`, `account-quota.md`, `usage.md`. Auth is `Authorization: Bearer kn-…`.
Balance and quota are refused (`403`, `restricted_key_not_allowed` / `shared_key_not_allowed`) for a restricted or shared
key: the configured key **must be an unrestricted key of the account**. `/v1/usage` reads the whole account for such a key.
Units: balance and usage costs are integer **micro-Rupiah** (Rp 1 = 1,000,000); quota is whole Rupiah. Reads are free.

Not live-verified: the checkout had no provider key (it lives in root-only operator files and is not Antares' input), so
the parsing is tested against the shapes Kenari documents with a fake server. First production read is the owner's.

## Configuration

`ANTARES_KENARI_API_KEY` (runtime secret, optional, `config.Secret`: redacted in every format). Unset: the admin page says
"Not configured" and Antares makes no request. Setting it and restarting Antares is an operator action.

## Behaviour (`internal/kenari/account.go`)

- Backend only. Antares calls the three GETs; the key is in the Authorization header of those requests and nowhere else;
  redirects are refused; error bodies are never echoed (only the two documented refusal codes are passed on).
- Cached 60 s; any caller's refresh is honoured at most every 15 s (an outage cannot be hammered). A failed read keeps the
  last good answer for up to 1 hour marked **stale**, then `UNAVAILABLE`. Stale holds until a read succeeds.
- Each of the three sections can be `OK`, `REFUSED` or `UNAVAILABLE` on its own. An unexpected shape (missing figure,
  currency other than IDR) is `UNAVAILABLE`: never a zero.
- `GET /api/admin/kenari-usage[?refresh=1]` is behind the Admin guard. Another user, an anonymous caller, or a request
  carrying a profile/key/session field gets 401/403/400 and Kenari receives nothing.
- The model picker (`GET /api/models`) no longer carries Hermes' provider `usage.windows`: those are the same shared
  account's quota and were visible to every signed-in user.

## Owner decision (not taken here): a shared aggregate for ordinary users

If the owner wants every user to see something, the least revealing option is the plan quota's `used_frac` and `resets_at`
per window only (no Rupiah, no balance, no per-model spend). It would still disclose the account's load to everyone, so it
needs an explicit owner decision. Not built.

## Shared limit for every user (`GET /api/quota`)

One plan serves every Antares account, so its quota is everyone's limit (owner request, 2026-10-29). Every active
account sees it in the chat: under "Shared limit, all users", above the account line on a wide screen and in the
Menu on a phone, as one bar per window with the share used and when it resets (`web/src/QuotaBar.tsx`).

- **What a user gets**: `state`, the plan's name, and per window only `key`, `usedFrac` and `resetsAt`. Balance,
  Rupiah amounts, per-model spend and the key stay on the admin route; `TestSharedQuotaShowsFractionsToActiveUsersOnly`
  checks that none of them reaches the user answer, and that an anonymous or pending caller gets nothing.
- **Load on Kenari**: the answer comes from the same cached snapshot as the admin page, never a refresh (fresh for a
  minute, attempts 15 seconds apart), so users cannot drive Kenari's 60-a-minute usage limit. The browser reads it when
  the chat opens and every five minutes.
- **Nearly spent**: from 80% (Kenari's own `alert_pct` default) a window's bar turns pink and says "nearly used
  up" in words.
- Admin → Provider usage shows the same bars with the Rupiah figures, and token counts grouped the Indonesian way
  (6.000.000).

