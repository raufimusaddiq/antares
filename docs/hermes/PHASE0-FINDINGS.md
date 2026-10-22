# Phase 0 findings — Hermes integration and security proof

Issue #1. Probed 2026-10-22 against the live `hermes` container. Nothing in Hermes was reconfigured;
the only mutations were `hermes profile create/delete` for `testA`/`testB` (owner-authorised on #1).

## Verdict

**Phase 0 FAILS. Multi-user is NOT safe on this deployment. Phase 1 multi-user scope is blocked.**
Per AGENTS.md this is a stop-and-report: nothing was built around it and no test was weakened.
Antares may be used by **one trusted user** only.

Two independent blockers, each sufficient on its own:

1. **Profile routing is not available.** `gateway.multiplex_profiles` is unset, so `/p/<profile>/…`
   returns 404 for every non-default profile (even after the profile exists). Deliverables 4, 5, 7
   (two bindings, per-profile key tests, A-vs-B negative tests) could not be run. Enabling it needs a
   config/env change and a restart, which Phase 0 may not do.
2. **No tool filesystem isolation.** See below. This is the answer the issue says is "more valuable
   than a green build".

## 1. Pinned image

| Field | Value |
|---|---|
| Compose image ref | `nousresearch/hermes-agent:latest` (floating tag) |
| Image ID | `sha256:36099926da316742423544e055d4734777105ffe6341873af5459b957d0c2919` |
| Image created | 2026-10-15T06:20:46Z |
| Registry digest | **none** — `RepoDigests` is empty and `RepoTags` is empty for this image, so a registry digest cannot be pinned from this host. Finding: pin by image ID here; the owner should re-pull by digest to get a reproducible reference. |
| Version | Hermes Agent v0.22.2 (2026.9.24), upstream `d923a5eb`, Python 3.14.7 |

Command: `docker inspect hermes --format '{{.Image}}'`; `docker exec hermes hermes --version`.

## 2. Capability snapshot

Fixtures: `docs/hermes/fixtures/` (`*.status` = HTTP status, `*.body.json` = body). All captured live
through `hermes_edge`; the owner key was scrubbed (grep-verified absent). Not captured, deliberately:
`/api/sessions*` and `/v1/skills` return the owner's real data — shapes below come from the route
table in the container source (`gateway/platforms/api_server.py`), **not** from fixtures, and are
unverified at the wire level.

Verified live: `GET /health` (no auth, 200), `/health/detailed`, `/v1/capabilities`, `/v1/models`,
`/v1/toolsets`, auth matrix (no key → 401, wrong key → 401, owner key → 200).

Advertised by `/v1/capabilities` (and present in the route table): `/v1/chat/completions`,
`/v1/responses`, `/v1/runs` (+ status, events SSE, approval, steer, stop), `/api/sessions` CRUD +
messages + fork + chat + chat/stream + model lock, `/v1/toolsets`, `/v1/skills`, `/api/model/options`,
`/api/jobs*`, artifacts upload/download, browser-control.

Capability flags that matter for PRD §5 / capability-driven UI: `memory_write_api: false`,
`admin_config_rw: false`, `jobs_admin: false`, `audio_api: false`, `realtime_voice: false`,
`browser_extension_control.enabled: false`. There is **no memory read/list API** in the route table;
memory is only reachable through the agent's `memory` tool. `runtime.tool_execution: "server"` —
tools run on the API-server host.

Enabled toolsets on the deployed default: web, **terminal**, **file**, **code_execution**, vision,
image_gen, skills, todo, memory (browser disabled).

## 3. Network proof

```
docker run --rm --network hermes_edge curlimages/curl -s -m 5 http://hermes:8642/health
{"status": "ok", "platform": "hermes-agent", "version": "0.22.2"}      # HTTP 200
docker network inspect hermes_edge   ->  hermes 172.26.0.2/16
```

`hermes:8642` resolves and answers from inside `hermes_edge`; the port is `expose`d only, not
published. Caveat: this used a throwaway curl container, not an `antares` container (none exists
yet). The Antares container must join `hermes_edge`; edge route decision is still the owner's.

## 4/5/7. Two bindings, per-profile keys, A↔B negative tests — NOT PROVEN

- `hermes profile create testA|testB` succeeded (names are lower-cased to `testa`/`testb`; each gets
  `/opt/data/profiles/<name>/` with its own `.env`, `config.yaml`, `SOUL.md`, skills, cron).
- `GET /p/testa/v1/models` with the owner key → **404 `Unknown or unconfigured profile`**, both before
  and after the profiles existed (`fixtures/profile-prefix-unserved.*`). Cause, from source
  (`_resolve_request_profile`): when `multiplex_profiles` is falsy, any prefix other than the launch
  profile is rejected. `config.yaml` has no such key and `GATEWAY_MULTIPLEX_PROFILES` is unset
  (checked).
- Source also shows that a named profile authenticates with its **own** `API_SERVER_KEY` and fails
  closed (401) if it has none, and never inherits the owner's. That is encouraging but **unverified
  on the wire**, because no profile can be reached. A cross-key failing call was therefore not made.
- Running a second gateway per profile (`hermes -p X gateway run` on another port) would be an
  alternative, but it starts new services in the production container; not authorised, not done.

## 6. Profile-vs-tool filesystem isolation — NOT ISOLATED

Evidence (read-only, byte counts / `test -r` only; no content read):

- One gateway process (`hermes`, uid 10000) serves the API and executes tools
  (`runtime.tool_execution: "server"`; terminal backend is the local default — no `terminal:` config).
- All profiles live in one filesystem tree, same owner: `/opt/data` (`drwx------ 10000`),
  `/opt/data/profiles/testa|testb` (`10000:10000`), default `state.db`, `memories/`, `.env` (28 KB of
  provider secrets) all `10000`.
- As uid 10000: `/opt/data/profiles/testb/.env` and `SOUL.md` readable; default `state.db`,
  `memories/` and `.env` all `test -r` true; `/opt/data/profiles/testa/` listable.
- The `terminal`, `file` and `code_execution` toolsets are enabled. Any of them under profile A can
  therefore read profile B's sessions/memory **and the default profile's credentials**, by absolute
  path. Profiles are config/state namespaces, not sandboxes (PRD §5.5 confirmed).

I did not drive an LLM tool call to show it end to end (it needs a routable profile, blocker 1, and
the one canary-write I attempted was blocked as outside authorisation). The read-permission evidence
plus the enabled toolsets is sufficient to state the boundary does not exist at the OS level.

To make multi-user possible the owner would need, at minimum: restrict users' toolsets to ones that
cannot touch the filesystem (disable terminal/file/code_execution/skills write), **or** run a separate
Hermes container/uid/volume per tenant. Neither was tested.

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

`testa`/`testb` deleted (`hermes profile list` → `default` only), aliases removed. `testa` initially
failed to delete because my own root-run probe left root-owned cache dirs inside it; I removed
`/opt/data/profiles/testa` by exact path. `hermes profile delete` leaves tiny tombstone files (`testa`, `testb`, 8 bytes each) in
`/opt/data/profiles/.deleted/`, which I left. Container uptime unchanged (no restart).

## Decision requests for the owner

1. Do you want to enable `multiplex_profiles` (config + restart) so routing can be proven, understanding it does not fix blocker 2?
2. Tenancy model for Phase 1: restricted toolsets in one container, or one container per tenant?
3. Re-pull the image by registry digest so it can be pinned reproducibly.
