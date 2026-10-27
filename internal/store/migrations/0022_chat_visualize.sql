-- #153 (LLM-008): the per-chat Visualize toggle. On, the chat's runs carry the versioned OpenUI instruction as Hermes'
-- per-run instructions; off (the default), they carry none. Nothing global changes.
ALTER TABLE chats ADD COLUMN visualize INTEGER NOT NULL DEFAULT 0 CHECK (visualize IN (0, 1));
