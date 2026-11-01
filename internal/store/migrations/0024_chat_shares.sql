-- #250: a read-only link to a frozen copy of one chat. Only a hash of the link's token is kept, so the database
-- alone cannot rebuild a link. One link per chat: sharing again replaces it, and the old link stops working.
CREATE TABLE chat_shares (
  token_hash TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  chat_id    TEXT NOT NULL,
  title      TEXT NOT NULL,
  turns      TEXT NOT NULL,
  turn_count INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE (chat_id, user_id),
  FOREIGN KEY (chat_id, user_id) REFERENCES chats (id, user_id)
);
