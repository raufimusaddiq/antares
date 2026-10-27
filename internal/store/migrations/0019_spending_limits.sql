-- Spending limits (PRD §21.7, UXP-009, CTX-005, UXP-ACC-011, issue #152), beside the token limit (token_budget).
--
-- All three are optional and in the price list's currency (IDR; Kenari quotes nothing else, and no exchange rate is
-- invented). NULL means not set. Soft limits only warn; the daily hard limit is a run admission gate on estimated
-- spend of priced replies in the current UTC day. Unpriced replies are never counted as zero cost: they are reported.
ALTER TABLE user_preferences ADD COLUMN daily_soft_idr REAL CHECK (daily_soft_idr IS NULL OR daily_soft_idr > 0);
ALTER TABLE user_preferences ADD COLUMN monthly_soft_idr REAL CHECK (monthly_soft_idr IS NULL OR monthly_soft_idr > 0);
ALTER TABLE user_preferences ADD COLUMN daily_hard_idr REAL CHECK (daily_hard_idr IS NULL OR daily_hard_idr > 0);

-- Deleting a chat removes its runs, but not what they cost: their usage is kept here per UTC day and model (counts and
-- token sums only, nothing that names a chat, a run or any content), so the spending windows still count it and
-- deleting a chat cannot reset a limit. It goes with the user.
CREATE TABLE deleted_run_usage (
  user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day                TEXT NOT NULL,   -- YYYY-MM-DD, UTC, of the run's start
  model              TEXT NOT NULL,   -- '' when Hermes did not say
  runs               INTEGER NOT NULL CHECK (runs > 0),
  input_tokens       INTEGER NOT NULL DEFAULT 0,
  output_tokens      INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day, model)
);
