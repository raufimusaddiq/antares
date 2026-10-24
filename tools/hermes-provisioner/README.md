# hermes-provisioner

Host-side helper that creates and removes per-user Hermes profiles for Antares (issue #18). It runs
**on the host**, never inside the Antares container, and reaches Hermes only through
`docker exec -u hermes <container> hermes ...` with argv arrays (no shell, no string interpolation).

## Run

```sh
export PROVISIONER_TOKEN=...      # required, >= 32 chars, no default; the service refuses to start without it
export PROVISIONER_INFERENCE_API_KEY=... # required: operator-managed shared launch-provider key
export PROVISIONER_INFERENCE_BASE_URL=... # required: exact matching OpenAI-compatible endpoint
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
| `POST /provision` | `{"slug":"u_xxxxxxxx"}` | creates the profile if absent, bootstraps the declared shared inference key/endpoint for a new profile, writes a distinct fresh `API_SERVER_KEY` to its `.env`, returns `{profile,key,created_at}` once. `409` if the profile already has a key. |
| `POST /rotate` | `{"slug":...}` | replaces the key, returns it once. `404` if the profile is absent. |
| `POST /deprovision` | `{"slug":...}` | `hermes profile delete` then `purge-identity`. Idempotent. Returns no key. |
| `GET /health` | – | unauthenticated; `200` when the container answers `hermes --version`, else `503`. Body: `{"status","revision","modified"}`, the build's `vcs.revision` (`"unavailable"` without a VCS stamp). Deploys compare it to the deployed commit: `deploy/verify-helper-revision.sh`. The exact deployed commit is visible to anything that can reach the bind address (the docker bridge), which is why the bind must never be public. |

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
- New-profile bootstrap (#132) writes only the declared `OPENAI_API_KEY`/`OPENAI_BASE_URL` pair in addition to the distinct inbound key. It never reads a user profile as its source.
- Keys travel to the container on stdin (not argv), are never logged, and never appear in errors.

## Shared inference bootstrap (#132)

Owner decision: all accounts may use the operator-managed shared inference credential. Model
choices remain independent per profile/chat. The provider secret is backend-only and is not the
per-profile `API_SERVER_KEY` used by Antares to authenticate to Hermes.

Set `PROVISIONER_INFERENCE_API_KEY` and `PROVISIONER_INFERENCE_BASE_URL` in the helper's existing
root-owned `0600` EnvironmentFile. Source them explicitly from the operator-managed launch-provider
secret and its matching endpoint. Do not copy the owner's or any user's `.env`. No default,
ambient-secret fallback, credential discovery or runtime rotation is added. Missing or malformed
values refuse helper startup, with a fixed error that does not echo either value.

For a newly created profile, the seeded `model.base_url` must match the declared endpoint
(trailing slashes are ignored). For a named provider (`providers.<name>`, bare or `custom:<name>`) the endpoint and `key_env` are read from that entry, resolved by Hermes' own `_get_named_custom_provider`, not from the model block; for `openai` and unnamed `custom` they come from `model.*`. `${OPENAI_BASE_URL}` and `${env:OPENAI_BASE_URL}` are supported
references to that same declaration. The launch provider must use the verified OpenAI-compatible
`openai`, `custom` or named `custom:<name>` credential contract. The key variable must be unset or `OPENAI_API_KEY`, with no `key_cmd`. A different provider/key mapping or endpoint fails
closed before an inbound key is issued. No new model/provider selector or fixed model is added.

Only a profile whose parsed `.env` holds no variable (absent, empty, or just the comment header `profile create` seeds) receives the inference pair; existing comment lines are kept. The write is atomic
and `0600`; values travel through stdin, never Docker argv. It neither rewrites `config.yaml`
model choices nor copies unrelated variables, channels, memory or sessions. Existing profiles
receive no inference bootstrap or shared-key replacement. `/rotate` retains its existing inbound
key/tool-policy behavior and preserves inference credentials/model choices.

This change does not repair previously created profiles with missing inference credentials.
Their recovery/backfill needs a separate operator decision. Deployment, service restart and
setting these runtime secrets are also separate from merging this code. Phase 0 remains FAIL;
operation is restricted to trusted users.

## Timeouts and cancellation

Each `docker exec` is bounded at 60s and a whole request at 330s (`opBudget`), detached from the
client connection; the write deadline is set once the request holds the mutation lock. The server
therefore never abandons a request after the key is written, even if the caller disconnects.

If a caller still never *receives* a key (client timeout, lost response), the profile has a key
Antares does not know. `/provision` then returns `409`; recover with `POST /rotate`, which returns a
fresh key. A provision that fails after `profile create` but before the key is written leaves a
keyless profile. A plain retry of `/provision` completes it: a profile with no variable in its
`.env` is bootstrapped under the same endpoint/provider checks as a new one; a profile that already
holds an inference key and a matching endpoint binding keeps it. A profile with other variables but
no matching binding is refused (`502`); the helper does not backfill it or delete/recreate the profile.

## Residue directory

Hermes' gateway recreates the (empty) profile directory about 30s after a delete, when it unserves
the profile, so `/deprovision` alone cannot leave `/opt/data/profiles/` clean. A sweeper (every 2 minutes, one `hermes profile list` per sweep,
under the same lock as the mutating endpoints) `rmdir`s empty directories whose name is a valid `u_`
slug and is not a live profile. Other names, such as `.deleted`, are never touched.

If `/deprovision` returns `502` naming `/opt/data/profiles/<slug>`, the profile and its identity are
already gone but the directory was not empty, so the helper refused to delete it. Retrying cannot
clear it. Find out what is using it, then run `rmdir` on it by hand (as the `hermes` user).

## Test

```sh
go test ./...
```
