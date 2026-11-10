-- The AI editor (specs/006-ai-editor-blog, US3): suggested changes to the author's own text, kept per check, and the anchor of each block's part so a
-- block can be found again after the author edits (a check no longer goes out of date on an edit). Every row carries the author; every query filters on it.
CREATE TABLE post_edits (
  id         TEXT PRIMARY KEY,
  check_id   TEXT NOT NULL REFERENCES post_checks(id) ON DELETE CASCADE,
  post_id    TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  author_id  TEXT NOT NULL REFERENCES users(id),
  position   INTEGER NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('replace','insert')),
  part       INTEGER NOT NULL,
  anchor     TEXT NOT NULL,
  original   TEXT NOT NULL DEFAULT '',
  proposed   TEXT NOT NULL,
  reason     TEXT NOT NULL,
  decision   TEXT NOT NULL DEFAULT 'open' CHECK (decision IN ('open','accepted','rejected')),
  decided_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX post_edits_check ON post_edits (check_id, position);
CREATE INDEX post_edits_author_post ON post_edits (author_id, post_id);
ALTER TABLE post_blocks ADD COLUMN anchor_text TEXT NOT NULL DEFAULT '';
