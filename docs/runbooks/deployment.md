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
- Touch the Hermes container, its config, its profiles or its data directory. Inside the running Hermes container the only authorised mutation is `hermes profile create` / `delete` for disposable test profiles, and only when the ticket's owner authorisation says so. A root-run command (`docker exec` without `-u hermes` that writes), or deleting a profile directory by hand, needs the owner's explicit approval first. If `hermes profile delete` fails, stop and report.
- Rotate a secret, or write one anywhere it could be committed.
- Run anything against production data.

The concrete artifacts (Dockerfile, compose file, systemd unit, operator sequence) are in `deploy/`;
see `deploy/README.md`.

## Health and rollback

The service exposes a health endpoint that reports readiness — including whether it can reach
Hermes — without leaking configuration. A deploy is verified by that endpoint plus one real
authenticated request, not by the container being "up".

Rollback is redeploying the previous image tag. Keep the previous tag until the new one is verified.

## Backups

The SQLite database is the only state Antares owns. Back it up before a migration, and verify a
restore at least once per phase — an untested backup is a hope, not a backup.

## Provisioning helper

Per-user Hermes profiles are created by `tools/hermes-provisioner/`, a small Go service that runs
**on the host** (not in the `antares` container, which stays Docker-socket-free). It executes
`hermes profile create/delete/purge-identity` through `docker exec -u hermes` and exposes
`/provision`, `/rotate`, `/deprovision` and `/health` over HTTP (see its README).

- **Token:** `PROVISIONER_TOKEN` is read from the service's environment, supplied by the host's
  secret store or a root-owned `EnvironmentFile` outside the repository. The same value is given to
  Antares as a runtime secret. There is no default; the service will not start without it.
- **Network:** bind to an address reachable from the `antares` container only (default
  `127.0.0.1:8750`; change `PROVISIONER_ADDR` for the container bridge) and never expose it at the edge.
- **User:** a normal host user with Docker access to the Hermes container; nothing more.
- **Scope:** it refuses `default` and any slug not matching `^u_[a-z0-9]{8,32}$`.
- Returned keys are stored by Antares in its secret store; the helper keeps none.
- **Failure handling:** a `/deprovision` `502` naming a `/opt/data/profiles/<slug>` path means a
  non-empty residue directory the helper will not delete; investigate, then `rmdir` it manually
  (owner approval applies). A lost `/provision` response is recovered with `/rotate`. Details in the
  helper README.
