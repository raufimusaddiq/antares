-- Voice staging (PRD §21.4, issue #82). An uploaded recording is a small, capped blob, so it
-- lives in the row: the metadata and the bytes commit or vanish together, and there is no
-- file on disk that a failed request could orphan.
CREATE TABLE assets (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  media_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
  created_at TEXT NOT NULL,
  content    BLOB NOT NULL
);
CREATE INDEX assets_user ON assets(user_id);
