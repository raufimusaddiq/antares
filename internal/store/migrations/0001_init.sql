-- Antares core schema (PRD §3, §5, §10). Timestamps are RFC 3339 UTC text.
-- role, approval_status and tenant_status are three separate fields (PRD §3).

CREATE TABLE users (
  id              TEXT PRIMARY KEY,
  google_issuer   TEXT NOT NULL,
  google_sub      TEXT NOT NULL,
  email           TEXT NOT NULL,
  display_name    TEXT,
  avatar_url      TEXT,
  role            TEXT NOT NULL CHECK (role IN ('USER','ADMIN')),
  approval_status TEXT NOT NULL CHECK (approval_status IN ('PENDING','APPROVED','REJECTED','SUSPENDED')),
  tenant_status   TEXT NOT NULL CHECK (tenant_status IN ('UNPROVISIONED','CONFIGURING','READY','FAILED','DISABLED')),
  created_at      TEXT NOT NULL,
  approved_at     TEXT,
  approved_by     TEXT REFERENCES users(id),
  last_login_at   TEXT,
  UNIQUE (google_issuer, google_sub)
);

-- Single-row latch: the first admin is created exactly once (AUTH-008).
CREATE TABLE admin_bootstrap (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL
);

-- Approval history. Targets and actors are users; admins see status, never content.
CREATE TABLE approvals (
  id             TEXT PRIMARY KEY,
  target_user_id TEXT NOT NULL REFERENCES users(id),
  actor_user_id  TEXT NOT NULL REFERENCES users(id),
  action         TEXT NOT NULL,
  from_status    TEXT,
  to_status      TEXT,
  reason         TEXT,
  created_at     TEXT NOT NULL
);
CREATE INDEX approvals_target ON approvals (target_user_id, created_at);

-- One Hermes profile binding per user. The API key is sealed (AES-256-GCM,
-- see internal/secrets); credential_fingerprint is a keyed hash whose UNIQUE
-- constraint refuses a bearer key reused across users.
CREATE TABLE tenant_bindings (
  id                     TEXT PRIMARY KEY,
  user_id                TEXT NOT NULL UNIQUE REFERENCES users(id),
  profile_name           TEXT NOT NULL UNIQUE,
  routing_mode           TEXT NOT NULL CHECK (routing_mode IN ('MUX_PREFIX','DEDICATED_PORT')),
  upstream_endpoint_id   TEXT NOT NULL,
  credential_ciphertext  BLOB NOT NULL,
  credential_key_version INTEGER NOT NULL,
  credential_fingerprint TEXT NOT NULL UNIQUE,
  memory_scope_key       TEXT NOT NULL UNIQUE,
  status                 TEXT NOT NULL CHECK (status IN ('CONFIGURING','READY','FAILED','DISABLED')),
  verified_at            TEXT,
  created_at             TEXT NOT NULL,
  UNIQUE (id, user_id)
);

CREATE TABLE app_sessions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked_at TEXT
);
CREATE INDEX app_sessions_user ON app_sessions (user_id);

-- Owned rows carry user_id, and child rows reference (id, user_id) so the
-- database itself refuses a row whose owner differs from its parent's.
CREATE TABLE chats (
  id                TEXT PRIMARY KEY,
  user_id           TEXT NOT NULL REFERENCES users(id),
  hermes_binding_id TEXT NOT NULL,
  hermes_session_id TEXT NOT NULL,
  title             TEXT,
  status            TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  UNIQUE (hermes_binding_id, hermes_session_id),
  UNIQUE (id, user_id),
  FOREIGN KEY (hermes_binding_id, user_id) REFERENCES tenant_bindings (id, user_id)
);
CREATE INDEX chats_user_updated ON chats (user_id, updated_at);

CREATE TABLE runs (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id),
  chat_id         TEXT NOT NULL,
  hermes_run_id   TEXT,
  idempotency_key TEXT NOT NULL UNIQUE,
  status          TEXT NOT NULL CHECK (status IN
                    ('PENDING','RUNNING','COMPLETED','FAILED','CANCELLED','DISCONNECTED','TIMED_OUT')),
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  UNIQUE (id, user_id),
  FOREIGN KEY (chat_id, user_id) REFERENCES chats (id, user_id)
);
CREATE INDEX runs_chat ON runs (chat_id, created_at);

-- Message metadata only. Hermes owns transcripts (PRD §10.2, PERF-005); there
-- is deliberately no content column.
CREATE TABLE messages (
  id                TEXT PRIMARY KEY,
  user_id           TEXT NOT NULL REFERENCES users(id),
  chat_id           TEXT NOT NULL,
  run_id            TEXT,
  role              TEXT NOT NULL CHECK (role IN ('user','assistant','tool','system')),
  hermes_message_id TEXT,
  created_at        TEXT NOT NULL,
  FOREIGN KEY (chat_id, user_id) REFERENCES chats (id, user_id),
  FOREIGN KEY (run_id, user_id) REFERENCES runs (id, user_id)
);
CREATE INDEX messages_chat ON messages (chat_id, created_at);

CREATE TABLE audit_log (
  id             TEXT PRIMARY KEY,
  actor_user_id  TEXT REFERENCES users(id),
  action         TEXT NOT NULL,
  target_kind    TEXT,
  target_id      TEXT,
  outcome        TEXT NOT NULL,
  request_id     TEXT,
  created_at     TEXT NOT NULL
);
CREATE INDEX audit_log_created ON audit_log (created_at);
