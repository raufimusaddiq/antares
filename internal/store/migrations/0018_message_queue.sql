-- Per-chat message queue (PRD §27.4 QUEUE-001..004, §29.1, issue #110).
--
-- A queued message is plain text waiting for the chat's current run to end. It is keyed by owner AND
-- chat and cascades with both, so a guessed id never reaches another user's row. position orders the
-- queue; the ordinal shown to the user is derived from it. status is QUEUED until a dispatcher claims
-- the row with a compare-and-set to SUBMITTING; a row that became a run is deleted, and the run itself
-- carries the stable idempotency key "queue-<id>", so a repeated dispatch replays that run instead of
-- sending the message twice.
CREATE TABLE queue_items (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  position   INTEGER NOT NULL,
  input      TEXT NOT NULL,
  status     TEXT NOT NULL CHECK (status IN ('QUEUED','SUBMITTING')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX queue_items_chat ON queue_items (user_id, chat_id, position);

-- Why the chat's queue is not sending on its own. NULL means automatic dispatch is on. Anything else
-- is paused until the owner resumes it or sends the next item by hand: USER (they paused it), or the
-- status/code that stopped it (a failed, cancelled or disconnected run, a refused dispatch).
ALTER TABLE chats ADD COLUMN queue_paused TEXT;
