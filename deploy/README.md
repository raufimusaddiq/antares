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
sudo install -d -m 0755 -o root -g root /opt/antares /opt/antares/bin
sudo install -m 0600 -o root -g root /dev/null /opt/antares/antares.env        # variables: see ../.env.example
sudo install -m 0600 -o root -g root /dev/null /opt/antares/provisioner.env
```

Layout:

| Path | Mode | Owner |
|---|---|---|
| `/opt/antares`, `/opt/antares/bin` | `755` | `root:root` |
| `/opt/antares/antares.env`, `/opt/antares/provisioner.env` | `600` | `root:root` |
| `/opt/antares/bin/hermes-provisioner` | `755` | `root:root` |

Why: everything is root-owned, so only root can create, rename or unlink anything in the tree. The
directories are world-searchable (`755`) because the unit runs as `antares-prov`, and `execve` of
`ExecStart` needs search on every parent directory. The secrets stay unreadable to everyone but root
(`600`): systemd reads `EnvironmentFile` as root before dropping privileges, so the helper needs no
read access. The deploying user and `antares-prov` cannot replace the binary, which runs with the
Docker group. Edit the env files with `sudo`. Compose also reads `antares.env` as the invoking user,
so run `docker compose` with `sudo` (or point `ANTARES_ENV_FILE` at a readable copy).

`root:root 0755` is chosen over a traverse-only ACL: it needs no `setfacl`/filesystem support, is what `ls -l`
shows, and exposes nothing (the secrets inside are `600`). On 2026-10-23 the live host had `/opt/antares` at
`700 ubuntu:ubuntu`; `antares-prov` could not reach `ExecStart` and the unit crashlooped with `203/EXEC`
(47 restarts). `deploy/install-provisioner.sh` re-applies the modes with `install -d` and fails if they differ;
`deploy/install-provisioner.sh --check-dirs` asserts them without changing anything. Env files are only
stat'ed, never read or printed, and never loosened.

`provisioner.env`:

```
PROVISIONER_TOKEN=<>=32 random chars; the same value as ANTARES_PROVISIONER_TOKEN>
# Explicit operator-managed shared launch-provider source; never a user profile .env.
PROVISIONER_INFERENCE_API_KEY=<shared-inference-provider-key>
PROVISIONER_INFERENCE_BASE_URL=<exact-shared-inference-endpoint>
# HERMES_CONTAINER=hermes
# Must equal ANTARES_TENANT_ALLOWED_TOOLSETS in antares.env (default shown). If the two differ,
# toolset_policy fails for every tenant, or the provisioner refuses to start on an unknown name.
PROVISIONER_ALLOWED_TOOLSETS=web,vision,image_gen,skills,memory,todo,clarify
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

The helper is a **separate Go module** (`tools/hermes-provisioner`). Pulling the Antares image does not
rebuild it: on 2026-10-23 the host ran the PR #39 build while `af2ad22` was deployed, so the toolset
policy did not exist and every tenant failed `toolset_policy` (#50). **Every deploy rebuilds and
reinstalls it, in this order** (the old process holds the inode, so stop before replacing):

```sh
# Go stamps vcs.modified=true on ANY untracked file; checkout alone leaves them, and the revision check
# below would then reject a correct deploy after the binary is installed. Use a dedicated checkout:
# clean -fdx deletes everything untracked or ignored in it.
git -C <checkout> checkout --detach $ANTARES_TAG && git -C <checkout> clean -fdx
cd <checkout>/tools/hermes-provisioner && go build -o /tmp/hermes-provisioner .
sudo systemctl stop hermes-provisioner
sudo install -m 0755 -o root -g root /tmp/hermes-provisioner /opt/antares/bin/hermes-provisioner
sudo systemctl start hermes-provisioner
```

`sudo PROVISIONER_HEALTH_URL=http://<PROVISIONER_ADDR>/health deploy/install-provisioner.sh <built-binary> $ANTARES_TAG`
(the variable is only needed when the bridge gateway is not `172.17.0.1`; it must match the unit's
`PROVISIONER_ADDR`, otherwise the final check fails on a deploy that succeeded) does the stop, install and start
and also enforces the directory contract (section 1) and the revision check below; prefer it. First-time
host setup, once:

```sh
sudo useradd --system --create-home --shell /usr/sbin/nologin antares-prov   # skip if it exists
sudo usermod -aG docker antares-prov
sudo cp deploy/hermes-provisioner.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable hermes-provisioner
```

**Revision check (a deploy is not verified without it).** `GET /health` returns
`{"status":"ok","revision":"<vcs.revision>","modified":false}`, read by `debug.ReadBuildInfo` from the
binary itself. Without a VCS stamp `revision` is `"unavailable"`, never a made-up value. Then:

```sh
deploy/verify-helper-revision.sh $ANTARES_TAG http://172.17.0.1:8750/health
```

It exits non-zero when the helper's revision differs from `$ANTARES_TAG`, is `unavailable`, was built from
a modified tree, or `/health` lacks the field (a pre-#50 helper). `200` and `systemctl is-active` do not
prove the helper is current; this does. `strings -a /opt/antares/bin/hermes-provisioner | grep vcs.revision`
shows the same thing offline.

The unit binds `172.17.0.1:8750` (the docker bridge gateway that `host.docker.internal` resolves to
with `host-gateway`). **Check before starting**: `docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}'`
and edit `PROVISIONER_ADDR` if it differs. It must not be `0.0.0.0`, and the port must not be exposed
at the edge or in the firewall's public zone. `curl http://172.17.0.1:8750/health`
from the host confirms it answers.

## 4. Start Antares

Both external networks must already exist; `antares` joins both and creates neither:

- `hermes_edge`: lets `antares` reach `hermes:8642`.
- `idx_default`: lets Caddy (which is on `idx_default`, not `hermes_edge`) resolve `antares:8080`.
  Without it the route answers 502.

```sh
cd deploy
# env_file is /opt/antares/antares.env (0600 root:root): run compose with sudo (keep ANTARES_TAG), or set ANTARES_ENV_FILE
sudo ANTARES_TAG=$ANTARES_TAG docker compose up -d
sudo docker compose ps                 # antares: healthy
curl -fsS http://127.0.0.1:8080/readyz     # {"status":"ready"}
```

`ready` and `healthy` only say the process and its database are up. They are not the check. Run the gate next.

## 4a. The deploy gate (smoke test)

From any machine that reaches the deployment over HTTPS (not the host, no secrets from the host):

```sh
export ANTARES_SMOKE_COOKIE_ADMIN=… ANTARES_SMOKE_COOKIE_A=… ANTARES_SMOKE_COOKIE_B=… ANTARES_SMOKE_COOKIE_PENDING=…
go run ./cmd/antares-smoke --url https://<ANTARES_PUBLIC_ORIGIN host> --json smoke-result.json
```

Exit `0` is the only result that means deployed. `1` (a step failed; the output names it) and `2` (a step did not
run) mean it is not: roll back or fix, then run again. Where the four sessions come from, every step, and what it does
not cover: `docs/runbooks/smoke-test.md`.

With synthetic accounts instead of real sessions, run the same command through the fixture wrapper so that every fixture is
removed afterwards, on failure too (`python3 -I scripts/smoke_fixtures.py run --db … --workdir … -- <antares-smoke …>`). Only its
exit `0` counts: `4` means cleanup left residue. Usage and guarantees: `docs/runbooks/smoke-test.md`.

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

Caddy runs in a container on `idx_default`, so on this host proxy to `antares:8080` (the compose file
joins `antares` to that network) rather than `127.0.0.1:8080`, which inside Caddy's container is
Caddy itself. The loopback publish is kept only for host-side checks such as `/readyz` above; it is
never `0.0.0.0`.

## Roll back

Keep the previous tag until the new one is verified.

```sh
sudo ANTARES_TAG=<previous full sha> docker compose up -d
```

Migrations are forward-only: back up the volume first
(`docker run --rm -v deploy_antares-data:/d -v "$PWD":/b busybox tar czf /b/antares-data.tgz -C /d .`;
the volume name is `<compose project>_antares-data`) and restore it if the previous image cannot
read the migrated schema.

## Unverified here (operator must check)

- Pulling from ghcr.io (needs the first workflow run on `main`, and package visibility).
- `hermes_edge` and `idx_default` exist, `antares:8080` resolves from Caddy, and `hermes:8642` resolves from the `antares` container.
- The provisioner address is reachable from the container as `host.docker.internal:8750`, and
  host firewall rules allow it from the bridge but not from outside.
- The `antares-prov` user can `docker exec -u hermes hermes …`.
- The Caddy block against the live Caddyfile.
