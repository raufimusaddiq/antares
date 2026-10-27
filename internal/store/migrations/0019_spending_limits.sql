-- Spending limits (PRD §21.7, UXP-009, CTX-005, UXP-ACC-011, issue #152), beside the token limit (token_budget).
--
-- All three are optional and in the price list's currency (IDR; Kenari quotes nothing else, and no exchange rate is
-- invented). NULL means not set. Soft limits only warn; the daily hard limit is a run admission gate on estimated
-- spend of priced replies in the current UTC day. Unpriced replies are never counted as zero cost: they are reported.
ALTER TABLE user_preferences ADD COLUMN daily_soft_idr REAL CHECK (daily_soft_idr IS NULL OR daily_soft_idr > 0);
ALTER TABLE user_preferences ADD COLUMN monthly_soft_idr REAL CHECK (monthly_soft_idr IS NULL OR monthly_soft_idr > 0);
ALTER TABLE user_preferences ADD COLUMN daily_hard_idr REAL CHECK (daily_hard_idr IS NULL OR daily_hard_idr > 0);
