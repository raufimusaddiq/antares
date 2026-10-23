-- A chat whose transcript could not be read is marked failed_at and retried only after a backoff, so a
-- few persistently failing chats cannot occupy every search's read budget. Cleared by a successful index.
ALTER TABLE search_state ADD COLUMN failed_at TEXT NOT NULL DEFAULT '';

-- Runes of text indexed for the chat: the per-user budget that bounds how much one search can scan.
ALTER TABLE search_state ADD COLUMN runes INTEGER NOT NULL DEFAULT 0;
