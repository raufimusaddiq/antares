-- Per-chat model choice (PRD §27.1 LLM-002, issue #100). All NULL means "the profile's
-- Hermes default": a new chat starts there and never inherits another chat's choice.
-- Provider and model are the ids Hermes listed in GET /api/model/options when the choice
-- was saved; reasoning_effort and service_tier are only set for a model that listed support.
ALTER TABLE chats ADD COLUMN model_provider TEXT;
ALTER TABLE chats ADD COLUMN model_id TEXT;
ALTER TABLE chats ADD COLUMN reasoning_effort TEXT;
ALTER TABLE chats ADD COLUMN service_tier TEXT;
