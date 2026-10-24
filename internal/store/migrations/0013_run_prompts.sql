-- Run approvals and clarification prompts (PRD §9A.7, issue #104).
--
-- One row per decision Hermes is waiting for on a run. The row, not the browser and not the worker's
-- memory, is the authority on whether a prompt is still open, so a reload or a restart cannot lose one
-- and nothing is ever decided on the user's behalf. PENDING -> SENDING while the user's answer is on its
-- way to Hermes (so a double submit sends once) -> ANSWERED; SETTLED means Hermes or the run ended it
-- without Antares sending an answer. text is Hermes' redacted, bounded command or question; an answer's
-- content is never stored (Hermes owns the transcript), only an approval's choice.
CREATE TABLE run_prompts (
  id                   TEXT PRIMARY KEY,
  user_id              TEXT NOT NULL REFERENCES users(id),
  run_id               TEXT NOT NULL,
  kind                 TEXT NOT NULL CHECK (kind IN ('approval','clarify')),
  hermes_request_id    TEXT NOT NULL,
  execution_generation INTEGER,
  text                 TEXT NOT NULL,
  detail               TEXT NOT NULL DEFAULT '',
  choices              TEXT NOT NULL DEFAULT '[]',
  status               TEXT NOT NULL CHECK (status IN ('PENDING','SENDING','ANSWERED','SETTLED')),
  decision             TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL,
  decided_at           TEXT,
  UNIQUE (run_id, hermes_request_id),
  FOREIGN KEY (run_id, user_id) REFERENCES runs (id, user_id)
);
CREATE INDEX run_prompts_open ON run_prompts (user_id, run_id) WHERE status IN ('PENDING','SENDING');
