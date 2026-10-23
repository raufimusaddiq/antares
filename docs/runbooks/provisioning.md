# Runbook — admin approval, provisioning and tenant verification

Issue #22 (PRD §3, §5.2, §5.3). How an approved account becomes a usable tenant, what is checked
before `READY`, and what an operator must do when it does not get there.

## Flow

```text
admin approve ─▶ APPROVED + UNPROVISIONED ─▶ (claim) CONFIGURING
   no binding: POST /provision    ─▶ key returned once ─▶ sealed + stored (tenant_bindings, CONFIGURING)
   binding: POST /rotate          ─▶ policy reapplied, new key sealed over the existing credential
   verify against Hermes          ─▶ all checks pass    ─▶ READY   (user reaches the app)
                                  ─▶ any check fails    ─▶ FAILED + reason (admin sees it)
```

- The slug is `u_` + 24 random hex characters chosen by the server and stored on the user **before** the
  helper is called, so a retry asks for the same profile. It is never derived from the email or name.
- The key is sealed with AES-256-GCM (`internal/secrets`), bound to its binding id, and keyed-hashed
  into a `UNIQUE` fingerprint so one key cannot be bound to two users. Neither the key nor the profile
  name is in any response, log line, audit row or error string; admins see status and a fixed-vocabulary
  reason only.
- Every setup run with a binding calls `/rotate` before verification: both requeue operations,
  automatic retries, resumed leases and reactivation. The helper reapplies toolset policy and replaces
  the profile key; Antares seals the new key with the current `ANTARES_SECRET_KEY` and stores it against
  the same binding ID. The old profile key is invalidated. Every such run requires the helper.
  If `/rotate` reports a missing host profile (404) or missing key (409), `/provision` recreates the
  profile or issues its first key, then replaces the credential in the existing binding before full
  verification. This does not restore any host data lost with a deleted profile.
- Users who are approved but not `READY` see `SETTING_UP`; a tenant that ends `FAILED` shows the user
  the existing `ERROR` screen (slice 2) and the admin the reason. Every chat route stays closed
  (`TENANT_NOT_READY`) until `READY`.
- The first admin is bootstrapped `APPROVED + UNPROVISIONED`. The admin guard needs an *active, mapped*
  admin, so the background sweep provisions that account like any other. There is no admin API to
  provision the first admin, and the guard was not loosened for it.

## Admin API (all require an active, mapped admin; mutations require same-origin)

| Route | Effect |
|---|---|
| `GET /api/admin/users?status=PENDING` | list accounts (status only; no profile, key or content) |
| `GET /api/admin/users/{id}` | one account |
| `POST …/approve` | `PENDING`/`REJECTED` → `APPROVED`, starts provisioning |
| `POST …/reject` | `PENDING` → `REJECTED`; **reason required**; revokes the user's sessions |
| `POST …/suspend` | `APPROVED` → `SUSPENDED`; **reason required**; revokes sessions, disables the binding |
| `POST …/reactivate` | `SUSPENDED` → `APPROVED`; an existing binding is rotated, re-sealed and verified before access returns |
| `POST …/tenant-binding` | provision without a binding, otherwise rotate, re-seal and verify; resets attempts; takes no body |
| `POST …/tenant-verify` | rotate, re-seal and verify an existing binding; resets attempts; blocks access while running; takes no body |
| `GET /api/admin/audit` | audit feed (actor, action, target, outcome, time; no reasons, no content) |

Bodies accept only `{"reason": "..."}` (≤ 500 chars, no control characters). Any other field — a
profile, slug, key, session id — is a `400`, as is a query parameter or header of that kind. An admin
cannot act on their own account. Two admins acting at once give one winner and a `409 STATE_CONFLICT`.

## Retry policy

Transient helper failures (unreachable, 5xx, unreadable response) and unavailable verification
evidence retry with backoff 15 s, 30 s, 1 m, … capped at 10 m, for 5 attempts. An enabled forbidden
toolset after policy application is a deterministic divergence: it stops after one failed run, as do
permanent helper failures (token refused, request rejected, answer for another profile). Attempts
count actual failed runs. If a report also contains transient verification failures, the admin detail
names `toolset_policy` as the deterministic check and retains the other check results. Deterministic stops and exhausted retry budgets stay `FAILED` with a NULL
next-attempt time, an explicit `operator attention required` detail distinguishing the two outcomes,
and a `tenant.attention` audit event. Both admin requeue operations reset the budget.

A run interrupted by a restart resumes when its 12-minute lease expires. If a provision response
was lost before a binding was stored, `/provision` returns `409` and the pipeline recovers with
`/rotate`. With a binding, every retry attempts rotation again, falling back to `/provision` only
for a missing profile or key. A run makes at most two helper calls; a provision conflict during that
fallback waits for the next retry rather than adding a third call. Token refusals and other helper
failures do not trigger provisioning.

## What `READY` requires (`internal/hermes/verify.go`)

All six, against `/p/<profile>/…` with the tenant's own key. Any failure, or any answer in a shape the
check does not recognise, is a failure.

| Check | Passes when |
|---|---|
| `endpoint` | `/v1/capabilities` (object `hermes.api_server.capabilities`) and `/v1/models` (`object: list`) answer 200 |
| `profile_identity` | the **default** profile's route and a never-provisioned route answer the tenant key with a refusal: **401 or 403** for the default route, **401, 403 or 404** for the never-provisioned route (the deployed Hermes answers 404 for a route that does not exist). Any other status, and a 404 on the default route, is a broken or misrouted endpoint. Hermes' verified API has no profile-name field, so identity is shown by what refuses the key |
| `credential_isolation` | no key, a wrong key and every other tenant's route (up to 3) get exactly 401/403 |
| `session_ownership` | `/api/sessions` answers 200 for the profile, is empty on first verification, and other tenants' session routes refuse the key |
| `toolset_policy` | enforced at provision time (the provisioner writes `agent.disabled_toolsets` on `/provision` and `/rotate`) and checked here: no toolset in `hermes.DeniedToolsets` is enabled, nothing outside the allow-list is enabled, and every denied toolset is reported |
| `memory_boundary` | capabilities report `memory_write_api: false`, `admin_config_rw: false`, `auth.required: true` |

`ANTARES_TENANT_ALLOWED_TOOLSETS` may narrow the allow-list. It cannot include
`terminal, file, code_execution, delegation, cronjob, session_search, connections, browser`: the process
refuses to start.

## What this does **not** establish

- **Phase 0 is still FAIL.** Profiles are not sandboxes (PRD §5.5). `memory_boundary` is a policy gate
  on what the API offers, not proof of filesystem isolation, and nothing here makes Antares safe for
  mutually untrusted users. One trusted user remains the supported use until Phase 0 passes.
- Per-profile routing was **not reachable** on the deployed container in Phase 0 (`/p/<profile>/…` →
  404), and the helper did not apply `disabled_toolsets` (it now does, issue #41). On that deployment every verification
  fails at `endpoint` (and then `toolset_policy`): users end `FAILED`, never `READY`. That is the
  intended fail-closed result, not a bug to work around. It also means the admin surface cannot open,
  because the bootstrap admin's own tenant cannot verify.
- Refusal probes require **401 or 403**, except the never-provisioned route, which also accepts the
  **404** the deployed Hermes returns for it (issue #41). A 404 on the default route or a sibling route
  still fails: that would mean a misrouted endpoint. Reasons name the statuses accepted.
- `/api/sessions` returned `{"object":"list","data":[],"limit":1,"offset":0,"has_more":false}` for a
  fresh profile on the deployed build (issue #41; fixture `sessions-empty`). Only the empty list
  envelope is evidenced: the shape of a session item is still unverified. The check accepts the
  envelope (and, defensively, a bare array or `{"sessions": [...]}`) and fails on anything else.
- What has and has not been exercised live (Hermes v0.22.2, 2026-10-23, throwaway profiles, since
  deleted): the 404 on a never-provisioned route, the `/api/sessions` empty-list envelope, and the
  tenant toolset policy (`config set agent.disabled_toolsets`, then `GET /p/<profile>/v1/toolsets` with
  the profile's key: every denied toolset `enabled: false`) were each observed on the wire; the
  `config set` was run by hand with the provisioner's command, not through a running provisioner. Not
  exercised live: a complete provision-and-verify of a real tenant to `READY` through Antares and the
  running provisioner, the non-empty sessions item shape, and the cross-tenant checks against a second
  routable tenant. Those run only against a fake Hermes in tests.

## Operator actions

- `FAILED` with `provisioning helper …`: check the helper (`GET /health`), its token and its network
  path from the Antares container, then `tenant-binding`.
- `FAILED` with `verification failed: toolset_policy …`: do not edit the profile's `config.yaml`; the
  provisioner overwrites it on the next `/provision` or `/rotate`. Make `PROVISIONER_ALLOWED_TOOLSETS`
  (provisioner) equal `ANTARES_TENANT_ALLOWED_TOOLSETS` (Antares), restart the provisioner, then
  use `tenant-verify` to rotate, store the new key and verify (`docs/runbooks/tenant-toolsets.md`).
- `FAILED` with `verification failed: toolset_policy … toolset X is enabled but not allowed` for every
  tenant right after a **Hermes upgrade**: the new build probably added a toolset that is on by
  default. The provisioner only disables the toolsets it knows (`knownToolsets` in
  `tools/hermes-provisioner/toolsets.go`, mirrored by `hermes.KnownToolsets`), so the new one stays on.
  Add its name to both lists (the contract test against `docs/hermes/fixtures/toolsets.body.json`
  fails until the fixture is re-captured and both lists agree), redeploy both programs, then rotate
  each profile so the policy is reapplied. Until then no tenant verifies; that is fail-closed.
- `FAILED` with `verification failed: endpoint …`: the profile route is not served (see Phase 0 and
  `docs/hermes/MULTIPLEX-LISTENER-DIAGNOSIS.md`).
- A changed `ANTARES_SECRET_KEY`: if the change was accidental, stop Antares before any setup run
  rotates and overwrites credentials, and restore the previous secret through the operator's normal
  recovery procedure. Once a run stores a replacement, restoring the previous secret alone cannot
  decrypt that replacement or recover the invalidated profile key. Keep the intended current secret
  and requeue affected approved accounts to obtain and seal fresh profile keys. The binding ID,
  profile, memory scope and private data stay in place; the helper must be available.
- A profile key Hermes no longer accepts: `tenant-verify` or `tenant-binding` is the supported
  rotation-and-store path. It invalidates the old profile key, seals and stores the replacement, then
  verifies with that replacement. Do not paste a key into Antares by hand. If the bootstrap admin
  cannot access the admin API, due retries still run through the sweep; a stopped account requires
  operator recovery of admin access before an admin can requeue it.

## Named-profile key reload evidence

The restart in `docs/hermes/MULTIPLEX-LISTENER-DIAGNOSIS.md:20-21` concerns starting the default
listener with its missing key. Named tenant profiles take a different authentication path. In the
pinned Hermes source (`d923a5eb487a17c0a68801bba9ef0f56c81040a8`):

- [Profile middleware and scope](https://github.com/NousResearch/hermes-agent/blob/d923a5eb487a17c0a68801bba9ef0f56c81040a8/gateway/platforms/api_server.py#L1674-L1716)
  enter a named profile's runtime scope for each request;
  [that scope](https://github.com/NousResearch/hermes-agent/blob/d923a5eb487a17c0a68801bba9ef0f56c81040a8/gateway/run.py#L1772-L1818)
  rebuilds the profile's secret mapping.
- [Secret loading](https://github.com/NousResearch/hermes-agent/blob/d923a5eb487a17c0a68801bba9ef0f56c81040a8/agent/secret_scope.py#L350-L418)
  reads the profile `.env`; its cache checks the opened file's stat identity. The helper writes a
  temporary file and uses `os.replace` (`tools/hermes-provisioner/hermes.go`, `envScript`), so the
  replacement invalidates that cache. External or managed secret sources must not override the
  profile's `API_SERVER_KEY`.
- [Named-profile authentication](https://github.com/NousResearch/hermes-agent/blob/d923a5eb487a17c0a68801bba9ef0f56c81040a8/gateway/platforms/api_server.py#L1536-L1552)
  reads `API_SERVER_KEY` from the request's scope; the default profile instead uses the adapter key.

This source path supports using the rotated named-profile key on the next request without a gateway
reload, including when the tenant was previously `READY`. This is source evidence, not a live
rotation test against the deployed gateway. Antares still requires full verification with the new
key before returning to `READY`; a helper success alone cannot unlock access.
