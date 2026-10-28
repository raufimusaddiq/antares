# Runbook — tenant toolset policy

Issue #13, applied by issue #41. The allowed set is **operator configuration**, not a constant in
Antares code: `ANTARES_TENANT_ALLOWED_TOOLSETS` (Antares, enforced by verification) and
`PROVISIONER_ALLOWED_TOOLSETS` (the provisioner, applied to each profile) take the same comma list,
default `web,skills,memory,todo,clarify`. The floor below can never be allowed. Set both
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
- **Cross-tenant tool proof (#161), Hermes v0.22.5, 2026-10-28:** see "A/B tool isolation proof" below.
  It moved `vision` and `image_gen` into the floor.

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
| `vision`, `image_gen` | **Cross-tenant read (#161).** `vision_analyze` and image-generation reference images load any local path the gateway user can read: under the local terminal backend Hermes' resolver allows "any path" (`tools/image_source.py` `_permitted_host_read_target`), so B can load an image from A's profile. Measured below. Image input in chat is unaffected: Antares sends the picture to the model as `image_url` data, not through this tool. |
| `discord`, `discord_admin`, `a2a`, `computer_use`, `kanban`, `video`, `video_gen`, `x_search`, `tts`, `stt`, `context_engine` | Not needed for v1 chat. |

`yuanbao` appears in the fixture, is disabled on `default`, and is on neither list. The default for a
new profile should disable it too (not needed for v1 chat). Decide explicitly rather than inherit.

## A/B tool isolation proof (#161)

`scripts/tool_isolation_probe.py`, run through `scripts/smoke_fixtures.py` against production (disposable accounts A
and B and their profiles, removed afterwards: cleanup clean). It plants canaries in A's profile (a text file in its
home, an image in its media cache, a skill), resolves the tools a B run is actually given (the `api_server` platform
list minus B's `agent.disabled_toolsets`), calls them directly under B's `HERMES_HOME`, and finally asks B's assistant
in a real chat to fetch A's canary file. Every result, the Hermes version and the container image are in the report;
a different Hermes build invalidates it (the #175 staleness check).

Writes inside the Hermes container: the canaries and one skill, as the `hermes` user, only in the two disposable fixture
profiles that `smoke_fixtures.py` deletes afterwards. That is more than the profile create/delete AGENTS.md allows by
default; the owner authorised it on 2026-10-28 (full control, test in production while Antares has no users). Running
the probe again needs the same authorisation.

    sudo python3 -I scripts/smoke_fixtures.py run --db <antares.sqlite> --workdir /var/backups/antares/smoke-fixtures \
      --timeout 1500 -- python3 -I scripts/tool_isolation_probe.py https://<origin> <antares.sqlite> <result.json>

Before the fix (`docs/hermes/fixtures/tool-isolation-probe-before-2026-10-28.json`, Hermes v0.22.5): B's runs were
given `vision_analyze`, and Hermes' image resolver, called under B's profile, returned the bytes of A's canary image
(finding `image_resolver_reads_other_profile: true`). Everything else held.

1. **`web_extract`: safe.** All 14 targets refused before any fetch with "Blocked: URL targets a private or internal
   network address": `file://` and a bare path into A's profile, loopback (`127.0.0.1`, `localhost`, `[::1]`, decimal
   and hex forms), the container names `hermes` and `antares`, the provisioner on the docker bridge, RFC 1918 addresses,
   link-local metadata `169.254.169.254` and `metadata.google.internal`. `security.allow_private_urls` is off. Hermes
   checks every redirect target the same way (`tools/url_safety.py`).
2. **`skill_manage`: no execution path.** Skills are files under the profile's own `skills/`; `skill_view` refused A's
   skill by name, by `../../<A>/...` and by absolute path. The only code that runs skill content is the inline
   ``!`cmd` `` expansion, gated by `skills.inline_shell`, which is off (Hermes default; no profile sets it). A skill B
   wrote with ``!`cat <A's canary>` `` came back unexpanded. **Upstream finding (low):** `skill_manage` on a name B
   does not have answers with the other profiles that do have it, naming A's profile id (an opaque slug, no content).
   It is Hermes code; recorded, not fixable here.
3. **Antares routes:** B got 404 on A's chat, messages, run, run events, chat search and artifact, and global search
   found nothing of A's.

After the fix: the same probe, re-run once this is deployed; its result is committed as
`docs/hermes/fixtures/tool-isolation-probe-2026-10-28.json` (not in the tree until then).

**Upgrading to the #161 floor.** A list that still names `vision` or `image_gen` now stops both processes at
startup: the provisioner (`tools/hermes-provisioner/main.go`, refused policy) and Antares (`internal/config`). Before
starting the new binaries, remove both names from `PROVISIONER_ALLOWED_TOOLSETS` in `/opt/antares/provisioner.env` and
`ANTARES_TENANT_ALLOWED_TOOLSETS` in `/opt/antares/antares.env` (or leave both unset for the default). Then re-verify every
existing tenant (admin `tenant-verify`): `/rotate` rewrites its `agent.disabled_toolsets`; until then `toolset_policy`
would fail its next verification.

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
