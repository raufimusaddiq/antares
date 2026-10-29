# Runbook — tenant toolset policy

Issue #13, applied by issue #41. **An admin chooses the tenant toolsets in Admin → Capabilities → Assistant
tools** (`GET`/`PUT /api/admin/tool-policy`, stored in `tenant_tool_policy`, audited as `tool_policy.set`). Only
`hermes.OfferedToolsets` can be chosen: `web, vision, image_gen, video_gen, video, tts, stt, x_search, skills,
memory, todo, clarify`. The floor below is shown locked with its reason, and so are the toolsets not reviewed for
shared tenants (`discord, discord_admin, yuanbao, computer_use, a2a, kanban, context_engine`). Saving sends every
approved account with a ready or failed binding back through verification (the same as an admin's tenant-verify):
each run passes the policy to the provisioner's `/rotate`, which writes it to the profile, and the verifier's
`toolset_policy` check uses the same list. A run that started before the change is not marked ready: `MarkReady`
compares the policy version the run applied with the current one inside the READY transaction, after taking the
write lock, so no save can slip between the check and the commit (`the tenant tool policy changed during setup`);
it retries with the new policy. While re-verifying, an account's
chat pauses for about a minute.

The provisioner trusts the list only within its own copy of the offer (`offeredToolsets`, pinned to Antares' by
`TestProvisionerToolsetListsMatchAntares`): any other name, a floor toolset included, is a 400 and nothing runs.
So a caller holding the provisioner token still cannot enable the floor or an unreviewed toolset.

Until an admin saves a policy, the operator configuration below applies:
`ANTARES_TENANT_ALLOWED_TOOLSETS` (Antares, enforced by verification) and
`PROVISIONER_ALLOWED_TOOLSETS` (the provisioner, applied to each profile when a request names no toolsets) take
the same comma list, default `web,vision,image_gen,skills,memory,todo,clarify`. The floor below can never be allowed. Set both
variables to the same value; if the provisioner allows more than Antares, `toolset_policy` fails.
Both refuse the same bad input at startup: an unknown name, a floor toolset, or a set-but-blank value
(`" "`, `","`). Only an empty or unset variable means the default.

**The provisioner owns each profile's `agent.disabled_toolsets`.** `/provision` and `/rotate` both write
it (every toolset Hermes reports that is not allowed), so a hand edit to one profile is overwritten by
the next provision or rotate. Narrowing a single profile by hand is not durable; narrow the variable.

## Status

- **Demonstrated on a live profile** (Hermes v0.22.2, 2026-10-23, throwaway profile, since deleted).
  The provisioner's exact `config set` command was run by hand, not through a running provisioner. Then
  `GET /p/<profile>/v1/toolsets` with that profile's key returned 200 with
  `web, vision, image_gen, skills, todo, memory` enabled and every denied toolset
  (`terminal, file, code_execution, delegation, cronjob, session_search, connections, browser`) reported
  `enabled: false`; the rest of the 27 reported toolsets were also off. Output below.
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
`default`'s. To change the policy, use Admin → Assistant tools (above). Without a saved policy, change
`PROVISIONER_ALLOWED_TOOLSETS` and `ANTARES_TENANT_ALLOWED_TOOLSETS` together, restart the provisioner, and
rotate the affected profiles.
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


## Recovery after verification failure (issue #51)

Both admin requeue operations (`tenant-binding` and `tenant-verify`) reset the retry budget.
For an existing binding, the pipeline calls `/rotate`, which reapplies `agent.disabled_toolsets`,
then seals the returned key against the same binding ID and runs all verification checks. A missing
host profile (404) or key (409) from `/rotate` instead triggers `/provision`, then the same credential
replacement and verification. Recreating a deleted profile cannot restore its lost data.
Retries, interrupted runs, and reactivation also rotate existing bindings before verification.
Profile, binding ID, memory scope and private data stay in place. The old key is invalidated;
a lost rotation response is recovered by rotating again. No failed or partial verification unlocks chat.
A mixed report retains other failed checks without labelling those checks deterministic.

An enabled forbidden toolset carries `reapply_toolset_policy` remediation in the internal verifier
report. If it is still enabled after the helper ran, the run stops immediately as `FAILED` with
`operator attention required: deterministic failure in toolset_policy:` in the admin detail and a `tenant.attention`
audit event. Attempts count actual failed runs, rather than being set artificially to the limit.
The operator should align the two allow-list variables, fix helper access or policy application,
and requeue. A helper success alone never proves policy compliance.

Unreachable endpoints, malformed evidence and transient helper failures retry with bounded backoff.
At the retry limit, the detail instead says `operator attention required: retry budget exhausted:`
and the same attention event is recorded. Both stopped outcomes have a NULL next-attempt time;
the account remains blocked until an admin requeues it. These signals are available through the
existing admin user and audit APIs; there is no external notification service.

Tenant states remain:

| State | Entry and exit |
|---|---|
| `UNPROVISIONED` | New account; approval or requeue without a binding makes it eligible for a claim. |
| `CONFIGURING` | Claimed, requeued with a binding, or reactivated with a binding. Helper and verification run here; an expired lease resumes the run. |
| `READY` | Full verification passed. Requeue or reactivation requires fresh verification; suspension disables access. |
| `FAILED` | Helper or verification failed. Transient failures retry after backoff; deterministic failures and exhausted budgets require operator attention and requeue. |
| `DISABLED` | Admin suspended the account and binding. Only reactivation returns it to setup. |

Regression tests cover both requeue modes repairing policy, a helper that leaves policy wrong,
deterministic versus transient retry behavior, lost rotation responses, owner-scoped replacement,
and suspension during repair. Existing verifier and provisioner tests still enforce the denied
floor, policy writes on rotation and failure before key issuance when policy application fails.

## Allowed means enabled for API chats (2026-10-29)

Hermes' default toolset for the `api_server` platform leaves out `clarify`, `tts`, `stt`, `video`, `video_gen` and
`x_search`, so writing only `agent.disabled_toolsets` left those off even when allowed (an Assistant tools switch had
no effect for them). The policy step now also runs `hermes -p <slug> tools enable <allowed...> --platform api_server`
(writes `platform_toolsets.api_server`), measured on a throwaway profile: `clarify` and `tts` went from disabled to
enabled. `agent.disabled_toolsets` is still written first and applied last, so nothing outside the allowed set can be
on. Existing profiles get it on their next provision or rotate (a tenant-verify, or saving Assistant tools).
