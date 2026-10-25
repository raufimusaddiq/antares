-- Chat management (issue #148, PRD §26.1 session/history, UXP-ACC-018).
--
-- pinned_at is the owner's pin; NULL means not pinned. Only an ACTIVE chat is pinned: archiving
-- clears it. Rename and archive need no column: title exists, and ARCHIVED is already a chat status.
ALTER TABLE chats ADD COLUMN pinned_at TEXT;

-- A delete that did not finish must not cost the chat its place. While a delete is under way the chat is
-- PENDING_DELETE and pre_delete_status remembers whether it was ACTIVE or ARCHIVED; when the delete fails the
-- chat goes back to that status (pin untouched) and delete_failed_at flags it. The DELETE_FAILED status is
-- kept in the CHECK but is no longer written.
ALTER TABLE chats ADD COLUMN pre_delete_status TEXT CHECK (pre_delete_status IN ('ACTIVE','ARCHIVED'));
ALTER TABLE chats ADD COLUMN delete_failed_at TEXT;

-- Deleting a chat removes its runs, so the tokens those runs reported would leave the user's
-- budget (PRD §21.7) and usage totals. This keeps only the two numbers, per user, and nothing that
-- names a chat, a run or any content. The budget and the usage total read it with the live runs.
CREATE TABLE deleted_chat_usage (
  user_id       TEXT PRIMARY KEY REFERENCES users(id),
  input_tokens  INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0)
);
