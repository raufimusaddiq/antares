# hermes-provisioner

Host-side helper that creates and removes per-user Hermes profiles for Antares (issue #18). It runs
**on the host**, never inside the Antares container, and reaches Hermes only through
`docker exec -u hermes <container> hermes ...` with argv arrays (no shell, no string interpolation).

## Run

```sh
export PROVISIONER_TOKEN=...      # required, >= 32 chars, no default; the service refuses to start without it
export PROVISIONER_ADDR=127.0.0.1:8750   # optional (default shown)
export HERMES_CONTAINER=hermes           # optional (default shown)
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
- Keys travel to the container on stdin (not argv), are never logged, and never appear in errors.

## Test

```sh
go test ./...
```
