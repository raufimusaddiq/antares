# Runbook — the deploy gate (smoke test)

Issue #53. **A deploy is "done" when this passes against it, and not before.** `/healthz`, `/readyz`,
`docker compose ps`, `systemctl is-active`, a 302 from the OAuth start and `READY` rows in the database each
answer for a process or a table. None of them sends a chat. On 2026-10-23 Phase 1 was called ready on exactly
those, and the owner could not chat, twice. This test does what the owner did.

```
go run ./cmd/antares-smoke --url https://antares.example.com --json smoke-result.json
# or: go build -o antares-smoke ./cmd/antares-smoke && ./antares-smoke …
```

Run it from any machine that can reach the deployment over HTTPS. It needs no access to the host, Docker, the
database or Hermes: it is a client of Antares' public API and never calls Hermes.

| Exit | Verdict | Meaning |
|---|---|---|
| `0` | `PASS` | every step ran and passed. **The only exit code that means "deployed".** |
| `1` | `FAIL` | a step failed; `firstFailure` names it |
| `2` | `INCOMPLETE` | nothing failed, but a step did not run (a missing session, or a prerequisite failed). **Not a pass.** |
| `3` | | usage or configuration error; nothing was tested |

## Inputs

Sessions come from the environment (never flags: those land in shell history and `ps`). Each is the **value** of the
`__Host-antares_session` cookie after a real Google sign-in in a browser (DevTools → Application → Cookies); a pasted
`__Host-antares_session=<value>` is accepted too. A session is a bearer credential for that account: treat it like a
password, never commit or paste it, and expect it to expire after 7 days.

| Variable | Account | State it must be in |
|---|---|---|
| `ANTARES_SMOKE_COOKIE_ADMIN` | the bootstrap admin (or any ADMIN) | approved, workspace `READY` |
| `ANTARES_SMOKE_COOKIE_A` | dedicated test account A | signed in once; `PENDING`, `APPROVED` or `SUSPENDED` |
| `ANTARES_SMOKE_COOKIE_B` | dedicated test account B | same. **Its session is revoked by the run**: sign B in again before the next one |
| `ANTARES_SMOKE_COOKIE_PENDING` | dedicated account that must **never** be approved | `PENDING` |

`ANTARES_SMOKE_URL` / `--url` is the public origin. `--origin` (or `ANTARES_SMOKE_ORIGIN`) must equal the deployment's
`ANTARES_PUBLIC_ORIGIN` if you reach Antares by a different address (for example `http://127.0.0.1:8080` from the
host); a mismatch makes every mutation answer `403 Cross-origin request refused`. A non-loopback `http://` target is
refused unless `--allow-http`, because the cookies would cross the network in clear.

Use four Google accounts that exist only for this. A, B and the pending account become real tenants/users on the
deployment (each approved account gets a Hermes profile); say so to whoever owns the Hermes host.

Other flags: `--run-timeout` (default 2m: message sent → terminal phase), `--tenant-timeout` (5m: approval → `READY`,
and a re-verification), `--timeout` (20m, the whole run), `--prompt`, `--json <file|->` (`-` writes JSON to stdout and
the human log to stderr).

## What it does, in order

Each line is a step id in the result. A step whose prerequisite did not pass is recorded `skip` with `blockedBy`,
never `pass`. Every string in the output goes through a redactor (the supplied sessions, bearer tokens, JWTs,
`__Host-antares_*` cookies, `key=`/`secret=`/`token=`/`password=` values), and response bodies are never printed, only
`HTTP <status> <error code>`.

1. `reach.health` — `/healthz` 200 and `/readyz` `ready`. Reachability only; nothing below is implied by it.
2. `login.admin|a|b|pending` — each supplied session is accepted by `/api/me`. No session supplied → `skip`.
3. `admin.active` — the admin can use the admin API (their own workspace is `READY`).
4. `negative.unapproved` — the pending account is `PENDING` and is refused `403 ACCOUNT_PENDING` on listing chats,
   creating a chat, sending a message and the admin API.
5. `account.approve` — A and B are approved through the admin API (a `SUSPENDED` account from a previous run is
   reactivated; a `REJECTED` one fails the step).
6. `tenant.ready.a|b` — the tenant reaches `READY` with a `READY` binding. A tenant that stays `FAILED` fails the step
   with Antares' own `tenantDetail`, for example `verification failed: toolset_policy (boundary-crossing toolset
   terminal is enabled)`.
7. `tenant.verify.a|b` — asks Antares to rotate, store the new key and verify (`tenant-verify`). The account first goes back
   to `CONFIGURING` (the step fails if it does not, since a stale `READY` would prove nothing); `READY` afterwards means
   Antares re-ran its six checks with **the tenant's own credential** against the Hermes it is deployed with:
   `endpoint`, `profile_identity`, `credential_isolation`, `session_ownership`, `toolset_policy`, `memory_boundary`.
   `toolset_policy` reads the profile's effective toolsets and fails if a boundary-crossing toolset is enabled or
   anything outside the allow-list is, which is how `agent.disabled_toolsets` not being applied shows up.
8. `chat.a.reply`, `chat.b.reply` — `POST /api/chats`, `POST …/messages`, follow `…/events` to a terminal phase within
   `--run-timeout`, confirm the run row says `COMPLETED` (the stream is a view; the row is the authority), then read the
   transcript and require a **non-empty assistant reply after the prompt**. `FAILED`, `CANCELLED`, `TIMED_OUT`, no
   terminal phase in time, and a completed run with an empty reply are all failures.
9. `negative.cross_user` — in both directions, the other user's chat, messages, send, run, run events, cancel and chat
   list are all `404 NOT_FOUND`, the owner's reply text appears in none of the answers, and each owner can still read
   their own chat (so a 404 cannot be an outage).
10. `negative.revoked` — an admin suspends B. With no waiting, B's list, create, own transcript, send, own run and own
    event stream are all refused (`401`, since suspension revokes the sessions in the same transaction, or `403
    ACCOUNT_SUSPENDED`); A is unaffected. Any `2xx` fails the step.
11. `cleanup.restore` — B is reactivated and waits for `READY` again. B's session stays revoked.

## How each of the three live failures is caught

| Live failure (2026-10-23) | Step that fails | What it says |
|---|---|---|
| Stale host helper: `agent.disabled_toolsets` never written, so boundary toolsets stay on | `tenant.ready.a` for a profile provisioned after the helper went stale; `tenant.verify.a` for an existing one (re-verification). If even the admin's workspace was provisioned stale: `admin.active` | `verification failed: toolset_policy (boundary-crossing toolset terminal is enabled)` |
| `/opt/antares` `700`, service crash-looping `203/EXEC` | `reach.health` (nothing answers) | connection error or non-200; every later step `skip`, `blockedBy: reach.health` |
| Tenant setup remains `FAILED`; retryable failures back off, while deterministic stops or exhausted budgets require admin requeue | `tenant.ready.a|b`, or `admin.active` for the admin | `tenant A is FAILED after N attempt(s): <Antares' detail>` |

`internal/smoke/scenario_test.go` reproduces the first and third against a real in-process Antares (fake Hermes and
provisioner behind real HTTP) and asserts the failing step and its text; the second is asserted by `cmd/antares-smoke/main_test.go`
(a target that does not answer healthy fails `reach.health` and everything after it is skipped; a refused connection
fails the same step). The negative checks are proven the other way as well: tests plant the defect
(an ownership check that stopped working, a revocation that a stale cache ignores, a pending account that can chat)
and assert the matching step fails.

## Reading the result

`--json` writes `{target, runId, startedAt, finishedAt, verdict, firstFailure, pass, fail, skip, steps:[{id, title,
status, detail, blockedBy?, durationMs}]}`. Keep it with the release record. `status` is `pass`, `fail` or `skip` and
nothing else.

## Making it the gate

`docs/runbooks/deployment.md` ("Health and rollback") and `deploy/README.md` (step 4a) both state that a deploy is
verified by exit `0` from this command and by nothing less. If it is not `0`: do not announce the deploy, and roll back
to the previous tag (`deploy/README.md`, "Roll back") unless the failing step is understood and the owner accepts it.
`INCOMPLETE` is not a lesser pass; supply the missing session and run again.

It is not wired into GitHub Actions, on purpose: a hosted runner has no sessions (a Google sign-in cannot be completed
headlessly) and no reason to hold four test accounts' bearer credentials. Run it from the operator's machine, or from a
runner that holds the four sessions as secrets and is allowed to reach the deployment; the exit code and JSON are built
for that.

## Test-identity login (not implemented, and why)

A headless run would need Antares to accept a login that is not Google's. Antares' identity provider is deliberately not
configurable from the environment (`internal/auth/oidc.go`), and this change does not add that switch: an instance
configured to trust a test provider signs in whoever holds that provider's signing key, as any user including an admin.
If the owner wants unattended runs, the shape would be a dedicated staging instance (own database, own hostname, no real
users) started with an explicit, loudly logged switch, with the key kept at `0600` on the staging host. That is a
security-relevant product change and needs the owner's decision; until then the primary path is real sessions. The
in-process tests do complete a full OIDC Authorization Code + PKCE flow against a fake provider injected through
`auth.Options.Endpoints`, so the login handler itself is exercised, just not Google.

## What this does not cover

- **Google's side of login.** The sessions are supplied; the tool cannot drive a Google sign-in. Session issuance
  against real Google is exercised only when you create the sessions by hand.
- **Phase 0 isolation.** It checks the API-visible boundary (Antares' ownership, the tenant's own route and credential,
  the toolset policy). It does not prove that one Hermes profile cannot read another's files or memory through a
  toolset; that Phase 0 result stands (`docs/hermes/PHASE0-FINDINGS.md`, `docs/runbooks/memory-scoping.md`).
- **`agent.disabled_toolsets` in the file.** It sees the profile's effective toolsets through Antares' verifier, not
  `config.yaml`. A toolset that is effectively off passes even if the file is empty for another reason.
- **A real model answering well.** It requires a non-empty reply, not a correct one.
- **Streams open at the moment of a suspension.** Only the next request is tested; the open-stream close is covered by
  `TestPhase1Gate/4` with a fake Hermes.
- **Browsers and the UI**, load, backups, Caddy buffering of SSE beyond "events arrived through the edge you pointed
  it at", and anything behind `501 CAPABILITY_UNAVAILABLE` (assets, jobs, models, skills, toolsets routes are not built).
- **Cleanup of Hermes profiles.** Approved test accounts keep their tenant profiles. Antares cannot delete them (admin is
  blind to profile names by design, and `AGENTS.md` limits who may delete a profile); remove them through the helper
  when the owner says to.
- **Production data.** It creates chats on the target. Point it at production only with the owner's say-so
  (`docs/runbooks/deployment.md`).
