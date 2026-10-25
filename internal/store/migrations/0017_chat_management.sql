-- Chat management (issue #148, PRD §26.1 session/history, UXP-ACC-018).
--
-- pinned_at is the owner's pin; NULL means not pinned. Only an ACTIVE chat is pinned: archiving
-- clears it. Rename and archive need no column: title exists, and ARCHIVED is already a chat status.
ALTER TABLE chats ADD COLUMN pinned_at TEXT;

-- Deleting a chat removes its runs, so the tokens those runs reported would leave the user's
-- budget (PRD §21.7) and usage totals. This keeps only the two numbers, per user, and nothing that
-- names a chat, a run or any content. The budget and the usage total read it with the live runs.
CREATE TABLE deleted_chat_usage (
  user_id       TEXT PRIMARY KEY REFERENCES users(id),
  input_tokens  INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0)
);
