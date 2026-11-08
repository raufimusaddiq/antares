<!--
Sync Impact Report
Version change: (template, unversioned) -> 1.0.0
Modified principles: none (first adoption; all placeholders filled)
Added sections: Core Principles I-V, Additional Constraints, Development Workflow, Governance
Removed sections: none
Follow-up TODOs: none. Derived from AGENTS.md and docs/runbooks/security-invariants.md.
-->
# Antares Constitution

## Core Principles

### I. Default Deny and Backend-Selected Identity (NON-NEGOTIABLE)
No approved account with a healthy tenant binding means no Hermes request: not degraded, not
cached. The backend alone selects the Hermes profile; a request that carries a profile name, route
prefix, API key, header or session id is rejected, never ignored or overridden. Every database
query touching user resources enforces ownership in the query itself. An admin can approve, revoke
and suspend but MUST NOT read a user's conversations, memories or files.
Rationale: a user-supplied profile name is a cross-tenant read; these rules make multi-user safe.

### II. Antares Is a Front End, Not an Agent
Every answer, tool call and piece of generative UI comes from Hermes. Antares MUST NOT add a second
LLM framework, prompt pipeline or tool executor, MUST NOT call a model provider on the user's
behalf for chat, and MUST NOT fork Hermes. Narrow helper calls (titles, summaries) go through the
configured gateway and are never presented as agent work.
Rationale: duplicating Hermes creates two sources of truth and an unreviewed trust boundary.

### III. Honest, Capability-Driven UI
Nothing unconfigured or unsupported is shown as available; "unsupported" is a correct answer and a
fabricated one is not. A failed, cancelled or disconnected run MUST NOT be presented as completed.
Failure is visible to the user.
Rationale: users act on what the screen claims.

### IV. Tests Guard Behaviour (NON-NEGOTIABLE)
A change needs a test that fails before it and passes after. Tests assert what a user, attacker or
caller can observe, one behaviour per test, with shared setup. Security invariants (default deny,
two-user ownership, backend-selected profile, no credential in responses or logs, admin blind, CSP)
and regression tests for owner-reported bugs are never removed. A guard MUST NOT be narrowed, and
a test MUST NOT be skipped or disabled, to get green.
Rationale: untested isolation claims are not isolation.

### V. Small Footprint
Two containers and SQLite. No Redis, Kafka, Postgres or extra orchestration without a PRD change.
Complexity MUST be justified in the plan.
Rationale: the system is operated by one owner; every moving part is a recurring cost.

## Additional Constraints

- **Secrets:** no credential in a committed file, log, error string, URL or read model. Keys come
  from runtime secrets; `.env.example` holds names and placeholders only; fixtures are sanitized.
- **No security overclaim:** Antares MUST NOT be described as secure for mutually untrusted users
  until the Phase 0 isolation gate is demonstrated.
- **Hermes API fidelity:** shapes come from recorded fixtures of the deployed version, never guesses.
- **Production boundaries:** no Docker socket access for Antares; Antares does not modify Hermes
  data directories; inside the Hermes container only profile create/delete is authorised.
- **Stack:** Go server, React/Vite web app; the committed bundle in `internal/webui/dist` MUST be
  rebuilt with the web change (enforced by `TestBundleIsNotStale`).

## Development Workflow

- One ticket, one branch, one worktree, one PR from `origin/main`, merged with a merge commit.
- One commit per review round; never rebase, amend or force-push a branch under review.
- Required checks (including Hermes Review) pass before merge.
- Deploy: back up, release, pull and restart, verify `/api/version`, run the smoke test, clean up
  smoke accounts, never touching real users' accounts.
- Reports state what changed, what was verified and how, and what is open.

## Governance

This constitution supersedes other practices where they conflict. `AGENTS.md` and
`docs/runbooks/security-invariants.md` are its detailed companions; amending a rule there that
this document states requires amending it here in the same PR. Amendments are made by pull request
with a stated rationale and a version bump: MAJOR for removing or redefining a principle, MINOR for
adding one or materially expanding guidance, PATCH for wording. Every plan and PR review MUST
check compliance with Principles I-V; deviations are recorded in the plan with justification. Use
`AGENTS.md` for runtime development guidance.

**Version**: 1.0.0 | **Ratified**: 2026-11-08 | **Last Amended**: 2026-11-08
