# Runbook — tenant toolset policy

Issue #13. This is a **documented default** for each tenant profile's `agent.disabled_toolsets`. The
admin owns it and may change it per profile; it is not a constant in Antares code.

## Status

- **Per-profile routing is NOT delivered.** With a second profile present the gateway's `api_server`
  adapter did not start, so `/p/<profile>/v1/...` could not be reached at all. Diagnosis:
  [issue #13 comment 6333799116](https://github.com/raufimusaddiq/antares/issues/13#issuecomment-6333799116).
  It is a separate ticket.
- **Everything below is UNVERIFIED.** Nothing was read back through `/p/<profile>/v1/toolsets`,
  because that route could not be reached. The policy is **not enforced or demonstrated on any
  profile**; it is a proposal. Toolset names and tool lists come from the committed `/v1/toolsets`
  fixture (the `default` profile). The reasons come from the Phase 0 findings (section 6), not from a
  tenant test.
- No guard case exists for `/p/<profile>/...`. A check against a route that cannot be served cannot
  pass, so none is pinned.

Until a tenant policy is verified end to end, Antares may serve **one trusted user** only (AGENTS.md).

## Enabled for a tenant

| Toolset | Tools (from fixture) | Note |
|---|---|---|
| `web` | `web_search`, `web_extract` | See open question 1. |
| `vision` | `vision_analyze` | |
| `image_gen` | `image_generate` | |
| `skills` | `skill_manage`, `skill_view`, `skills_list` | Writes into the profile's own skills. See question 2. |
| `memory` | `memory` | Writes the profile's own memory; required for per-user memory (PRD). |
| `todo` | `todo_list` | |
| `clarify` | `clarify` | Currently *disabled* on `default`; enabling it is a tenant-profile choice. |

## Disabled, with the reason

| Toolset(s) | Reason |
|---|---|
| `terminal`, `file`, `code_execution` | The isolation hole. Shell/file access from one profile reads another profile's `.env` and state on the shared filesystem (Phase 0 section 6). |
| `delegation`, `cronjob` | **Privilege escalation.** Both start an agent turn with the *full* toolset, re-granting everything disabled. Leaving either on makes the policy decorative. |
| `session_search` | Reads the shared session store: cross-tenant read. |
| `connections` | Holds third-party service credentials; a tenant would act as the owner elsewhere. |
| `browser` | A full browser with cookies and sessions (includes `browser_vault`); can act as a logged-in user. |
| `discord`, `discord_admin`, `a2a`, `computer_use`, `kanban`, `video`, `video_gen`, `x_search`, `tts`, `stt`, `context_engine` | Not needed for v1 chat. |

`yuanbao` appears in the fixture, is disabled on `default`, and is on neither list. The default for a
new profile should disable it too (not needed for v1 chat). Decide explicitly rather than inherit.

## Open questions (escalation review; not resolved)

1. **`web_extract`** fetches URLs from the gateway host. If it accepts `file://` or internal addresses
   (for example `hermes:8642`, or other containers on `hermes_edge`) it is a read path across the
   boundary. Not tested. Needs a check before `web` is called safe.
2. **`skill_manage`** writes skill files into the profile. Skills are instructions, but whether a skill
   can carry an executable script that runs without `terminal` is not established. Not tested. If it
   can, `skills` should be read-only (`skill_view`, `skills_list` only) for tenants.

Neither is confirmed as an escalation path; neither is cleared. Until checked, treat both as
unverified.

## How to apply (once routing is delivered; unverified)

Set `agent.disabled_toolsets` in the tenant profile's own `config.yaml`, never in `default`'s. Then
read `/p/<profile>/v1/toolsets` and confirm every toolset under "Disabled" has `enabled: false` and
the "Enabled" set is exactly as listed. Until that has been done, none of this is verified.

## Guard case

Not added. Add a `/p/<profile>/v1/toolsets` case, with a fabricated fixture, in the PR that makes the
route reachable, and show it failing against a profile whose policy was not applied. The
`/p/<profile>/v1/models` 404 is deliberately not pinned either.
