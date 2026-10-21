# Runbook — deployment

How Antares runs, and what a coding agent may and may not touch.

## Topology

```
browser  ──►  Caddy (edge)  ──►  antares (Go, API + static + SQLite)
                                      │
                                      └──►  hermes (its own container, its own persistence)
```

The browser talks only to Antares. Hermes is reachable only over the private container network. No
Hermes credential ever reaches a browser.

## Containers

- **`antares`** — one Go binary serving the API, the built React bundle and an embedded SQLite
  database. No Docker socket. No access to Hermes' data directory.
- **`hermes`** — already deployed and owned by the operator. Antares does not manage it, restart it,
  or write to its storage.

## Configuration

All configuration comes from the environment. `docs/` and `.env.example` list variable **names**;
values live in runtime secrets and never in the repository.

At minimum: Google OIDC client id/secret and redirect URL, session signing key, Hermes base URL and
API key, the admin bootstrap mechanism, and the public base URL.

## What a coding agent may do

- Build the image, run it locally, run the test suite.
- Write and read deployment documentation.
- Open a PR that changes the deployment contract, for review.

## What a coding agent may not do — ask the owner first

- Deploy to any environment, restart any service, or change a Caddy route.
- Touch the Hermes container, its config, its profiles or its data directory.
- Rotate a secret, or write one anywhere it could be committed.
- Run anything against production data.

## Health and rollback

The service exposes a health endpoint that reports readiness — including whether it can reach
Hermes — without leaking configuration. A deploy is verified by that endpoint plus one real
authenticated request, not by the container being "up".

Rollback is redeploying the previous image tag. Keep the previous tag until the new one is verified.

## Backups

The SQLite database is the only state Antares owns. Back it up before a migration, and verify a
restore at least once per phase — an untested backup is a hope, not a backup.
