-- Progress of a running check (specs/006-ai-editor-blog, US2): the step the assistant's run is in, when that step began and when the last
-- event arrived from it. Derived from the run's own event stream (reasoning and tool starts and message deltas), never estimated.
ALTER TABLE post_checks ADD COLUMN stage TEXT NOT NULL DEFAULT 'reading' CHECK (stage IN ('reading','searching','writing'));
ALTER TABLE post_checks ADD COLUMN stage_at TEXT NOT NULL DEFAULT '';
ALTER TABLE post_checks ADD COLUMN last_event_at TEXT NOT NULL DEFAULT '';
ALTER TABLE post_checks ADD COLUMN lookups INTEGER NOT NULL DEFAULT 0;
