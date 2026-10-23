# Runbook — backup, restore and database corruption recovery

Antares' only state is one SQLite database (`ANTARES_DB_PATH`, `/data/antares.sqlite` in the image) plus
its WAL. Tooling is built into the `antares` binary (the image has no shell or `sqlite3`):

| Command | What it does |
|---|---|
| `antares backup <file>` | Consistent snapshot of the live database via `VACUUM INTO`, safe beside active writers. Refuses to overwrite, and refuses a destination inside the data directory (Antares would then refuse to start; use `-` or another mount). Verified with `integrity_check` before it appears at `<file>`; mode 0600. |
| `antares backup -` | Same, streamed to stdout (for the read-only container, where `/data` is the only writable path). |
| `antares restore (-keep-old DIR \| -discard-old) <file>` | Verifies `<file>`, then replaces the database. **Antares must be stopped**; restore refuses if it is not (see below). |
| `antares check [file]` | `integrity_check` + `foreign_key_check`; default file is `ANTARES_DB_PATH`. Exit 0 and `ok <path>`, or non-zero. Paths containing `?`, `#` or `%` are refused (the SQLite driver would read a different file), and it confirms through SQLite which file it actually opened. If more than 50 problems exist it says so. Stop Antares first when checking the live file. |

Never back up by copying `antares.sqlite` while Antares runs: in WAL mode committed rows live in
`-wal`, and a copy of the main file alone is silently stale or inconsistent.

## What restore checks, and refuses

Before it changes anything, restore requires the file to be readable SQLite, pass `integrity_check` and
`foreign_key_check`, contain `schema_migrations`, and record only migrations this binary embeds with
identical checksums. An **older** schema is accepted (startup migrates it forward). Refused, with a
message and the live database untouched: garbage, truncated, empty, non-Antares SQLite, and a backup
from a newer binary. It then stages a copy beside the live file, re-verifies it, refuses if another
process holds the live database (exclusive lock, see below), saves the current files (`-keep-old`), renames the live
files aside (a stale WAL replayed onto a restored file corrupts it), renames the copy into place,
fsyncs the directory and only then deletes the old files; if the swap fails they are put back. An
unexplained failure to open the live file (permissions, I/O) also refuses: only "locked" or a file
SQLite calls corrupt/not-a-database lets restore proceed. If a restore is killed mid-swap, the live
name may be absent and the old files sit as `antares.sqlite.restore-tmp.old*`: rename them back (or
restore again) before starting Antares, which would otherwise create an empty database.

`-keep-old` or `-discard-old` is mandatory so that throwing the current database away is a decision.
`-keep-old DIR` must be **outside** the data directory — not the directory itself, not a subdirectory, not a
symlink into it (all resolved before the check). Antares refuses to start over a data directory that holds
other entries, so restore rejects such a path up front, before staging or locking, and changes nothing.

## "Stopped" is enforced, within limits

`store.Open` holds a shared `flock` on the data **directory** for as long as the handle is open (taken
before the database file may be created), and restore takes an exclusive one before touching anything
and keeps it until it returns. So a running Antares — even with an idle connection
pool and no transaction — makes restore refuse with "the live database is in use"; so does a concurrent
`antares backup`. The lock is released by the kernel if the process dies. Restore also checks that no
writer holds SQLite's write lock, which catches tools that bypass `store.Open` (the `sqlite3` CLI).

Limits: a process that has the file open **read-only without `store.Open`** (a `sqlite3` shell, a
copy running in the background) is not detected; `flock` is advisory and not reliable on network
filesystems. Stopping Antares remains the operator's step — the lock only turns forgetting it into an
error instead of data loss. While restore holds the lock, `Open` (and so `antares` start-up and `antares backup`) refuses, including
in the moment during the swap when the live name does not exist, so no empty database can be created
under it. That covers every process that uses `store.Open`; it does not cover someone creating
`antares.sqlite` by hand or a tool that opens the path without `store.Open`.

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

Symptoms: `/readyz` 503, "database disk image is malformed" in the logs, `antares check` prints
`CORRUPT <path>` followed by SQLite's findings and exits 1. (A file that is not SQLite at all and a
file damaged inside both report this way; `check` fails with a plain error only when it cannot open the
file, e.g. wrong path or permissions.)

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

Paths shortened; output is verbatim. `/dev/shm` is a different filesystem from the database.

```
$ antares check
ok run/data/antares.sqlite                                     [exit 0]
$ antares backup /dev/shm/abk/a1.bak
backup written and verified: /dev/shm/abk/a1.bak               [exit 0]
$ antares backup - > run/a2.bak ; antares check run/a2.bak
ok run/a2.bak                                                  [exit 0]
$ head -c 3000 run/a2.bak > run/trunc.bak ; antares check run/trunc.bak
CORRUPT run/trunc.bak
  database disk image is malformed (11)
... integrity check failed; see docs/runbooks/backup-restore.md [exit 1]
$ antares restore -discard-old run/trunc.bak
restore: refusing run/trunc.bak: integrity_check failed: database disk image is malformed (11)   [exit 1]
$ antares restore run/a2.bak
usage: antares restore (-keep-old DIR | -discard-old) <backup-file>   (stop antares first)
restore: give a backup file and exactly one of -keep-old or -discard-old   [exit 1]
$ antares restore -keep-old run/old run/a2.bak
restored run/data/antares.sqlite from run/a2.bak; start antares and check /readyz   [exit 0]
  (kept: run/old/antares.sqlite.20261023T035116.291329Z; run/data holds only antares.sqlite)
(first 8 bytes of the database overwritten with X)
$ antares check
CORRUPT run/data/antares.sqlite
  file is not a database (26)
... integrity check failed; see docs/runbooks/backup-restore.md [exit 1]
$ ANTARES_DB_PATH=run/typo/antares.sqlite antares backup run/x.bak
backup: database run/typo/antares.sqlite: stat run/typo/antares.sqlite: no such file or directory   [exit 1]
```

Backup never creates the database: a wrong `ANTARES_DB_PATH` fails instead of writing an empty
"verified" backup.

CI proves the loop without the host: `go test ./internal/store -run 'Backup|Restore|Check'` and
`go test ./cmd/antares -run AdminCmds` (data → backup → destroy → restore, chat and messages back;
backup beside an active writer; bad files refused with the live files byte-identical).
