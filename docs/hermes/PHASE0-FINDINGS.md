# Phase 0 findings — Hermes integration and security proof

Issue #1. Probed 2026-10-22 against the live `hermes` container. Hermes was not reconfigured or restarted.

**Mutations, stated plainly.** Authorised on #1: `hermes profile create` / `delete` for `testA`/`testB`.
**Not authorised, and done anyway:** (a) a probe run as root inside the container left root-owned
directories inside the `testa` profile, which made `hermes profile delete testa` fail; (b) I then
hand-deleted that profile directory with `rm -rf`, by exact path. Issue #1 covers neither a root write
into the production container nor a manual delete of a profile path. Both stayed inside the disposable
`testa` profile; nothing under `default` was written. (A canary write into `testb` was attempted and
blocked by the permission classifier; I did not retry it.)

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
a status code differs; a key is added or removed; a value changes type; a list changes length or an
item's shape; Hermes is unreachable; or the per-toolset `enabled` flags in `/v1/toolsets` differ
(compared by toolset name, so the failure says which toolset changed). All other values are ignored.
The toolset check exists because isolation rests on per-profile `disabled_toolsets` (section 6): a
toolset silently becoming enabled is the change it must catch.

Verification: `scripts/test_hermes_contract.py` is an offline self-test (no network) run by the CI
`security` job. Against the previous version of the guard (which compared only the first list element),
the same five toolset mutations — one added, one removed, one swapped and enabled, one item type
change, one newly enabled — all passed unnoticed; the self-test covers each and the current code
fails on each. It also passes against the live API.
Observed version at capture: Hermes Agent v0.22.2 (2026.9.24), upstream `d923a5eb`.

## 2. Capability snapshot

Fixtures: `docs/hermes/fixtures/` (`*.status` = HTTP status, `*.body.json` = body). **Shapes are real;
every number, timestamp and URL is fabricated.** They are produced by one script, reproducible with:

```
# from a container on hermes_edge; the key comes from the environment and is never written
docker run --rm --network hermes_edge -e HERMES_API_KEY -v "$PWD":/w \
  python:3.11-slim python3 -I /w/scripts/hermes_contract.py capture     # or: check
```

The script aborts if any response contains the key.

| Endpoint | Status |
|---|---|
| `GET /health` (no auth), `/health/detailed`, `/v1/capabilities`, `/v1/models`, `/v1/toolsets` | **verified on the wire**, fixture committed |
| auth matrix on `/v1/capabilities`: no key / wrong key → 401, owner key → 200 | **verified on the wire** |
| `GET /p/<profile>/…` for an unregistered profile → 404 | **verified on the wire** |
| `/v1/chat/completions`, `/v1/responses`, `/v1/runs*` (status, events SSE, approval, steer, stop) | **UNVERIFIED** — listed by `/v1/capabilities` and the route table only; not called |
| `/api/sessions` CRUD, `/messages`, `/fork`, `/chat`, `/chat/stream`, `/model` | **UNVERIFIED** — route table only; responses carry the owner's real data, so no fixture. Do not code against a guessed shape. |
| `GET /v1/skills`, `/api/model/options`, `/api/jobs*`, artifacts, browser-control | **UNVERIFIED** — route table only |

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
  after the profiles existed (`fixtures/profile-prefix-unserved.*`). See the discrepancy in the verdict
  about `multiplex_profiles` and `gateway.profile_routes`.
- Source also shows that a named profile authenticates with its **own** `API_SERVER_KEY` and fails
  closed (401) if it has none, and never inherits the owner's. That is encouraging but **unverified
  on the wire**, because no profile can be reached. A cross-key failing call was therefore not made.
- Running a second gateway per profile (`hermes -p X gateway run` on another port) would be an
  alternative, but it starts new services in the production container; not authorised, not done.

## 6. Profile-vs-tool filesystem isolation — NOT ISOLATED

Evidence (read-only checks, `test -r` and byte counts; no file contents were read):

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
