# Runbook — Memory & Skills and personal instructions (issue #111)

The account menu's **Memory & Skills** panel (read only while open) covers three things, all from the caller's own
workspace.

## Memory: UPSTREAM_BLOCKED

Hermes v0.22.5 exposes no memory route: the gateway route table (`gateway/platforms/api_server.py`, `/v1/*` and
`/api/*`) has `/v1/skills` and `/v1/toolsets` but nothing for facts or notes. Antares therefore shows that viewing,
editing and forgetting a fact is unavailable and points to the operator. It never reads `memory_store.db`, mounts a
profile or runs a script against one (KNOW-002/003). `TestCrossUserThreatNotYetBuilt` ("memory facts") fails the day
a memory path is routed without a test. When Hermes ships a documented per-profile memory API, KNOW-006 needs an
upstream read-back after each edit, not an optimistic update.

## Skills: read-only

The list is the `skills` part of `GET /api/capabilities`, which reads the caller's profile's `GET /v1/skills` (name,
description, category; `internal/hermes/discovery.go`). Searchable in the browser. There is no write or delete:
Hermes offers none, and `GET /v1/skills` returning data does not imply one. A skill's full text is not served by Hermes.

## Personal instructions

`GET/PUT /api/me/personality` (Active guard, same origin; `{"text": "…"}`, up to 2,000 characters, empty clears)
stores the text in `user_preferences.personality` (migration 0020). Each of the user's later runs sends it as the
`instructions` field of `POST /v1/runs`; Hermes puts that into the run's ephemeral system prompt, appended after the
profile's own prompt (`agent/chat_completion_helpers.py`), and Antares prefixes it as preferences that never override
the instructions above. Nothing is written to a profile file, a SOUL or another profile. The base agent policy is the
operator's and is not shown. `TestPersonalityAppliesToTheOwnersNextRunOnly` checks A's run carries it, B's does not,
and clearing it stops it.
