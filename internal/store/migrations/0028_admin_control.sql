-- Admin control (specs/004-admin-full-control).
--
-- A takedown is a mark on a published post. It never changes status or the published version, so a restore returns the post
-- exactly as it was. The mark is all empty or all set: takedown_at/by/reason/title are written together by the admin action
-- and cleared together by the restore. takedown_title is the title of the published version at that moment, so the admin and
-- the author can still name a post whose author unpublished it meanwhile (no draft text is ever copied).
-- takedown_seen_at is set when the author has seen the one-time notice; a new takedown clears it.
ALTER TABLE posts ADD COLUMN takedown_at TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN takedown_by TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN takedown_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN takedown_title TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN takedown_seen_at TEXT NOT NULL DEFAULT '';
CREATE INDEX posts_takedown ON posts (takedown_at) WHERE takedown_at <> '';

-- The admin's default model per purpose; no row means the service default. Images and videos stay on tenant_tool_policy.
CREATE TABLE model_defaults (
  purpose  TEXT PRIMARY KEY CHECK (purpose IN ('chat','summary','blog_assistant')),
  model_id TEXT NOT NULL,
  set_by   TEXT NOT NULL,
  set_at   TEXT NOT NULL
);

-- What an admin action says about itself (a takedown's reason, a default's model). Never read back by the admin API.
ALTER TABLE audit_log ADD COLUMN detail TEXT;
