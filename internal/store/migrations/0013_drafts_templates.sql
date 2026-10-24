-- Server-side composer drafts and personal slash templates (PRD §21.6, §22, UXP-006/007,
-- issue #107).
--
-- A draft is plain text: it is not encrypted at rest and nothing claims it is. revision is
-- the compare-and-set token two devices use to avoid overwriting each other; it starts at 1
-- on the first write (the absence of a row is revision 0). chat_drafts is keyed by owner AND
-- chat, so a guessed chat id can never address another user's row, and it cascades with the
-- chat and the user.
CREATE TABLE chat_drafts (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  revision   INTEGER NOT NULL CHECK (revision >= 1),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, chat_id)
);

-- The composer of a chat that does not exist yet: one per user.
CREATE TABLE new_chat_drafts (
  user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  revision   INTEGER NOT NULL CHECK (revision >= 1),
  updated_at TEXT NOT NULL
);

-- A template is stored text and nothing else: there is deliberately no command, tool or
-- skill column. Inserting one into the composer is a client text edit.
CREATE TABLE prompt_templates (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (user_id, name)
);
