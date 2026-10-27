# Runbook — Hermes wire fixtures from the smoke run (#169)

New UI must be built on what Hermes actually sends, not on mock event names. The fixtures in
`docs/hermes/fixtures/wire/` are captured from a real Hermes during the owner's normal synthetic smoke run
(`smoke-test.md`, "Synthetic fixtures with guaranteed cleanup"), so they use no production data, no real user and no
real credential: the run's accounts are minted by `scripts/smoke_fixtures.py` and removed by it afterwards.

## What is recorded, and for whom

`ANTARES_WIRE_CAPTURE_DIR` (off by default) turns on `hermes.Capture` (`internal/hermes/capture.go`). It records only
calls whose profile belongs to a synthetic smoke account (`google_issuer = https://synthetic-test.invalid/antares`,
`store.IsSyntheticProfile`); a real user's traffic is never written, even while it is on.

| File | Source |
|---|---|
| `run-events.jsonl` | every frame of `GET /v1/runs/{id}/events`, one `{"event", "data"}` per line |
| `run-create.json` | the answer of `POST /v1/runs` |
| `session-messages.json` | the answer of `GET /api/sessions/{id}/messages` |

Everything is **shape only, by allowlist** (`hermes.Sanitize`): an object keeps only the keys Antares knows from the
Hermes contract (`knownKeys`); any other key is dropped and counted as `"<redacted keys>": N`, because a key can itself be
data. A string becomes `"<string>"` unless it is a listed event, status, role or object name (`knownValues`); numbers
become `0`; booleans and nulls stay; arrays keep three elements. Ids, text, previews, secrets and URLs never reach a file,
whether as a value or as a key (`TestSanitizeRedactsAdversarialKeysAndValues`). Files are `0600`.

Each Antares process writes into its own subdirectory, `<dir>/<UTC start time>-<random>/`, so an older or interrupted capture is
never mixed into a new one and cannot satisfy the contract check on its behalf.

## Capture procedure (owner, on the Antares host)

1. Add `ANTARES_WIRE_CAPTURE_DIR=/data/wire-capture` to the Antares env file and redeploy (the container is read-only
   except `/data`).
2. Run the synthetic smoke as usual, with a prompt that makes the assistant use a tool so the tool events appear, for
   example `--prompt "Use web search to find today's date in Jakarta, then answer in one line."`
3. Copy the **newest** subdirectory's files out (`docker exec antares ls /data/wire-capture` lists them by start time):
   `docker cp antares:/data/wire-capture/<newest>/. docs/hermes/fixtures/wire/` and write the Hermes image id
   (`docker inspect -f '{{.Image}}' hermes`) and the capture date into `docs/hermes/fixtures/wire/VERSION`.
4. Remove the variable and delete the capture **before** restarting: Antares refuses to start over a data directory that
   holds anything but the database. Use the volume that is actually mounted at `/data` (its name depends on the Compose
   project), and check that only the database files are left before redeploying:
   ```sh
   vol=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' antares)
   test -n "$vol" || { echo "no volume mounted at /data"; exit 1; }
   docker run --rm -v "$vol":/data busybox:1.36 sh -c 'rm -rf /data/wire-capture && ls -A /data'
   # expect only: antares.sqlite antares.sqlite-shm antares.sqlite-wal
   ```
5. Read the files before committing (they are shape only, so a review is quick) and open a PR.

Re-capture after every Hermes upgrade.

## Contract check

`go test ./internal/hermes -run Wire` compares the recorded run with what `Normalize` reads (`wireDrift` in
`capture_test.go`): each field it reads must be present with the expected JSON type, and `message.delta`,
`tool.started`, `tool.completed` and `run.completed` must all appear. A missing event or a changed type fails CI. Until
the first capture is committed the check skips and says so.
