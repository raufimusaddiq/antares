-- Own model hiding and global model policy (PRD §26.1, §29.1, LLM-ACC-003, issue #115).
--
-- model_hidden is one user's preference for what their own picker shows. It is keyed by owner,
-- changes nothing at Hermes and never affects another user. model_blocked is the admin's global
-- block list: it always wins over a user's preference and is not user content, so an admin may
-- write it without reading anyone's conversations.
CREATE TABLE model_hidden (
  user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  model_id TEXT NOT NULL,
  PRIMARY KEY (user_id, model_id)
);

CREATE TABLE model_blocked (
  model_id   TEXT PRIMARY KEY,
  blocked_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL
);
