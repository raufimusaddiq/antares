# Runbook — Memory & Skills and personal instructions (issue #111)

The account menu's **Memory & Skills** panel (read only while open) covers three things, all from the caller's own
workspace.

## Memory: view only, through the provisioning helper

Hermes v0.22.5 exposes no memory route: the gateway route table (`gateway/platforms/api_server.py`, `/v1/*` and
`/api/*`) has `/v1/skills` and `/v1/toolsets` but nothing for facts or notes. The owner decided (2026-11-01, #248) to
show memory anyway, view only, the way the helper already serves skills and generated media.

- Hermes keeps a profile's memory in `memories/USER.md` (about the user) and `memories/MEMORY.md` (its own notes),
  entries separated by `\n§\n`. `GET /api/me/memory` (Active guard) asks the helper's `POST /memory` for the profile
  bound to the caller's account on the server; a client that names a profile is refused (400). The helper
  (`tools/hermes-provisioner/memory.go`) reads the two files in the Hermes container as the hermes user, opening every
  path component with `O_NOFOLLOW` (a symlink is refused, never followed), and splits them with Hermes' own parser.
  It writes nothing: Hermes' own loader would create `memories/`, so it is not used.
- The panel lists both, as plain text. Entries Hermes blocks from the prompt as threats are still shown as stored,
  so the user can see them.
- There is no edit or forget: changing memory means asking the assistant in a chat (its memory tool). An edit route
  would need Hermes' own locking and write gate, and a read-back after each change (KNOW-006);
  `TestCrossUserThreatNotYetBuilt` ("memory edits") fails the day one is routed without a test.
- Tests: `TestMemoryIsTheCallersOwnOnly` (each person gets only their own profile's memory; accounts that may not
  use the assistant are refused before the helper is asked), `TestMemoryScriptReadsOnlyTheProfilesOwnFiles` (the
  script against real files: own profile read, symlinked profile, directory or file refused, nothing created).
- `memory_store.db` and other profile files are never read.

## Skills: read-only

The list is the `skills` part of `GET /api/capabilities`, which reads the caller's profile's `GET /v1/skills` (name,
description, category; `internal/hermes/discovery.go`). Searchable in the browser. There is no write or delete:
Hermes offers none, and `GET /v1/skills` returning data does not imply one. A skill's full text is not served by Hermes.

## Personal instructions

`GET/PUT /api/me/personality` (Active guard, same origin; `{"text": "…"}`, up to 2,000 characters, empty clears)
stores the text in `user_preferences.personality` (migration 0021). Each of the user's later runs sends it as the
`instructions` field of `POST /v1/runs`; Hermes puts that into the run's ephemeral system prompt, appended after the
profile's own prompt (`agent/chat_completion_helpers.py`), and Antares prefixes it as preferences that never override
the instructions above. Nothing is written to a profile file, a SOUL or another profile. The base agent policy is the
operator's and is not shown. `TestPersonalityAppliesToTheOwnersNextRunOnly` checks A's run carries it, B's does not,
and clearing it stops it.

The built-in writing rules (`docs/runbooks/writing-rules.md`) go first in the same `instructions`, then the OpenUI instruction, then
these preferences, so the person's own words about tone come last and win. `runs.writing_rules` records `v1`, `off` or NULL.

## Skills list on Hermes v0.22.5

The deployed Hermes' `GET /v1/skills` calls its skill lister with an argument the lister no longer accepts
(`include_editorial`) and answers 500 every time, so Capabilities showed "Hermes did not return a usable skills
list" while 53 skills were installed. When Hermes' own list fails, Antares asks the provisioning helper
(`POST /skills {slug}`, `tools/hermes-provisioner/skills.go`), which runs Hermes' own lister read-only with
`HERMES_HOME` set to the caller's own profile (so a person's own saved skills are included, and nobody else's). A
failure there leaves Hermes' reason in place. When a Hermes upgrade fixes the route, its list is used and the helper is
not asked.
