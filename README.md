# Antares

A private, self-hosted web chat for a **Hermes Agent** — multiple users, one assistant, strict
per-user isolation.

The name is the star: Antares is the brightest star in Scorpius, the constellation Hermes' own name
comes from. It is a chat front end, not an agent — every answer, every tool call and every piece of
generative UI markup comes from Hermes.

## What it does

Visitors sign in with Google. Signing in creates a **pending account** — not permission to chat. An
administrator approves the user and binds a private Hermes profile; only then can the user open,
resume or create conversations. Each user sees only their own transcripts, memories, files, runs and
tool outcomes. An admin can revoke access without gaining access to anyone's private chat.

One Go service (API + static React bundle + embedded SQLite) in one container, next to Hermes in its
own. The browser talks only to Antares. It does not orchestrate LLMs, does not call providers
directly, and does not fork Hermes.

## Reference implementation

[**aixell**](https://github.com/axellageraldinc/aixell) is the product this one is measured against —
a private PWA chat for a single Hermes profile, with login + TOTP, push notifications and a daisyUI
interface. It is the interaction-quality benchmark and the source of the UX parity requirements in
the PRD (§21, §26).

Antares differs from it in exactly the ways the PRD makes non-negotiable:

- **Multi-user, not single-user.** aixell is one person's chat. Antares must prove user A cannot
  read user B's sessions or memory before it may serve more than one account.
- **Google OIDC plus admin approval, not password + TOTP.** Identity comes from Google; authorization
  is this app's decision; a new account is inert until approved.
- **Backend-selected profiles.** The user never supplies a profile name, route, key or session id.
- **Go + React + SQLite, not Node + daisyUI.** The PRD fixes the stack; the reference does not.

aixell is a **reference, never a copy source.** Its filesystem shortcuts are explicitly out of
bounds — the PRD says so in §21.9 and §18.3, and the reasons are recorded there.

## Status

**In production** for one trusted user, with signed-in accounts held pending until an admin approves
them. Shipped: streaming chat with cancel, retry and rewind; per-user Hermes profiles created by a
host-side provisioner; images, voice input, search, drafts and templates; generative UI (OpenUI)
cards; a usage view; web push; an installable PWA; and nightly backups.

The PRD ([`docs/PRD.md`](docs/PRD.md), v2.4) is the baseline. Work is tracked as issues on the
project board, and later features follow the owner's requests.

## Repository layout

| Path | What it holds |
|---|---|
| `cmd/antares` | the service: API, embedded web bundle, SQLite, `backup` / `restore` / `check` subcommands |
| `cmd/antares-smoke` | the production smoke test |
| `internal/` | auth, chat, store, tenant binding, Hermes client, push, quota, observability |
| `web/` | the React + Vite front end; its build is committed to `internal/webui/dist` |
| `tools/hermes-provisioner` | the host helper that creates and deletes Hermes profiles |
| `deploy/` | compose file, systemd units and backup script |
| `docs/runbooks/` | how each part works and how to operate it |

## Development

```sh
make test                                   # go build, vet and race tests
make threat                                 # cross-user threat suite
npm --prefix web ci && npm --prefix web test
npm --prefix web run build                  # rebuild internal/webui/dist after any web change
```

CI requires these checks: go, web, security, mobile layout, sandbox probe and Hermes Review.
Before you change anything, read [`AGENTS.md`](AGENTS.md).

## Deploying

Every push to `main` publishes `ghcr.io/raufimusaddiq/antares:<commit sha>`. To deploy, back up the
database, pull that tag with `deploy/compose.yml`, then run the smoke test against production. The
steps are in [`docs/runbooks/deployment.md`](docs/runbooks/deployment.md),
[`docs/runbooks/backup-restore.md`](docs/runbooks/backup-restore.md) and
[`docs/runbooks/smoke-test.md`](docs/runbooks/smoke-test.md).

## The hard gate

Phase 0 is a proof, not a build: two distinct Hermes profile bindings in the existing container, user
A unable to read user B's sessions or memory through any allowed API or tool, and the admin
auth/status matrix passing. Until that is demonstrated, Antares can be used by a single trusted user
but **must not be represented as secure for multiple mutually untrusted users**.

Phase 0 results and the Hermes API fixtures: [`docs/hermes/PHASE0-FINDINGS.md`](docs/hermes/PHASE0-FINDINGS.md).

## Non-negotiables

1. Default deny — no approved, healthy tenant binding means no Hermes request.
2. Google authenticates identity; Antares authorizes; Hermes runs the models.
3. The backend selects the profile. Users never supply profile names, routes, keys or session ids.
4. Private by construction — every query touching user data enforces ownership.
5. No agent clone — all reasoning and generative UI markup come from Hermes.
6. Capability-driven UI — nothing unconfigured is shown as available.
7. Small footprint — two containers, SQLite, no extra datastore or orchestration in v1.
8. Failure is visible — a failed or cancelled run is never presented as completed.

## License

MIT — see [LICENSE](LICENSE).
