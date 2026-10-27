-- #151: a deleted chat's finished replies that reported no usage stay counted, per UTC day, so the usage dashboard's
-- totals and "unreported" figures do not shrink when a chat is deleted. Tokens are not recorded: they are unknown.
CREATE TABLE deleted_unreported_runs (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day     TEXT NOT NULL,   -- YYYY-MM-DD, UTC, of the run's start
  runs    INTEGER NOT NULL CHECK (runs > 0),
  PRIMARY KEY (user_id, day)
);
