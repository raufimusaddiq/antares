-- Provisioning state (PRD §5.3, issue #22). The profile slug is chosen by the
-- server and stored before the helper is called, so a retry after a crash asks
-- the helper for the same profile instead of creating a second one.
--
-- tenant_detail is a short, server-written reason from a fixed vocabulary; it
-- is shown to admins and never holds upstream error text or credential data.
-- tenant_next_attempt_at is both the backoff clock (FAILED) and the lease
-- expiry (CONFIGURING), so an interrupted run is picked up again.
ALTER TABLE users ADD COLUMN tenant_profile TEXT;
ALTER TABLE users ADD COLUMN tenant_detail TEXT;
ALTER TABLE users ADD COLUMN tenant_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN tenant_next_attempt_at TEXT;
ALTER TABLE users ADD COLUMN tenant_checked_at TEXT;

CREATE UNIQUE INDEX users_tenant_profile ON users (tenant_profile) WHERE tenant_profile IS NOT NULL;
