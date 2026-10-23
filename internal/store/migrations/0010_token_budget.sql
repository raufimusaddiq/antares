-- Opt-in per-chat token budget (PRD §21.7, issue #81). NULL means no limit: there is no
-- default budget. The limit counts a chat's reported input+output tokens, where Hermes'
-- input already includes cache reads, so it is a size cap on one conversation, not spend.
ALTER TABLE user_preferences ADD COLUMN token_budget INTEGER CHECK (token_budget IS NULL OR token_budget > 0);
