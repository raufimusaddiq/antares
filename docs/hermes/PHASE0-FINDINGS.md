# Phase 0 findings — Hermes integration and security proof

Issue #1. Probed 2026-10-22 against the live `hermes` container. Hermes was not reconfigured or restarted.

**Mutations, stated plainly.** Authorised on #1: `hermes profile create` / `delete` for `testA`/`testB`.
**Not authorised, and done anyway:** (a) a probe run as root inside the container left root-owned
directories inside the `testa` profile, which made `hermes profile delete testa` fail; (b) I then
hand-deleted that profile directory with `rm -rf`, by exact path. Issue #1 covers neither a root write
into the production container nor a manual delete of a profile path. Both stayed inside the disposable
`testa` profile; nothing under `default` was written. (A canary write into `testb` was attempted and
blocked by the permission classifier; I did not retry it.)

> **Staleness (#175, 2026-10-27).** This evidence was captured on Hermes Agent v0.22.2 (image `sha256:36099926…`).
> Production now runs v0.22.5, image `sha256:9c5561579f3f…` (built 2026-10-23), so the findings below describe an older
> build. Current evidence on the running build: three production smoke runs on 2026-10-27 passed every tenant check
> (endpoint, profile_identity, credential_isolation, session_ownership, toolset_policy, memory_boundary) and 14 of 14
> cross-user probes (`docs/runbooks/smoke-test.md`). Tool-level isolation (A/B negative tool probes) is still unproven and
> is tracked in #161. Re-run the smoke and update this note after every Hermes upgrade.

## Verdict

**Phase 0 FAILS. Multi-user is NOT demonstrated safe on this deployment. Phase 1 multi-user scope is blocked until the open items below pass.**
Per AGENTS.md this is a stop-and-report: nothing was built around it and no test was weakened.
Antares may be used by **one trusted user** only.

Two independent blockers, each sufficient on its own:

1. **Profile routing was not reachable.** `GET /p/testa/v1/models` returns 404 for a non-default
   profile, so deliverables 4, 5 and 7 (two bindings, per-profile key tests, A-vs-B negative tests)
   could not be run. The owner records on #1 that `multiplex_profiles` is already `true` (unset in
   `config.yaml`, resolved to on by default) and that the real gate is `gateway.profile_routes: []`
   (a profile prefix 404s until the profile is registered there). Registering a route is a config
   change, which Phase 0 may not make. **Discrepancy, not resolved:** the gateway boot log says
   `Single-profile install: gateway.multiplex_profiles unset, serving the default profile only`, and
   the mode is decided at boot from the profiles present then. So "already true" is the owner's
   statement and the source default, not something I observed on the wire; whether it flips with more
   profiles needs a gateway restart, which is also out of scope. Either way it is a config/restart
   action for the owner.
2. **No tool filesystem isolation.** See below. This is the answer the issue says is "more valuable
   than a green build".

## 1. Image pinning — dropped as a deliverable; API contract test instead

Decided by the owner on #1: the requirement is a stable API contract, not a frozen image. Pinning
never bound anything: the image the container is *running* is not the `latest` on disk (running image
ID `sha256:36099926da31…`, disk `sha256:7a69201726bc…`, `RepoDigests` empty), so a pin would describe
an image that is not the one serving. Hermes may update at any time.

The guard is `scripts/hermes_contract.py check`, built from the captured fixtures. It exits 1 when:
a status code differs; a key is added or removed; a value changes type (null, bool, int, float,
string, list, object); a list changes length or an item's shape; a fixture is missing; Hermes is
unreachable; or a toolset's `enabled` flag in `/v1/toolsets` differs (the failure names the toolset, e.g. `toolsets: browser: False -> True`, or `computer_use: added`/`removed`). All other values are ignored.

**What it does not guard — read this before relying on it.** `/v1/toolsets` is read for the profile
served at the URL called, which is the **default** profile. The `enabled` check therefore guards the
default profile's flags only. It does **not** observe any tenant profile's `disabled_toolsets`:
`CASES` has no `/p/<profile>/…` entry. A toolset becoming enabled in a tenant profile would go
unnoticed by *this* guard. (Originally the route could not be reached at all, so no such case could be
captured; tenant profiles are routable now, and a `/p/<profile>/v1/toolsets` read was captured once, by
hand, in issue #41, see the update at the end of this file.) The tenant-side check that exists is
Antares' own `toolset_policy` verification (`internal/hermes/verify.go`), run against each tenant
profile before it is `READY`; it is not part of this script.
Closing the remaining gap here needs a `/p/<profile>/v1/toolsets` case with its own fixture (open
item 1).

Verification: `scripts/test_hermes_contract.py` is an offline self-test (no network) run by the CI
`security` job. Against the round-1 guard (which compared only the first list element),
the same five toolset mutations — one added, one removed, one swapped and enabled, one item type
change, one newly enabled — all passed unnoticed; the self-test covers each and the current code
fails on each. It also passes against the live API.
Observed version at capture: Hermes Agent v0.22.2 (2026.9.24), upstream `d923a5eb`.

## 2. Capability snapshot

Fixtures: `docs/hermes/fixtures/` (`*.status` = HTTP status, `*.body.json` = body). **Shapes are real;
every number, timestamp and URL is fabricated, and every string is redacted unless its key is on the script's allowlist (free text such as titles, descriptions and message bodies is never captured).** They are produced by one script, reproducible with:

```
# from a container on hermes_edge; the key comes from the environment and is never written
docker run --rm --network hermes_edge -e HERMES_API_KEY -v "$PWD":/w:ro \
  python:3.11-slim python3 -I /w/scripts/hermes_contract.py check       # the contract test
# same, with `capture`: prints a diff against the baseline and changes nothing (exit 1 if it differs).
# Only `capture --accept` (writable mount) replaces the baseline; review the diff before committing it.
```

The script aborts if any response contains the key.

| Endpoint | Status |
|---|---|
| `GET /health` (no auth), `/health/detailed`, `/v1/capabilities`, `/v1/models`, `/v1/toolsets` | **verified on the wire**, fixture committed |
| auth matrix on `/v1/capabilities`: no key / wrong key → 401, owner key → 200 | **verified on the wire** |
| `GET /p/<profile>/…` for an unregistered profile → 404 | **verified on the wire, once, prose only** — deliberately **not** a fixture or a guard case: it is expected to change when profile routes are registered, and the guard must not fail on that. |
| `/v1/chat/completions`, `/v1/responses`, `/v1/runs*` (status, events SSE, approval, steer, stop) | **UNVERIFIED** — listed by `/v1/capabilities` and the route table only; not called |
| `GET /api/sessions?limit=1&offset=0` on a fresh tenant profile | **verified on the wire for the empty list envelope only** (issue #41): `{"object":"list","data":[],"limit":1,"offset":0,"has_more":false}`, fixture `sessions-empty`. A non-empty list carries real session data, so the item shape is still **UNVERIFIED**. |
| Run `usage` block (issue #81) | **read from the deployed container's source, not called on the wire**: `GET /v1/runs/{id}` and the terminal `run.*` event carry `usage` = `input_tokens` (includes cache), `output_tokens`, `total_tokens`, `cache_read_tokens`, `cache_write_tokens` (`gateway/platforms/api_server_runs.py:86-91`, `:867-874`), summed over the run's model calls (`agent/turn_usage.py:181`). Zero means not counted. No cost and no context occupancy are reported. Antares parses it in `internal/hermes/usage.go`; still unrecorded as a fixture: capturing one needs a real run on the deployed Hermes (a model call with the operator's key), which AGENTS.md and the deployment runbook reserve for the owner. Until then a reply recovered by polling has no known model, so Antares shows tokens but no cost for it. |
| `/api/sessions` CRUD, `/messages`, `/fork`, `/chat`, `/chat/stream`, `/model` | **UNVERIFIED** — route table only; responses carry the owner's real data, so no fixture. Do not code against a guessed shape. |
| `GET /v1/skills`, `/api/model/options` | **response shape read from the deployed container's source, not called on the wire** (`gateway/platforms/api_server.py` `_handle_skills` ~L2875, `_handle_model_options` ~L2434; `hermes_cli/inventory.py` `build_model_options_payload`): skills `{"object":"list","data":[{name,description,category}]}`; options `{"providers":[{slug,name,models[],authenticated,…}],"model","provider"}`. Antares discovery (`internal/hermes/discovery.go`) reads only those fields; fixtures in `internal/hermes/testdata/` are source-shaped with fabricated values. |
| `/api/jobs*`, artifacts, browser-control | **UNVERIFIED** — route table only |

The Phase 1 adapter must capture its own sanitized fixtures for every UNVERIFIED row (with test
data, not the owner's) before depending on it.

Flags that matter for PRD §5 / capability-driven UI (from `/v1/capabilities`): `memory_write_api:
false`, `admin_config_rw: false`, `jobs_admin: false`, `audio_api: false`, `realtime_voice: false`,
`browser_extension_control.enabled: false`. There is **no memory read/list API** in the route table;
memory is reachable only through the agent's `memory` tool. `runtime.tool_execution: "server"`.

**Enabled toolsets on the deployed default (13, from `fixtures/toolsets.body.json`):** web, terminal,
file, code_execution, vision, image_gen, skills, todo, memory, session_search, connections,
delegation, cronjob. Disabled: browser, video, video_gen, x_search, tts, stt, kanban, and others.

## 3. Network proof

```
docker run --rm --network hermes_edge curlimages/curl -s -m 5 http://hermes:8642/health
{"status": "ok", "platform": "hermes-agent", "version": "0.22.2"}      # HTTP 200
```

`hermes:8642` resolves (Docker DNS on the bridge) and answers from inside `hermes_edge`; the port is `expose`d only, not
published. Caveat: this used a throwaway curl container, not an `antares` container (none exists
yet). The Antares container must join `hermes_edge`; edge route decision is still the owner's.

## 4/5/7. Two bindings, per-profile keys, A↔B negative tests — NOT PROVEN

- `hermes profile create testA|testB` succeeded (names are lower-cased to `testa`/`testb`; each gets
  its own profile directory with its own `.env`, `config.yaml`, `SOUL.md`, skills, cron).
- `GET /p/testa/v1/models` with the owner key → **404 `Unknown or unconfigured profile`**, before and
  after the profiles existed (observed once on 2026-10-22; no fixture, see section 2). See the discrepancy in the verdict
  about `multiplex_profiles` and `gateway.profile_routes`.
- Source also shows that a named profile authenticates with its **own** `API_SERVER_KEY` and fails
  closed (401) if it has none, and never inherits the owner's. That is encouraging but **unverified
  on the wire**, because no profile can be reached. A cross-key failing call was therefore not made.
- Running a second gateway per profile (`hermes -p X gateway run` on another port) would be an
  alternative, but it starts new services in the production container; not authorised, not done.

## 6. Profile-vs-tool filesystem isolation — NOT ISOLATED

Evidence. All checks are read-only (`test -r`, `stat`, `ps`); no file content was read. Output is
verbatim. `<data>` is the Hermes data root inside the
container. Re-run on 2026-10-22 against the live container:

```
$ docker exec hermes sh -c 'ps -eo user,args | grep "gateway run --replace" | grep -v grep | cut -c1-8'
hermes
$ docker exec -u hermes hermes sh -c 'D=<data>; for f in .env state.db memories; do
    test -r $D/$f && echo "default/$f readable" || echo "default/$f NOT readable"; done'
default/.env readable
default/state.db readable
default/memories readable
$ docker exec hermes sh -c 'stat -c "%U %a" <data>/.env <data>/state.db <data>/memories'
hermes 600
hermes 600
hermes 755
```

So the process that runs tools is `hermes`, the default profile's credential file, state database and
memory directory belong to `hermes`, and `docker exec -u hermes` (the same user) can read all three.
**Recollection, not re-runnable evidence:** earlier in the same session, while the test profiles
existed, I saw (output not preserved; the profiles are deleted) that as that user another profile's credential file and persona file had
non-zero size via `wc -c < file`, and that another profile's directory could be listed. That part
cannot be re-run now.

Limits of this evidence: it proves OS-level readability by the gateway user, not that a *tool call*
reached the files. No tool call was driven.

- One gateway process serves the API **and executes tools** (`runtime.tool_execution: "server"`; no
  terminal sandbox backend is configured).
- All profiles live in one filesystem tree under one OS user, and that same user owns the default
  profile's state database, memory directory and credential file.
- Running as that user, I could read another profile's credential file and persona file, and the
  default profile's state database, memory directory and credential file, and list another profile's
  directory.
- Of the 13 enabled toolsets, these cross the boundary:
  - `terminal`, `file`, `code_execution` — read any path the OS user can read, so profile B's data and
    the default profile's credentials;
  - `session_search` — reads the session store;
  - `delegation` and `cronjob` — can start further agent turns that carry the full tool set, so they
    re-open the same access even if a direct tool were restricted;
  - `skills` (write) and `memory` — write into profile state.

Profiles are config/state namespaces, not sandboxes (PRD §5.5 confirmed). Per the owner's decision on
#1 (multi-profile in one container, owner and family only, risk accepted), isolation rests entirely on
`agent.disabled_toolsets` per profile. That means every one of the toolsets above must be disabled or
proven harmless per tenant profile, and Phase 0's negative tests must target exactly that. None of that
was tested here: no routable profile existed, and no end-to-end tool call was driven.

## 8. Admin bootstrap method (design, no Hermes dependency)

Out-of-band and break-glass; no auto-first-admin:

- Env `ANTARES_BOOTSTRAP_ADMIN_EMAILS` (runtime secret/config, names only in `.env.example`) lists
  verified Google emails that may be promoted. It does nothing by itself: first sign-in still creates
  `pending`.
- Promotion happens only via an operator CLI on the host: `antares admin grant --email …`, which writes
  an `admin_granted` audit row. The CLI needs shell/DB access to the antares container; there is no
  HTTP path to create the first admin and the first user to sign in never becomes admin.
- Break-glass revoke/rotation uses the same CLI. Admins stay content-blind (invariant 6).
This is a design only; it is untested because Antares has no code yet.

## Cleanup

`testa`/`testb` deleted; `hermes profile list` shows only `default`; aliases removed; no container
restart. `testa` needed the manual removal described in the header. `hermes profile delete` also leaves
small tombstone files in a hidden `.deleted` directory under the profiles directory, which I left.

## Open items for the owner

1. Register the test profiles in `gateway.profile_routes` (config change) so routing and per-profile key
   tests can be run, and confirm whether a restart is needed for multiplexing to switch on.
2. Decide the per-profile `agent.disabled_toolsets` set (see section 6), then run the A-vs-B negative
   tests against it. Until that passes, do not describe Antares as secure for multiple users.

## Update — issue #13 (2026-10-22)

Per-profile routing is **not delivered**. With a second profile present the gateway's `api_server`
adapter did not start, so `/p/<profile>/v1/...` was unreachable (see
[issue #13 comment 6333799116](https://github.com/raufimusaddiq/antares/issues/13#issuecomment-6333799116)).
The `disabled_toolsets` gap above was open at that point: no automated check existed.

### Update — issue #41 (2026-10-23)

Routing now works for a provisioned profile, and the toolset policy is no longer only proposed:
`tools/hermes-provisioner` writes `agent.disabled_toolsets` on `/provision` and `/rotate`, Antares'
`toolset_policy` check fails closed on any enabled toolset outside the allow-list, and a throwaway profile
read back through `GET /p/<profile>/v1/toolsets` with its own key showed every denied toolset
`enabled: false` (`docs/runbooks/tenant-toolsets.md`). What is **still unexercised**: the provisioner's
own HTTP path on the live host (the `config set` was run by hand), the two escalation questions in that
runbook (`web_extract`, `skill_manage`), and the A-vs-B cross-tenant negative tests. The Phase 0 FAIL
verdict and the isolation finding in section 6 stand unchanged: this policy limits which tools exist, it
does not make profiles sandboxes.

### Update — issue #161 (2026-10-28, Hermes v0.22.5)

The A-vs-B cross-tenant negative tests and both escalation questions were run against production with disposable
accounts (`scripts/tool_isolation_probe.py`, `docs/runbooks/tenant-toolsets.md` "A/B tool isolation proof"). `web_extract`
refuses every file, loopback, private, link-local, metadata and container-name target; `skill_manage` has no execution
path while `skills.inline_shell` is off; Antares answers 404 to B on every A route. One real hole was found and closed:
`vision_analyze` (and image-generation reference images) load any local path, so `vision` and `image_gen` joined the
floor. The verdict in section 6 still stands: profiles share one filesystem and one user, and the toolset floor is what
keeps a tenant's tools inside its own profile.
