-- Images a user attached to a run (PRD §27.3 MEDIA-EXT-001/002, issue #101). The bytes stay in
-- assets and travel to Hermes inline for that one run; this only records which of the owner's
-- assets went with which run, so history can show a thumbnail while the asset still exists.
-- asset_id has no foreign key on purpose: deleting a staged image must not rewrite history.
CREATE TABLE run_images (
  run_id     TEXT NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id),
  position   INTEGER NOT NULL CHECK (position >= 0),
  asset_id   TEXT NOT NULL,
  media_type TEXT NOT NULL,
  PRIMARY KEY (run_id, position),
  FOREIGN KEY (run_id, user_id) REFERENCES runs (id, user_id)
);
