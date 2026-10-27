# Runbook — usage limits: tokens and spending

Issue #152; PRD §21.7, UXP-009, CTX-005, UXP-ACC-011. Every limit is opt-in; none is set by default.

| Limit | Kind | Window | Unit | Where |
|---|---|---|---|---|
| Token limit | **hard** (refuses new messages) | lifetime, all your chats | input + output tokens Hermes reported | `PUT /api/me/budget` |
| Daily warning | **soft** (warns only) | current UTC day | estimated spend | `PUT /api/me/spending-limits` `dailySoft` |
| Monthly warning | **soft** (warns only) | current UTC month | estimated spend | `monthlySoft` |
| Daily spending limit | **hard** (refuses new messages) | current UTC day | estimated spend | `dailyHard` |

**Spend** is the estimate of replies whose model has a known price (`internal/chat/usage.go` price list, dated), summed
over replies started in the window across all the user's chats. A reply on a model with no known price is reported as
"no known price (not counted)", never as zero cost. Replies of deleted chats are not in any window (only their token
totals survive).

**Hard means an admission gate, not a provider cap.** A free chat whose user is at or over a hard limit takes no new
message (`403 BUDGET_REACHED`); a reply already running finishes, and its cost may take the day past the limit. There is
no provider reservation API, so no exact ceiling is promised. A replay of an already-admitted message still gets its run
back. Soft limits never refuse anything. The UI says which is which: "a warning only; messages still go through" against
"new messages are refused".

**Currency: IDR, not the PRD's USD.** The only prices known are Kenari's, quoted in IDR; converting to USD would need an
exchange rate nobody supplied, which would make every figure invented. The schema stores IDR (`user_preferences.daily_soft_idr`,
`monthly_soft_idr`, `daily_hard_idr`, migration `0019`), and the API reports `currency`.

**Forward migration.** `0019_spending_limits.sql` adds three nullable columns to `user_preferences` (each `NULL` or `> 0`);
existing rows keep their token limit and gain no spending limit. Nothing is backfilled.

Tests: `internal/chat/spending_test.go` (boundary at, under and over the hard limit; soft warns and never blocks; unpriced
replies not counted; UTC day and month windows; replay at the limit; strict input; another user unaffected).
