-- Built-in writing rules (specs/005-antislop-writing).
--
-- One row at most: absent means "on". The two columns say what a run or a post check was sent with:
--   'v1'  the rules text (version v1) was attached
--   'off' the administrator had switched the rules off, on purpose
--   NULL  expected but not attached (the text could not be loaded), or older than this migration
-- Only rows newer than this migration's applied_at count as "not applied" in the admin card.
CREATE TABLE writing_rules_setting (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  enabled    INTEGER NOT NULL CHECK (enabled IN (0,1)),
  updated_by TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
ALTER TABLE runs ADD COLUMN writing_rules TEXT;
ALTER TABLE post_checks ADD COLUMN writing_rules TEXT;
