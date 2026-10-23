-- Web Push on run completion (PRD §21.2, UXP-002, issue #59).
--
-- push_mode is the user's choice and defaults to off: there is no silent default
-- subscription. A subscription row is one browser/device. The push endpoint and its
-- keys are capability material (anyone holding them can push to that browser), so
-- they are sealed with the secret box, bound to the row id, never stored in clear;
-- endpoint_hash is only for de-duplication and for a device to recognise itself.
CREATE TABLE user_preferences (
  user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  push_mode  TEXT NOT NULL DEFAULT 'off' CHECK (push_mode IN ('off','all','failures')),
  updated_at TEXT NOT NULL
);

CREATE TABLE push_subscriptions (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint_hash   TEXT NOT NULL UNIQUE,
  sealed_endpoint BLOB NOT NULL,
  sealed_keys     BLOB NOT NULL,
  created_at      TEXT NOT NULL,
  UNIQUE (id, user_id)
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);

-- One row per {subscription, run, kind}: the claim that makes a notification
-- at-most-once across retries, restarts and a re-attached run.
CREATE TABLE push_delivery_events (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subscription_id TEXT NOT NULL REFERENCES push_subscriptions(id) ON DELETE CASCADE,
  run_id          TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  event_kind      TEXT NOT NULL CHECK (event_kind IN ('completed','failed')),
  created_at      TEXT NOT NULL,
  delivered_at    TEXT,
  UNIQUE (subscription_id, run_id, event_kind)
);
CREATE INDEX push_delivery_events_created ON push_delivery_events (created_at);
