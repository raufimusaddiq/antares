# Runbook — backup, restore and database corruption recovery

Antares' only state is one SQLite database (`ANTARES_DB_PATH`, `/data/antares.sqlite` in the image) plus
its WAL. Tooling is built into the `antares` binary (the image has no shell or `sqlite3`):

| Command | What it does |
|---|---|
| `antares backup <file>` | Consistent snapshot of the live database via `VACUUM INTO`, safe beside active writers. Refuses to overwrite. Verified with `integrity_check` before it appears at `<file>`; mode 0600. |
| `antares backup -` | Same, streamed to stdout (for the read-only container, where `/data` is the only writable path). |
| `antares restore (-keep-old DIR \| -discard-old) <file>` | Verifies `<file>`, then replaces the database. **Antares must be stopped.** |
| `antares check [file]` | `integrity_check` + `foreign_key_check`; default file is `ANTARES_DB_PATH`. Exit 0 and `ok <path>`, or non-zero. |

Never back up by copying `antares.sqlite` while Antares runs: in WAL mode committed rows live in
`-wal`, and a copy of the main file alone is silently stale or inconsistent.

## What restore checks, and refuses

Before it changes anything, restore requires the file to be readable SQLite, pass `integrity_check` and
`foreign_key_check`, contain `schema_migrations`, and record only migrations this binary embeds with
identical checksums. An **older** schema is accepted (startup migrates it forward). Refused, with a
message and the live database untouched: garbage, truncated, empty, non-Antares SQLite, and a backup
from a newer binary. It then stages a copy beside the live file, re-verifies it, refuses if another
process holds the live write lock, saves the current files (`-keep-old`), deletes the live `-wal`/`-shm`
(a stale WAL replayed onto a restored file corrupts it) and renames the copy into place.

`-keep-old` or `-discard-old` is mandatory so that throwing the current database away is a decision.

## Secrets travel separately

The database holds Hermes keys only as AES-GCM ciphertext (data-layer runbook). A restored database is
usable only with the same `ANTARES_SECRET_KEY`; keep that key in the secret store, not next to the
backups. Treat a backup file as sensitive anyway (accounts, bindings, chat metadata).

## Take a backup

Before every migration/deploy, and on whatever schedule the owner sets (there is no scheduler here):

```
docker exec antares /antares backup - > /var/backups/antares/antares-$(date -u +%Y%m%dT%H%M%SZ).sqlite
docker run --rm -v /var/backups/antares:/b:ro ghcr.io/raufimusaddiq/antares:<tag> check /b/<file>   # verify it
```

The `docker` forms are the intended host procedure and have **not** been run on the live host. The
subcommands themselves were run against a scratch database, as below.

## Restore

1. Stop Antares: `docker compose stop antares`.
2. Check the candidate: `antares check <backup>` must print `ok`.
3. Restore into the volume, keeping the old files outside the data directory (which must hold only the
   database files):
   ```
   docker run --rm -v antares-data:/data -v /var/backups/antares:/b -v /var/backups/antares-old:/old \
     -e ANTARES_DB_PATH=/data/antares.sqlite --user 65532:65532 \
     ghcr.io/raufimusaddiq/antares:<tag> restore -keep-old /old /b/<file>
   ```
4. Start Antares: `docker compose up -d antares`; wait for `/readyz` 200 (it applies any newer
   migrations first). Confirm a known account and a chat are present.

Restoring loses everything written after the backup was taken. Hermes holds the transcripts; Antares'
rows reference Hermes sessions, which a restore does not touch.

## Integrity check fails (corruption)

Symptoms: `/readyz` 503, "database disk image is malformed", `antares check` prints `CORRUPT`.

1. Stop Antares. Do not delete anything.
2. `antares check` and record the output. Copy `antares.sqlite*` out of the volume (including `-wal`) to
   a safe directory — the evidence, and the input for step 4.
3. **Preferred:** restore the newest backup that passes `antares check` (above). Accept the loss since it.
4. **No usable backup:** salvage with SQLite's recovery mode on the *copy*, on a machine with the
   `sqlite3` CLI, then load and verify the result before using it:
   ```
   sqlite3 copy.sqlite ".recover" | sqlite3 recovered.sqlite
   antares check recovered.sqlite
   ```
   `restore` accepts `recovered.sqlite` only if it passes the checks above; check the data by hand
   (users, bindings, recent chats) before starting. This step is documented from SQLite's behaviour and
   has not been exercised here; recovered data is best effort, and the incident should be reported as
   data loss if rows are missing.
5. Find the cause (full volume, abrupt host power loss with disks that lie about fsync, a plain-file
   copy over the live database) before restarting.

## What was run (scratch database, local binary, not the live host)

```
antares check                                  -> ok .../data/antares.sqlite
antares backup .../run/antares-1.bak           -> backup written and verified
antares backup - > .../run/antares-2.bak ; antares check .../antares-2.bak   -> ok
antares restore -discard-old trunc.bak         -> exit 1: ...malformed (11)   (first 3000 bytes of a backup)
antares restore antares-1.bak                  -> exit 1: give ... exactly one of -keep-old or -discard-old
antares restore -keep-old .../old antares-1.bak-> restored; old copy in .../old/antares.sqlite.<stamp>
(first 8 bytes of the database overwritten)
antares check                                  -> exit 1: file is not a database (26)
```

CI proves the loop without the host: `go test ./internal/store -run 'Backup|Restore|Check'` and
`go test ./cmd/antares -run AdminCmds` (data → backup → destroy → restore, chat and messages back;
backup beside an active writer; bad files refused with the live files byte-identical).
