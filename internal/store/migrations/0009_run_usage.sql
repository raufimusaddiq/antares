-- Token usage per run (UXP-008, UXP-009, issue #81). The numbers are what Hermes
-- reported for the run; NULL means Hermes reported none, which is not zero.
-- input_tokens includes cache reads and writes (Hermes counts them inside it).
ALTER TABLE runs ADD COLUMN input_tokens INTEGER;
ALTER TABLE runs ADD COLUMN output_tokens INTEGER;
ALTER TABLE runs ADD COLUMN cache_read_tokens INTEGER;
ALTER TABLE runs ADD COLUMN cache_write_tokens INTEGER;
ALTER TABLE runs ADD COLUMN usage_model TEXT;
