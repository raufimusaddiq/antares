# Deploying Antares (operator sequence)

These are the artifacts only. Nothing here has been run against production. Every step below is the
operator's; a coding agent does not perform it (`docs/runbooks/deployment.md`).

**Security status.** Phase 0 failed (`docs/hermes/PHASE0-FINDINGS.md`). Until it is re-demonstrated the
deployment is for **one trusted user**. Do not describe it as isolating mutually untrusted users.

## What gets published

`.github/workflows/release.yml` runs on every push to `main` and publishes
`ghcr.io/raufimusaddiq/antares:<full 40-char commit SHA>` and `ghcr.io/raufimusaddiq/antares:latest`.
Deploy and roll back by the SHA tag; `latest` moves. If the package is private, `docker login ghcr.io`
on the host with a token that has `read:packages`.

## Bumping the Go build base

The Dockerfile pins `golang:<version>-alpine@sha256:<digest>`. To bump: `docker pull golang:<new>-alpine`,
read the digest from `docker image inspect golang:<new>-alpine --format '{{index .RepoDigests 0}}'`, confirm
`docker run --rm golang:<new>-alpine go version` prints the version, then edit both. When a new Go major
ships, update `SUPPORTED` in `scripts/check_go_base.py`; CI fails if the base leaves that window or falls
behind `go.mod`.

## 1. Secret files (outside the repository)

```sh
sudo install -d -m 0750 /etc/antares
sudo install -m 0600 /dev/null /etc/antares/antares.env        # variables: see ../.env.example
sudo install -m 0600 /dev/null /etc/antares/provisioner.env
```

`provisioner.env`:

```
PROVISIONER_TOKEN=<>=32 random chars; the same value as ANTARES_PROVISIONER_TOKEN>
# HERMES_CONTAINER=hermes
```

`antares.env` holds every variable in `.env.example` (real values). Generate:
`ANTARES_SECRET_KEY=$(head -c32 /dev/urandom | base64)`, `PROVISIONER_TOKEN=$(head -c48 /dev/urandom | base64 | tr -d '=+/')`.
`ANTARES_HERMES_URL=http://hermes:8642`, `ANTARES_PROVISIONER_URL=http://host.docker.internal:8750`.
The Google OIDC client (redirect URI `<ANTARES_PUBLIC_ORIGIN>/api/auth/google/callback`) is created by
the owner.

## 2. Pull the image

```sh
export ANTARES_TAG=<full commit sha>
docker pull ghcr.io/raufimusaddiq/antares:$ANTARES_TAG
```

## 3. Provisioner (host, systemd)

Build the helper from the same commit and install it:

```sh
git -C <checkout> checkout $ANTARES_TAG
(cd <checkout>/tools/hermes-provisioner && go build -o /tmp/hermes-provisioner .)
sudo useradd --system --create-home --shell /usr/sbin/nologin antares-prov   # skip if it exists
sudo usermod -aG docker antares-prov
sudo install -d /opt/antares/bin && sudo install /tmp/hermes-provisioner /opt/antares/bin/
sudo cp deploy/hermes-provisioner.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now hermes-provisioner
```

The unit binds `172.17.0.1:8750` (the docker bridge gateway that `host.docker.internal` resolves to
with `host-gateway`). **Check before starting**: `docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}'`
and edit `PROVISIONER_ADDR` if it differs. It must not be `0.0.0.0`, and the port must not be exposed
at the edge or in the firewall's public zone. `curl -H "Authorization: Bearer …" http://172.17.0.1:8750/health`
from the host confirms it answers.

## 4. Start Antares

The Hermes network (`hermes_edge`) must already exist.

```sh
cd deploy
ANTARES_TAG=$ANTARES_TAG docker compose up -d
docker compose ps                 # antares: healthy
curl -fsS http://127.0.0.1:8080/readyz     # {"status":"ready"}
```

Then verify with one real authenticated request (sign in as the bootstrap admin), not just "up".

## 5. Caddy (documented, not applied)

Add to the existing site, then `caddy reload` (the owner's step):

```caddy
antares.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1      # SSE run streams must not be buffered
	}
}
```

If Caddy runs in a container rather than on the host, join it to a network shared with `antares` and
proxy to `antares:8080` instead; the published loopback port is then unused.

## Roll back

Keep the previous tag until the new one is verified.

```sh
ANTARES_TAG=<previous full sha> docker compose up -d
```

Migrations are forward-only: back up the volume first
(`docker run --rm -v deploy_antares-data:/d -v "$PWD":/b busybox tar czf /b/antares-data.tgz -C /d .`;
the volume name is `<compose project>_antares-data`) and restore it if the previous image cannot
read the migrated schema.

## Unverified here (operator must check)

- Pulling from ghcr.io (needs the first workflow run on `main`, and package visibility).
- `hermes_edge` exists and `hermes:8642` resolves from the `antares` container.
- The provisioner address is reachable from the container as `host.docker.internal:8750`, and
  host firewall rules allow it from the bridge but not from outside.
- The `antares-prov` user can `docker exec -u hermes hermes …`.
- The Caddy block against the live Caddyfile.
