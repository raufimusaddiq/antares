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

## Sessions are dropped on restore (decision, #78)

A backup holds `app_sessions` as they were when it was taken. Carried forward, a restore would re-admit
every session that was valid then, **including ones an admin revoked afterwards**: a revocation is just
`revoked_at` on a row, and the backup predates it. Reproduced on `main` before the fix: a session revoked
after the backup authenticated again on the restored database (`SessionUser` returned the user).

Decision: **restore deletes every `app_sessions` row** from the verified staged copy, before it is swapped
in, so nothing is ever live with the old sessions. Every user, including the admin, signs in again with
Google. Accounts, approvals, bindings, chats and runs are untouched. The live database is not modified if
this step fails. Backup files themselves still contain session hashes (never raw tokens); treat them as
sensitive.

Operator obligations after a restore:

1. Expect everyone to be signed out; tell users. This is not a fault.
2. Restore also loses anything written after the backup, **including account changes**: an account
   suspended or revoked after the backup is `ACTIVE` again in the restored database. Before users
   return, check the account list and re-apply any suspension or revocation made since the backup.
3. Restore from a backup taken from another environment or an untrusted source only with the same care.

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

## Live operation, verified on the host (#175, 2026-10-27)

**Schedule.** Until 2026-10-27 there was no scheduled backup on the host: no cron entry, no systemd timer; backups were
taken by hand at deploys. Since then `antares-backup.timer` (`deploy/antares-backup.{sh,service,timer}`, installed as
`/opt/antares/bin/antares-backup.sh` and `/etc/systemd/system/antares-backup.{service,timer}`) runs daily at 02:30 local
(±15 min, `Persistent=true` so a missed run catches up). Each run takes `antares backup -`, verifies the file with
`antares check` from the running image, and only then names it `/var/backups/antares/scheduled/antares-<UTC>.sqlite`
(dir `0700 root`, file `0600`); a failure exits non-zero and systemd records the run as failed
(`systemctl status antares-backup.service`, `journalctl -u antares-backup`). **Retention:** the newest 14 verified
backups. First run, verbatim (journal):

```
ok /b/.antares-20261027T082145Z.sqlite.partial
antares-backup: verified /var/backups/antares/scheduled/antares-20261027T082145Z.sqlite (524288 bytes) from ghcr.io/raufimusaddiq/antares:b0707458ef0946abefb91bf00516258627912b19
```

**Restore drill** (the backup taken before the `b070745` deploy, restored with that image into a scratch directory, never
the live volume): `check` on the backup → `ok`; `restore -keep-old` → `restored …`; `check` on the result → `ok`; the
restored database holds **0** `app_sessions` rows (everyone signs in again, as designed); schema at migration 16, so
start-up migrates it forward to 18. **Reconciliation** against a fresh backup of the live database: 0 accounts changed
state, 0 created, 0 removed since the backup, so nothing would have to be re-applied. Counts only are recorded; the
repository is public. The drill's container ran as uid 65532 and could not read the root-only backup; the drill copies
the file into a directory owned by 65532 first (the scheduled check runs as root, read-only, instead).

**Secret recovery path.** The database alone is not a working Antares: `ANTARES_SECRET_KEY` decrypts the stored Hermes
keys. It lives only in `/opt/antares/antares.env` (`0600 root`) on this host, beside the Google OIDC client secret
(recoverable from the Google Cloud console) and the provisioner token and shared inference key in
`/opt/antares/provisioner.env`. **Gap, recorded rather than hidden:** neither the backups nor `/opt/antares` are copied off
this host. Losing the host loses both; recovery is then a fresh install, a new `ANTARES_SECRET_KEY`, and re-provisioning
every tenant (bindings re-created; Hermes keeps the transcripts). Losing only the key, with a backup in hand, has the
same remedy for the bindings while accounts and chats survive.

## Take a backup

Before every migration/deploy (in addition to the daily timer above):

```
docker exec antares /antares backup - > /var/backups/antares/antares-$(date -u +%Y%m%dT%H%M%SZ).sqlite
docker run --rm -v /var/backups/antares:/b:ro ghcr.io/raufimusaddiq/antares:<tag> check /b/<file>   # verify it
```

The `docker exec … backup -` form has been run on the live host (deploys, the daily timer). The check needs a file the
container user can read: run it with `--user 0:0` and a read-only mount, as `antares-backup.sh` does.

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
4. Restore signs every user out (it clears `app_sessions`). Start Antares: `docker compose up -d antares`; wait for `/readyz` 200 (it applies any newer
   migrations first). Confirm a known account and a chat are present, and re-apply any suspension or revocation made since
   the backup (see "Sessions are dropped on restore").

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

## Walkthrough: backup → restore → smoke (2026-10-23, issue #75)

Backup, restore and the smoke gate had never been run together end to end; this is that run. Everything
was on a scratch data directory on a build host, not the live deployment. `$T` stands for the scratch root;
all output is verbatim except that `$T` replaces the path and timestamps on `log` lines are dropped.

**What was real and what was not.** `antares` (`backup`, `check`, `restore`) and `antares-smoke` are the
binaries built from this commit (`go build ./cmd/antares ./cmd/antares-smoke`). The *server* in between was
not the `antares serve` binary: it needs Google as the identity provider (fixed in `internal/auth`) and a
live Hermes, and no Google sessions or Hermes exist on this host. So a throwaway Go test harness (not
committed) started the real Antares packages — store, auth, tenant pipeline, chat, HTTP server — on a
file-backed SQLite database over a real socket, with the fake OIDC provider, fake Hermes and fake provisioner
from `internal/smoke/stack_test.go`. The database and its schema are real; the Hermes replies and the
Google sign-in are fabricated. This proves the data survives backup/restore and that the restored database
serves a complete smoke run. It does **not** prove the Docker procedure above, a restore against a real
Hermes, or `antares serve` itself starting on a restored volume.

### 1. Create state worth losing

The harness ran the smoke library once against an instance on `$T/live/antares.sqlite` (verdict PASS). That
signs in four accounts, approves two, provisions their tenants, sends a chat in each and revokes/reactivates
one. Antares was then stopped (database closed) and rows were counted:

```
admin_bootstrap          1
app_sessions             4
approvals                4
audit_log                29
chats                    2
messages                 0
push_delivery_events     0
push_subscriptions       0
runs                     2
schema_migrations        8
search_docs              6
search_state             2
tenant_bindings          3
user_preferences         0
users                    4
```

(`messages` is 0 by design: transcripts live in Hermes; Antares keeps chat and run rows.)

### 2. Back up

```
$ ANTARES_DB_PATH=$T/live/antares.sqlite antares backup $T/backups/antares-backup.sqlite
backup written and verified: $T/backups/antares-backup.sqlite
[exit 0]
$ antares check $T/backups/antares-backup.sqlite
ok $T/backups/antares-backup.sqlite
[exit 0]
```

### 3. Corrupt backups are refused (copies of the backup above)

`truncated` = first 3000 bytes; `bitflip` = 4096 bytes in the middle inverted; `garbage` = a 29-byte text file.
Each was run through `check` and through `restore -discard-old` into an **empty** data directory, which
still held 0 entries afterwards in all three cases.

```
$ antares check $T/backups/truncated.sqlite
CORRUPT $T/backups/truncated.sqlite
  database disk image is malformed (11)
integrity check failed; see docs/runbooks/backup-restore.md
[exit 1]
$ ANTARES_DB_PATH=$T/restored/antares.sqlite antares restore -discard-old $T/backups/truncated.sqlite
restore: refusing $T/backups/truncated.sqlite: integrity_check failed: database disk image is malformed (11)
[exit 1]

$ antares check $T/backups/bitflip.sqlite
CORRUPT $T/backups/bitflip.sqlite
  database disk image is malformed (11)
integrity check failed; see docs/runbooks/backup-restore.md
[exit 1]
$ ANTARES_DB_PATH=$T/restored/antares.sqlite antares restore -discard-old $T/backups/bitflip.sqlite
restore: refusing $T/backups/bitflip.sqlite: integrity_check failed: database disk image is malformed (11)
[exit 1]

$ antares check $T/backups/garbage.sqlite
CORRUPT $T/backups/garbage.sqlite
  file is not a database (26)
integrity check failed; see docs/runbooks/backup-restore.md
[exit 1]
$ ANTARES_DB_PATH=$T/restored/antares.sqlite antares restore -discard-old $T/backups/garbage.sqlite
restore: refusing $T/backups/garbage.sqlite: integrity_check failed: file is not a database (26)
[exit 1]
```

Nothing was accepted silently. (The restore message names the file and SQLite's reason; it does not say
"nothing was changed", but the empty directory shows that.)

### 4. Restore the good backup into the empty directory

```
$ ANTARES_DB_PATH=$T/restored/antares.sqlite antares restore -discard-old $T/backups/antares-backup.sqlite
restored $T/restored/antares.sqlite from $T/backups/antares-backup.sqlite; start antares and check /readyz
[exit 0]
$ ANTARES_DB_PATH=$T/restored/antares.sqlite antares check
ok $T/restored/antares.sqlite
[exit 0]
```

Antares was started on `$T/restored` and the same count query returned **identical numbers for every
table** listed in step 1 (4 users, 2 chats, 2 runs, 3 tenant bindings, 29 audit rows, 8 migrations).

### 5. Smoke against the restored instance

Four fresh sessions were minted by signing the same four accounts in again (the accounts, approvals and
tenants came from the restored database), then the real binary ran:

```
$ antares-smoke --url http://127.0.0.1:18475 --origin https://antares.smoke.test --json smoke.json
antares smoke test  target=http://127.0.0.1:18475  run=cb1254c0  sessions supplied=4/4
[ PASS ] reach.health                   2ms  /healthz ok, /readyz ready (process answers; this proves nothing about chat, the steps below do)
[ PASS ] login.admin                    3ms  signed in; account state ACTIVE
[ PASS ] login.a                        1ms  signed in; account state ACTIVE
[ PASS ] login.b                             signed in; account state ACTIVE
[ PASS ] login.pending                  3ms  signed in; account state PENDING
[ PASS ] admin.active                   8ms  admin API reachable
[ PASS ] negative.unapproved           15ms  pending account refused (403 ACCOUNT_PENDING) on chat list, create, send and admin
[ PASS ] account.approve                2ms  A already approved, B already approved
[ PASS ] tenant.ready.a                 3ms  tenant READY with a READY binding after 3ms
[ PASS ] tenant.verify.a             1.006s  READY only after Antares' six checks (endpoint, profile_identity, credential_isolation, session_ownership, toolset_policy, memory_boundary) passed again, in 1.004s
[ PASS ] chat.a.reply                  11ms  run COMPLETED in 8ms, assistant replied (41 chars); events run.started×1 assistant.delta×1 run.completed×1
[ PASS ] tenant.ready.b                      tenant READY with a READY binding after 1ms
[ PASS ] tenant.verify.b             1.006s  READY only after Antares' six checks (endpoint, profile_identity, credential_isolation, session_ownership, toolset_policy, memory_boundary) passed again, in 1.005s
[ PASS ] chat.b.reply                  11ms  run COMPLETED in 5ms, assistant replied (41 chars); events run.started×1 assistant.delta×1 run.completed×1
[ PASS ] negative.cross_user           15ms  14 cross-user probes in both directions all refused (404 NOT_FOUND); each owner could read their own chat
[ PASS ] negative.revoked               9ms  B refused (401/403) on 6 routes within 6ms of the suspension, no waiting; A unaffected. B's session is now revoked: sign B in again before the next run
[ PASS ] cleanup.restore             1.005s  B reactivated and READY again
RESULT: PASS (17 passed, 0 failed, 0 skipped)
[exit 0]
```

Exit 0, not 2: all four sessions were supplied and no step was skipped.

### Known gaps found by the walkthrough

- Not exercised: the Docker/volume procedure, `antares serve` on a restored volume, and a real Hermes or
  Google. A repeat on the live host (by the owner) is still owed before calling the procedure proven there.
- Sessions in `app_sessions` survived a restore (4 rows came back), re-admitting sessions revoked after the
  backup. Resolved in #78: restore now clears `app_sessions` (see "Sessions are dropped on restore").
- `check`/`restore` print the error twice-framed: a bare `integrity check failed; see …` line follows the
  findings (cosmetic), and `restore`'s refusal does not state that the live files were left untouched.
- Repeating it: the harness is not in the repository. To repeat without it, use steps 2–4 on any scratch
  database and run `antares-smoke` as in `docs/runbooks/smoke-test.md` against an instance started on
  the restored directory.
