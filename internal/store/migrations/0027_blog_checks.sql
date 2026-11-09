-- The AI check of a blog post (specs/001-blog-cms-openui-publish/data-model.md): one run of the author's own assistant over
-- a pinned version of the draft, the notes and proposed blocks it returned, and the blocks the author accepted at publish.
-- Every table carries the author; every authenticated query filters on it.
CREATE TABLE post_checks (
  id             TEXT PRIMARY KEY,
  post_id        TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  author_id      TEXT NOT NULL REFERENCES users(id),
  version        INTEGER NOT NULL,
  state          TEXT NOT NULL CHECK (state IN ('running','ready','failed','cancelled','timed_out','out_of_date')),
  reason         TEXT NOT NULL DEFAULT '' CHECK (reason IN ('','error','disconnected','unusable')),
  prev_status    TEXT NOT NULL,
  hermes_session TEXT NOT NULL DEFAULT '',
  hermes_run     TEXT NOT NULL DEFAULT '',
  notes          TEXT NOT NULL DEFAULT '[]',
  notices        TEXT NOT NULL DEFAULT '[]',
  started_at     TEXT NOT NULL,
  finished_at    TEXT NOT NULL DEFAULT ''
);
-- A post has at most one running check.
CREATE UNIQUE INDEX post_checks_running ON post_checks (post_id) WHERE state = 'running';
CREATE INDEX post_checks_post ON post_checks (post_id, started_at DESC);
CREATE INDEX post_checks_author_running ON post_checks (author_id) WHERE state = 'running';

CREATE TABLE post_blocks (
  id         TEXT PRIMARY KEY,
  check_id   TEXT NOT NULL REFERENCES post_checks(id) ON DELETE CASCADE,
  post_id    TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  author_id  TEXT NOT NULL REFERENCES users(id),
  position   INTEGER NOT NULL,
  anchor     INTEGER NOT NULL,
  kind       TEXT NOT NULL,
  markup     TEXT NOT NULL,
  fallback   TEXT NOT NULL,
  decision   TEXT NOT NULL DEFAULT 'proposed' CHECK (decision IN ('proposed','accepted','rejected')),
  needs_ack  INTEGER NOT NULL
);
CREATE INDEX post_blocks_check ON post_blocks (check_id, position);

-- verified is always 0: the run's events do not expose what a search returned (research R4), so nothing can be checked
-- against them. The constraint is the guard; a migration that relaxes it is the change that makes a source verifiable.
CREATE TABLE block_sources (
  block_id     TEXT NOT NULL REFERENCES post_blocks(id) ON DELETE CASCADE,
  position     INTEGER NOT NULL,
  url          TEXT NOT NULL,
  title        TEXT NOT NULL,
  retrieved_at TEXT NOT NULL,
  verified     INTEGER NOT NULL DEFAULT 0 CHECK (verified = 0),
  PRIMARY KEY (block_id, position)
);

-- What readers see: the blocks the author accepted, copied when a version is published. Later checks do not change it.
CREATE TABLE published_blocks (
  post_id  TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  version  INTEGER NOT NULL,
  position INTEGER NOT NULL,
  anchor   INTEGER NOT NULL,
  kind     TEXT NOT NULL,
  markup   TEXT NOT NULL,
  fallback TEXT NOT NULL,
  PRIMARY KEY (post_id, version, position)
);
CREATE TABLE published_block_sources (
  post_id      TEXT NOT NULL,
  version      INTEGER NOT NULL,
  block        INTEGER NOT NULL,
  position     INTEGER NOT NULL,
  url          TEXT NOT NULL,
  title        TEXT NOT NULL,
  retrieved_at TEXT NOT NULL,
  PRIMARY KEY (post_id, version, block, position),
  FOREIGN KEY (post_id, version, block) REFERENCES published_blocks (post_id, version, position) ON DELETE CASCADE
);
