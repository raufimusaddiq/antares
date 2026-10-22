# Runbook — data layer decisions (slice 1)

Three decisions the PRD left open. Code: `internal/store`, `internal/secrets`.

## Hermes key at rest

Per-user Hermes API keys are stored in `tenant_bindings` as AES-256-GCM ciphertext
(`credential_ciphertext`, `credential_key_version`). The key is derived from `ANTARES_SECRET_KEY`
(runtime secret, never in the database or repository); each blob is bound to its row (AAD = binding
id), so it cannot be moved between rows. `credential_fingerprint` is an HMAC of the key; its UNIQUE
constraint stops one key being bound to two users without storing anything reversible.

- **Attacker with the DB file only** (backup leak, copied volume): learns profile names and
  fingerprints, cannot recover keys.
- **Attacker with the DB file and `ANTARES_SECRET_KEY`** (or code execution in the process): recovers
  all keys. The key is not protected from the running process; that is the limit of a single-host design.
- **Upgrade path:** `credential_key_version` allows re-sealing under a new master key (decrypt with
  the old, write with the new). If a stronger boundary is needed, replace `secrets.Box` with a
  call to an external secret store/KMS; the column then holds a reference. Nothing else changes.

## Admin bootstrap is single-shot

Config names a Google `(issuer, sub)` and email. On start, one IMMEDIATE transaction checks the
`admin_bootstrap` latch (single row, `CHECK (id = 1)`), creates (or promotes the existing row for)
that user as `ADMIN/APPROVED` and sets the latch. Later starts, including concurrent ones and ones with
different config, change nothing. Changing the admin afterwards is a deliberate, audited operation, not
a config edit.

## Migrations

Forward-only, embedded SQL files `NNNN_name.sql`. Each runs in its own transaction with its
`schema_migrations` row (SQLite DDL is transactional), so a failure rolls back fully and the process
refuses to start. A recorded migration whose checksum differs from its file, or a recorded version
the binary does not know, also refuses startup. Recovery: restore the pre-migration backup or deploy
the matching binary; there are no down-migrations. `/readyz` is 503 unless the DB answers and is at
the latest version.

## `approvals` vs `audit_log`

- `approvals`: per-user lifecycle transitions made by an actor — approve, reject, suspend, reactivate —
  with `from_status`, `to_status` and `reason`. It backs the admin user-detail history (AUTH-005).
- `audit_log`: everything else that is security- or system-relevant — admin bootstrap, logins, binding
  changes, key rotation, policy changes, denied requests. It never carries status transitions.

A status change writes `approvals`; it may also write `audit_log` for the request outcome. Bootstrap
writes only `audit_log` because it has no actor and is not an approval decision.

## File and path safety

`store.Open` guarantees, on every start:

- The data directory is forced to 0700 (even if it existed as 0755), after resolving symlinks, so a
  data dir mounted through a symlink works and the chmod hits the real directory.
- The database file and any existing `-wal`/`-shm` sidecars are forced to 0600 before the first
  connection, and again after the pragmas run (SQLite creates sidecars lazily and only copies the db
  file's mode when it creates them). Sidecars from a restore are chmod-ed, never deleted: deleting a
  WAL loses committed rows.
- Paths containing `?`, `#` or `%` are rejected (the driver would rewrite them), and
  `PRAGMA database_list` proves the file the driver opened is the file that was secured.

Not guaranteed: files are readable with their old mode between the restore and the next start, and a
sidecar SQLite creates later (after a checkpoint removes the old one) takes the db file's mode, 0600.
Restore into a 0700 directory.
