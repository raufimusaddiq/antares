-- SearchChats pages by (created_at, id, seq). created_at never changes; updated_at moves when a run
-- is admitted, so keyset paging on it skipped or repeated a chat whose run started mid-pagination.
DROP INDEX chats_user_status_updated;
CREATE INDEX chats_user_status_created ON chats (user_id, status, created_at DESC, id DESC);
