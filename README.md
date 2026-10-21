# Hermes Chat UI

A lightweight, high-quality chat front end for an already-deployed **Hermes Agent**.

Visitors sign in with Google. Signing in creates a **pending account** — not permission to chat. An
administrator approves the user and binds a private Hermes profile; only then can the user open,
resume or create conversations. Each user sees only their own transcripts, memories, files, runs and
tool outcomes.

The app is a single Go service (API + static React bundle + embedded SQLite) in one container, next
to Hermes in its own container. The browser talks only to this app. It does not orchestrate LLMs,
does not call providers directly, and does not fork Hermes.

## Status

**Pre-implementation.** This repository holds the baseline specification only.

- [`docs/PRD.md`](docs/PRD.md) — the complete Product Requirements Document (v2.4). It is the source
  of truth for scope, security constraints and acceptance criteria.
- Work is tracked as issues; the delivery order follows the PRD's §16 phases.

## The hard gate

Phase 0 is a proof, not a build: two distinct Hermes profile bindings in the existing container, user
A unable to read user B's sessions or memory through any allowed API or tool, and the admin
auth/status matrix passing. Until that is demonstrated, the product can be used by a single trusted
user but **must not be represented as secure for multiple mutually untrusted users**.

## Non-negotiables

1. Default deny — no approved, healthy tenant binding means no Hermes request.
2. Google authenticates identity; this app authorizes; Hermes runs the models.
3. The backend selects the profile. Users never supply profile names, routes, keys or session ids.
4. Private by construction — every query touching user data enforces ownership.
5. No agent clone — all reasoning and generative UI markup come from Hermes.
6. Capability-driven UI — nothing unconfigured is shown as available.
7. Small footprint — two containers, SQLite, no extra datastore or orchestration in v1.
8. Failure is visible — a failed or cancelled run is never presented as completed.

## License

MIT — see [LICENSE](LICENSE).
