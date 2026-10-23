-- Derived, per-user search index over a user's own chats (PRD §21.3, UXP-003). It is a
-- scoped copy of text Hermes owns, not canonical transcript storage: rebuilt per chat from
-- the transcript, purged with the chat.
--
-- Privacy by construction: every row names its owner, the composite foreign key makes the
-- database refuse a row whose owner differs from its chat's, and store.SearchChats is the
-- only reader, joining on that owner in the query itself. There is no unscoped MATCH.
--
-- seq is the turn's position in the Hermes transcript (the history API's turn identity);
-- -1 is the chat title. folded is the Go-side case fold of body (store.Fold) so that the
-- query and the stored text are folded by the same function; SQLite's LOWER() folds ASCII only.
CREATE TABLE search_docs (
  user_id    TEXT NOT NULL,
  chat_id    TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  role       TEXT NOT NULL CHECK (role IN ('title','user','assistant')),
  body       TEXT NOT NULL,
  folded     TEXT NOT NULL,
  created_at TEXT,
  PRIMARY KEY (chat_id, seq),
  FOREIGN KEY (chat_id, user_id) REFERENCES chats (id, user_id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX search_docs_user ON search_docs (user_id, chat_id);

-- A chat is fresh when indexed_at >= invalidated_at. truncated means a size or quota
-- limit cut the index short, so the chat is never reported as completely searchable.
CREATE TABLE search_state (
  chat_id        TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL,
  indexed_at     TEXT NOT NULL,
  invalidated_at TEXT NOT NULL,
  truncated      INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY (chat_id, user_id) REFERENCES chats (id, user_id) ON DELETE CASCADE
);
CREATE INDEX search_state_user ON search_state (user_id);

-- SearchChats orders by (updated_at DESC, id DESC) within one user's ACTIVE chats.
CREATE INDEX chats_user_status_updated ON chats (user_id, status, updated_at DESC, id DESC);
