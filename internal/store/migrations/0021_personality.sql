-- #111 (KNOW-*, LLM-ACC-012): the user's own additional instructions, sent with each of their runs as Hermes'
-- per-run instructions (lower priority than the profile's base prompt). NULL means none.
ALTER TABLE user_preferences ADD COLUMN personality TEXT;
