# Runbook — admin approval, provisioning and tenant verification

Issue #22 (PRD §3, §5.2, §5.3). How an approved account becomes a usable tenant, what is checked
before `READY`, and what an operator must do when it does not get there.

## Flow

```text
admin approve ─▶ APPROVED + UNPROVISIONED ─▶ (claim) CONFIGURING
   helper POST /provision {slug}  ─▶ key returned once ─▶ sealed + stored (tenant_bindings, CONFIGURING)
   verify against Hermes          ─▶ all checks pass    ─▶ READY   (user reaches the app)
                                  ─▶ any check fails    ─▶ FAILED + reason (admin sees it)
```

- The slug is `u_` + 24 random hex characters chosen by the server and stored on the user **before** the
  helper is called, so a retry asks for the same profile. It is never derived from the email or name.
- The key is sealed with AES-256-GCM (`internal/secrets`), bound to its binding id, and keyed-hashed
  into a `UNIQUE` fingerprint so one key cannot be bound to two users. Neither the key nor the profile
  name is in any response, log line, audit row or error string; admins see status and a fixed-vocabulary
  reason only.
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
| `POST …/reactivate` | `SUSPENDED` → `APPROVED`; the stored binding is re-verified, not trusted |
| `POST …/tenant-binding` | re-run provisioning for an approved account (e.g. after `FAILED`); takes no body |
| `POST …/tenant-verify` | re-verify an existing binding; the user is not `READY` while it runs; takes no body |
| `GET /api/admin/audit` | audit feed (actor, action, target, outcome, time; no reasons, no content) |

Bodies accept only `{"reason": "..."}` (≤ 500 chars, no control characters). Any other field — a
profile, slug, key, session id — is a `400`, as is a query parameter or header of that kind. An admin
cannot act on their own account. Two admins acting at once give one winner and a `409 STATE_CONFLICT`.

## Retry policy

Helper failures are classified. *Transient* (unreachable, 5xx, unreadable response) and verification
failures retry with backoff 15 s, 30 s, 1 m, … capped at 10 m, for 5 attempts, then stay `FAILED` with
the reason. *Permanent* (the helper refuses our token, rejects the request, answers for another
profile) stop at once. An admin `tenant-binding` resets the budget. A run interrupted by a restart is
resumed when its 6-minute lease expires. If the helper's response was lost (it has a key we never
stored), `/provision` returns `409` and the pipeline recovers with `/rotate`, as the helper README says.

## What `READY` requires (`internal/hermes/verify.go`)

All six, against `/p/<profile>/…` with the tenant's own key. Any failure, or any answer in a shape the
check does not recognise, is a failure.

| Check | Passes when |
|---|---|
| `endpoint` | `/v1/capabilities` (object `hermes.api_server.capabilities`) and `/v1/models` (`object: list`) answer 200 |
| `profile_identity` | the **default** profile's route and a never-provisioned route refuse the tenant key. Hermes' verified API has no profile-name field, so identity is shown by what accepts the key |
| `credential_isolation` | no key, a wrong key and every other tenant's route (up to 3) get 401/403 |
| `session_ownership` | `/api/sessions` answers 200 for the profile, is empty on first verification, and other tenants' session routes refuse the key |
| `toolset_policy` | no toolset in `hermes.DeniedToolsets` is enabled, nothing outside the allow-list is enabled, and every denied toolset is reported |
| `memory_boundary` | capabilities report `memory_write_api: false`, `admin_config_rw: false`, `auth.required: true` |

`ANTARES_TENANT_ALLOWED_TOOLSETS` may narrow the allow-list. It cannot include
`terminal, file, code_execution, delegation, cronjob, session_search, connections, browser`: the process
refuses to start.

## What this does **not** establish

- **Phase 0 is still FAIL.** Profiles are not sandboxes (PRD §5.5). `memory_boundary` is a policy gate
  on what the API offers, not proof of filesystem isolation, and nothing here makes Antares safe for
  mutually untrusted users. One trusted user remains the supported use until Phase 0 passes.
- Per-profile routing was **not reachable** on the deployed container in Phase 0 (`/p/<profile>/…` →
  404), and `disabled_toolsets` is not applied by the helper. On that deployment every verification
  fails at `endpoint` (and then `toolset_policy`): users end `FAILED`, never `READY`. That is the
  intended fail-closed result, not a bug to work around. It also means the admin surface cannot open,
  because the bootstrap admin's own tenant cannot verify.
- The `/api/sessions` response shape is documented but **unverified** on the deployed build
  (`PHASE0-FINDINGS.md` §2). The check accepts a bare array or `{"sessions": [...]}` and fails on
  anything else. Capture a sanitized fixture once a profile is routable and replace the guess.
- The checks have been exercised only against a fake Hermes in tests, not against a live tenant.

## Operator actions

- `FAILED` with `provisioning helper …`: check the helper (`GET /health`), its token and its network
  path from the Antares container, then `tenant-binding`.
- `FAILED` with `verification failed: toolset_policy …`: apply the tenant policy in the profile's own
  `config.yaml` (`docs/runbooks/tenant-toolsets.md`), then `tenant-verify`.
- `FAILED` with `verification failed: endpoint …`: the profile route is not served (see Phase 0 and
  `docs/hermes/MULTIPLEX-LISTENER-DIAGNOSIS.md`).
- `stored credential cannot be opened`: `ANTARES_SECRET_KEY` changed. Restore the previous key; do not
  re-provision around it.
- Rotating a key Hermes has stopped accepting has **no supported path yet**: the helper's `/rotate`
  returns the new key only to its caller, and Antares has no admin route that asks for one and stores it
  (open item). Do not paste a key into Antares by hand.
