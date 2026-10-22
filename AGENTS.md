# AGENTS.md — Antares

Instructions for coding agents working in this repository. Read this file first, then the PRD.

## What this repository is

**Antares** is a multi-user web chat for an already-deployed **Hermes Agent**. It is a front end:
every answer, tool call and piece of generative UI markup comes from Hermes. Antares never
orchestrates LLMs, never calls a model provider directly, and never forks Hermes.

The complete specification is [`docs/PRD.md`](docs/PRD.md) (v2.4). It is the baseline for
development. **After the baseline is delivered, features follow the owner's requests** — the PRD is
not a ceiling, but it is the floor, and the security constraints in it are not negotiable at any
point.

## Read before you write

1. `docs/PRD.md` — the section that governs your ticket. Every ticket names its sections.
2. `docs/runbooks/sprint-delivery.md` — the delivery sequence.
3. `docs/runbooks/security-invariants.md` — the rules that must never be broken.
4. `docs/runbooks/deployment.md` — how this is deployed, and what you may not touch.

## The one gate that blocks everything

**Phase 0 must pass before any multi-user work is merged.** Two distinct Hermes profile bindings in
the existing container, user A unable to read user B's sessions or memory through any allowed API or
tool, and the admin auth/status matrix passing.

Until that is demonstrated, the product may be used by **one trusted user**. It must never be
described, documented or released as secure for multiple mutually untrusted users.

If Phase 0 fails, **stop and report** — do not build around it, and do not weaken the test that
proves it.

## Non-negotiable rules

1. **Default deny.** No approved account with a healthy tenant binding means no Hermes request. Not
   a degraded request, not a cached one — none.
2. **The backend selects the profile.** A user never supplies a profile name, route prefix, API key,
   header or session id. If a request carries one, reject it.
3. **Ownership on every query.** Every database query touching user resources enforces ownership.
   Negative IDOR tests with two test users are part of the definition of done, not an extra.
4. **No agent clone.** Do not write a second LLM framework, a prompt pipeline, or a tool executor.
   Hermes does the work.
5. **Capability-driven UI.** Nothing unconfigured or unsupported is displayed as available. An
   `unsupported` state is a correct answer; a fabricated one is not.
6. **Failure is visible.** A failed, cancelled or disconnected run is never presented as completed.
7. **Small footprint.** Two containers, SQLite, no Redis/Kafka/Postgres/extra orchestration in v1.
8. **Admin is blind by default.** An admin can approve, revoke and suspend. An admin cannot read a
   user's conversations, memories or files.

## The reference implementation is a reference, never a copy source

[`axellageraldinc/aixell`](https://github.com/axellageraldinc/aixell) is the UX-parity benchmark.
It is a **single-user** Node app with password + TOTP. Antares is multi-user, Google OIDC with admin
approval, Go + React + SQLite, backend-selected profiles.

Its filesystem shortcuts and its single-user assumptions are **out of bounds**. Do not import them,
do not copy its data model, and do not treat "aixell does it this way" as a reason. PRD §21.9 and
§18.3 record why.

## Working rules

- **One ticket, one branch, one worktree, one PR.** Branch from `origin/main`, PR to protected
  `main`, merge commit.
- **One commit per review round.** Batch the fixes; do not push a cosmetic commit and a fix commit
  separately.
- **Never rebase, amend or force-push a branch under review.** If the PR is behind, `git fetch origin
  && git merge origin/main` and resolve keeping both sides.
- **Read the real Hermes API, do not guess it.** Phase 0 records fixtures from the deployed version
  with keys redacted. An endpoint whose shape you assumed is a bug waiting for production.
- **Tests are the deliverable, not the afterthought.** A change without a test that fails before it
  and passes after is not done.
- **Never narrow a guard to make it pass.** If a guard excludes the case you changed, list the case
  explicitly.
- **Do not disable or skip a test** to make a suite green. If a test is wrong, say so with evidence
  and fix the test in its own commit.
- **Never claim security readiness you have not demonstrated.** "Should be isolated" is not
  isolation.

## Secrets

- No credential in a file you commit, in a log, in an error string, or in a read model.
- Hermes API keys, Google client secrets, session signing keys and push keys come from runtime
  secrets, never from the repository.
- `.env.example` lists variable **names** with placeholder values. It never holds a real value.
- A test fixture is sanitized: real response shapes, fabricated values.

## What you must not do

- Do not add Docker socket access to Antares, and do not let it modify Hermes' data directories.
- Do not deploy, restart a service, or touch production without the owner's explicit approval.
- Do not post, message or publish anything outside this machine.
- Do not rewrite git history, delete branches or tags, or force-push.
- Do not merge your own PR. The lead does that.
- Inside the running Hermes container the only authorised mutation is `hermes profile create` / `delete` for disposable test profiles, and only when the ticket's owner authorisation says so. A root-run command (`docker exec` without `-u hermes` that writes), or deleting a profile directory by hand, needs the owner's explicit approval first. If `hermes profile delete` fails, stop and report. (Phase 0 broke this once; see `docs/hermes/PHASE0-FINDINGS.md`.)

## Reporting

Report what you changed, what you verified and **how you verified it**, and what is still open. A
claim without evidence is not a report. If you are blocked, say what you are blocked on rather than
producing something that looks finished.
