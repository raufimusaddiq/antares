-- Chat run lifecycle (PRD §6.2, §9, issue #23).
--
-- error_code is a short code from the fixed API vocabulary (e.g. HERMES_UNAVAILABLE),
-- never upstream text. A run that is not terminal occupies its chat: the partial
-- unique index makes "one active run per chat" a database fact, so two racing
-- sends cannot both be admitted (RUN_CONFLICT).
ALTER TABLE runs ADD COLUMN error_code TEXT;

CREATE UNIQUE INDEX runs_one_active_per_chat ON runs (chat_id)
  WHERE status IN ('PENDING','RUNNING','DISCONNECTED');
