-- The blog (specs/001-blog-cms-openui-publish/data-model.md). Every table a user request touches carries the author, and
-- every authenticated query filters on it. Checks, blocks and sources arrive with the AI check; this is the plain
-- writing and publishing half.
CREATE TABLE posts (
  id                TEXT PRIMARY KEY,
  author_id         TEXT NOT NULL REFERENCES users(id),
  slug              TEXT NOT NULL DEFAULT '',
  title             TEXT NOT NULL DEFAULT '',
  summary           TEXT NOT NULL DEFAULT '',
  cover_asset_id    TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL CHECK (status IN ('draft','checking','ready','published','unpublished')),
  latest_version    INTEGER NOT NULL,
  published_version INTEGER NOT NULL DEFAULT 0,
  published_at      TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);
CREATE INDEX posts_author ON posts (author_id, updated_at DESC);
-- A slug names one published post; drafts may share or lack one.
CREATE UNIQUE INDEX posts_published_slug ON posts (slug) WHERE status = 'published';

-- A version is the title, summary and document as saved. Autosave moves the head version forward in place; a pinned
-- version (published, later also checked) is never rewritten: the next save starts a new one.
CREATE TABLE post_versions (
  post_id    TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  version    INTEGER NOT NULL,
  title      TEXT NOT NULL,
  summary    TEXT NOT NULL,
  doc        TEXT NOT NULL,
  pinned     INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY (post_id, version)
);

CREATE TABLE post_assets (
  id           TEXT PRIMARY KEY,
  post_id      TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  author_id    TEXT NOT NULL REFERENCES users(id),
  content_type TEXT NOT NULL,
  bytes        BLOB NOT NULL,
  created_at   TEXT NOT NULL
);
CREATE INDEX post_assets_post ON post_assets (post_id);
