# Runbook — tenant toolset policy

Issue #13, applied by issue #41. The allowed set is **operator configuration**, not a constant in
Antares code: `ANTARES_TENANT_ALLOWED_TOOLSETS` (Antares, enforced by verification) and
`PROVISIONER_ALLOWED_TOOLSETS` (the provisioner, applied to each profile) take the same comma list,
default `web,vision,image_gen,skills,memory,todo,clarify`. The floor below can never be allowed. Set both
variables to the same value; if the provisioner allows more than Antares, `toolset_policy` fails.

**The provisioner owns each profile's `agent.disabled_toolsets`.** `/provision` and `/rotate` both write
it (every toolset Hermes reports that is not allowed), so a hand edit to one profile is overwritten by
the next provision or rotate. Narrowing a single profile by hand is not durable; narrow the variable.

## Status

- **Enforced and demonstrated on a live profile** (Hermes v0.22.2, 2026-10-23, throwaway profile, since
  deleted): after the provisioner's `config set`, `GET /p/<profile>/v1/toolsets` with that profile's key
  returned 200 with `web, vision, image_gen, skills, todo, memory` enabled and every denied toolset
  (`terminal, file, code_execution, delegation, cronjob, session_search, connections, browser`) reported
  `enabled: false`; the rest of the 27 reported toolsets were also off. The `config set` was the provisioner's exact command, run by hand (not through a running provisioner). Output below.
- Phase 0's filesystem-isolation FAIL stands. This policy is a gate on which tools exist, not a sandbox.
- The two open questions below (`web_extract`, `skill_manage`) are still unverified.

```
GET /p/u_probezz02/v1/toolsets -> 200 object=list platform=api_server
enabled : ['web', 'vision', 'image_gen', 'skills', 'todo', 'memory']
disabled: ['browser', 'terminal', 'file', 'code_execution', 'video', 'video_gen', 'x_search', 'tts', 'stt', 'kanban', 'context_engine', 'session_search', 'connections', 'clarify', 'delegation', 'cronjob', 'discord', 'discord_admin', 'yuanbao', 'computer_use', 'a2a']
denied all reported & disabled: True
```

(`clarify` is off for `api_server` on this build by default, so it reports disabled although allowed;
the verifier only requires that nothing outside the allow-list is enabled.)

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

## How to apply

`tools/hermes-provisioner` applies the policy on `/provision` and `/rotate` with
`hermes -p <slug> config set agent.disabled_toolsets '[...]'`, in the profile's own `config.yaml`, never
`default`'s. To change the policy, change `PROVISIONER_ALLOWED_TOOLSETS` and
`ANTARES_TENANT_ALLOWED_TOOLSETS` together, restart the provisioner, and rotate the affected profiles.
Verify by reading `/p/<profile>/v1/toolsets` with the profile's key: every toolset under "Disabled" must
report `enabled: false`. Antares' `toolset_policy` check does exactly that on every verification.

## Guard cases

Tests, with fabricated data: `TestNarrowedAllowListStillVerifies` and the `toolset_policy` cases in
`internal/hermes/verify_test.go` (a denied or unlisted toolset enabled fails the check);
`TestProvisionerToolsetListsMatchAntares` (the provisioner's lists equal Antares' and the fixture);
the provisioner's `TestNarrowedPolicyReachesProvisionAndRotate`.

## Unknown names in `agent.disabled_toolsets` (issue #41, PR #42 review)

Measured on the deployed build (Hermes v0.22.2 (2026.9.24)) on 2026-10-23, against a throwaway profile
`u_probezz01` (deleted afterwards). The provisioner's exact command, with its full `disabledToolsets`
list as the JSON value:

```
$ hermes -p u_probezz01 config set agent.disabled_toolsets '["terminal","file","code_execution","delegation","cronjob","session_search","connections","browser","video","video_gen","x_search","tts","stt","kanban","context_engine","discord","discord_admin","yuanbao","computer_use","a2a"]'
✓ Set agent.disabled_toolsets = ['terminal', 'file', 'code_execution', 'delegation', 'cronjob', 'session_search', 'connections', 'browser', 'video', 'video_gen', 'x_search', 'tts', 'stt', 'kanban', 'context_engine', 'discord', 'discord_admin', 'yuanbao', 'computer_use', 'a2a'] in /opt/data/profiles/u_probezz01/config.yaml
exit 0
```

The same list with `"zz_not_a_toolset"` appended (an invented name):

```
✓ Set agent.disabled_toolsets = [... 'computer_use', 'zz_not_a_toolset'] in /opt/data/profiles/u_probezz01/config.yaml
exit 0
```

Read back with `hermes -p u_probezz01 config get agent.disabled_toolsets` and from the profile's
`config.yaml`: every name was persisted, the unknown one included, as a YAML list under
`agent.disabled_toolsets`. **The deployed build does not reject unknown names**, so no filtering is
needed and `/provision` has no 502 path from a name mismatch.

Effective view, `hermes -p u_probezz01 tools list --platform api_server`, after the full list was set:
only `web`, `vision`, `image_gen`, `skills`, `todo`, `memory` were reported enabled among the toolsets
named in this runbook (`clarify` is off for `api_server` by default on this build). None of the denied
toolsets or `a2a` / `computer_use` was enabled.

The gateway route itself was read afterwards on a second throwaway profile; see "Status". That profile's
`config set` was run by hand with the provisioner's command and list, not through a running
provisioner, so the provisioner's HTTP path on the live host is the one thing not exercised end to end.
