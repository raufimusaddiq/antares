-- ListAudit pages by keyset over (created_at DESC, id DESC); the single-column index
-- audit_log_created cannot serve that sort, so every page sorted in memory.
CREATE INDEX audit_log_keyset ON audit_log (created_at DESC, id DESC);
