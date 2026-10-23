# hermes-provisioner

Host-side helper that creates and removes per-user Hermes profiles for Antares (issue #18). It runs
**on the host**, never inside the Antares container, and reaches Hermes only through
`docker exec -u hermes <container> hermes ...` with argv arrays (no shell, no string interpolation).

## Run

```sh
export PROVISIONER_TOKEN=...      # required, >= 32 chars, no default; the service refuses to start without it
export PROVISIONER_ADDR=127.0.0.1:8750   # optional (default shown)
export HERMES_CONTAINER=hermes           # optional (default shown)
export PROVISIONER_ALLOWED_TOOLSETS=...  # optional; toolsets a tenant keeps (default web,vision,image_gen,skills,memory,todo,clarify)
go build -o hermes-provisioner . && ./hermes-provisioner
```

The running user needs Docker access to the Hermes container and nothing else.

## API

All endpoints except `/health` need `Authorization: Bearer $PROVISIONER_TOKEN`.

| Endpoint | Body | Result |
| --- | --- | --- |
| `POST /provision` | `{"slug":"u_xxxxxxxx"}` | creates the profile if absent, writes a fresh `API_SERVER_KEY` to its `.env`, returns `{profile,key,created_at}` once. `409` if the profile already has a key. |
| `POST /rotate` | `{"slug":...}` | replaces the key, returns it once. `404` if the profile is absent. |
| `POST /deprovision` | `{"slug":...}` | `hermes profile delete` then `purge-identity`. Idempotent. Returns no key. |
| `GET /health` | – | unauthenticated; `200` when the container answers `hermes --version`. |

## Rules enforced

- Slug must match `^u_[a-z0-9]{8,32}$`; anything else is a `400` before any command runs. `default`
  gets its own `400` and can never be provisioned, rotated or deprovisioned. Unknown JSON fields
  (`profile`, `key`, ...) are rejected.
- Fail closed: if `profile create` fails nothing is written.
- Tenant toolset policy (issue #41): before any key is issued, `/provision` runs
  `hermes -p <slug> config set agent.disabled_toolsets '[...]'` for the profile, disabling every toolset Hermes
  reports that is not in `PROVISIONER_ALLOWED_TOOLSETS` (`toolsets.go`; floor toolsets and unknown names refuse to start). Set
  the same value as `ANTARES_TENANT_ALLOWED_TOOLSETS`. `/rotate` reapplies it, so an edit to a profile's
  `agent.disabled_toolsets` is overwritten by the next provision or rotate. Hermes applies that list last,
  over every platform list. If it fails, no key is written (`502`).
- Keys travel to the container on stdin (not argv), are never logged, and never appear in errors.

## Timeouts and cancellation

Each `docker exec` is bounded at 60s and a whole request at 330s (`opBudget`), detached from the
client connection; the write deadline is set once the request holds the mutation lock. The server
therefore never abandons a request after the key is written, even if the caller disconnects.

If a caller still never *receives* a key (client timeout, lost response), the profile has a key
Antares does not know. `/provision` then returns `409`; recover with `POST /rotate`, which returns a
fresh key. A provision that fails after `profile create` but before the key is written leaves a
keyless profile, which a plain retry of `/provision` completes.

## Residue directory

Hermes' gateway recreates the (empty) profile directory about 30s after a delete, when it unserves
the profile, so `/deprovision` alone cannot leave `/opt/data/profiles/` clean. A sweeper (every 20s,
under the same lock as the mutating endpoints) `rmdir`s empty directories whose name is a valid `u_`
slug and is not a live profile. Other names, such as `.deleted`, are never touched.

If `/deprovision` returns `502` naming `/opt/data/profiles/<slug>`, the profile and its identity are
already gone but the directory was not empty, so the helper refused to delete it. Retrying cannot
clear it. Find out what is using it, then run `rmdir` on it by hand (as the `hermes` user).

## Test

```sh
go test ./...
```
