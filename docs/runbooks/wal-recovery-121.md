# WAL recovery preparation for #121

Production promotion, restart and database mutation require separate owner approval. This plan
does not authorize them. Phase 0 remains FAIL; any eventual smoke result covers trusted-user testing.

## Lock-loss reproduction

`secureFiles` used ordinary file descriptors after SQLite opened the database. Closing any ordinary
descriptor for the DB or SHM inode releases the process's POSIX locks on that inode. The dedicated
directory's `flock` does not replace SQLite's byte-range locks or coordinate native SQLite clients.
An external client can then mistake itself for the last connection and remove sidecars still held
by Antares. The production unlinking process was not traced; the lock-loss mechanism is reproduced
on disposable databases by these tests:

```sh
go test ./internal/store -run 'TestSecureFilesPreservesPOSIXLocks|TestOpenPreservesLocksAcrossExternalSQLite' -count=1 -v
```

A subprocess probes the parent's locks with `F_GETLK` before and after hardening. The SQLite test
also checks locks after `Open`, repeated hardening and a second same-process `Open`; external
SQLite commits remain visible and leave WAL/SHM inodes intact. Permission, symlink and non-regular
file tests remain required. `O_PATH` pins the chmod target without releasing SQLite's locks when
closed; `/proc/self/fd` keeps chmod tied to the validated inode. No SQLite version change is needed.

## Prepared capture and candidate

The protected source capture is `/var/backups/antares/wal-preservation-20261024T002356Z`.
Its manifest records both paths to the same DB inode, the named WAL/SHM, and the deleted WAL/SHM
copied through pinned process handles while Antares was paused. Preserve the raw originals, full
WAL including tails, manifests and checksums. Work in separate 0700 directories with files 0600.

For each source set, validate the WAL header, salts, rolling frame checksums, commit boundaries
and any incomplete tail. Reconstruct the last committed view only on copies. Do not discard tails
or treat their contents as committed. Validate integrity, foreign keys, complete schema and
migration checksums. Compare every table by primary key and full typed row contents, including
empty tables, audit records, account states, encrypted bindings and session revocations. Keep
values out of reports. Confirm the WAL reconstruction with an independent SQLite reader on copies.

The captured named view contains the committed union: all 16 tables were compared, 15 are
identical, and only five additional session rows occur in the named view. Shared rows, including
12 revoked sessions, agree. The deleted WAL has 79 valid frames across 22 commits and no tail.
This supports a byte-for-byte named-view candidate for this capture; it does not establish that
the capture contains subsequent production writes or every historical transaction.

Do not use `antares restore` for this incident: that command clears sessions. Do not attach either
old WAL to the live DB, checkpoint it, drop sidecars, restore the earlier backup, or rotate keys.

## Eventual cutover gates

1. Obtain approval for a maintenance window, fresh capture, bounded write freeze, candidate
   installation and an exact reviewed fixed image/helper SHA. Prepare a retention location for
   every current file set. Do not deploy a moving main head or assume the old approved SHA fixes locks.
2. Before closing any old connection, refresh and pin its actual DB/WAL/SHM handles. With all
   writers frozen, capture named and process-held files consistently again, including any new
   inodes or WAL tails. Validate and checksum the capture. Keep every original set.
3. Rebuild both committed views from that fresh capture. Repeat all-table, schema, FK, integrity
   and revocation comparisons. If the old candidate differs at all, invalidate it and prepare a
   new candidate from the fresh union. Do not resume writes between final capture and cutover.
4. Stop for explicit resolution of any conflicting row, missing record, revoke/reactivation
   disagreement, changed schema or invalid WAL. Never choose a version by timestamp alone,
   resurrect a revoked session, silently omit expired sessions, or discard unrelated data.
5. Before installation, recheck container identity, source inodes, sizes and checksums against
   the frozen capture. Any drift invalidates the candidate. Keep the write fence in effect while
   separately approved service shutdown and installation occur; shutdown may checkpoint or
   remove sidecars, so retain and compare the post-shutdown set too. Do not let that overwrite
   the only preserved copy. The install method must preserve sessions and retain all displaced files.
6. Start only the approved fixed revision, verify helper revision, restrictive modes, kernel
   SQLite locks and matching named/held inodes, then health/readiness and the full smoke gate.
   Smoke rotations and suspension apply only to identified smoke accounts with current approval.
   A failed gate leaves the new image and preserved data in place; propose a scoped forward fix.

Preparation ends before step 1. A future approval must name the candidate derived from the fresh
capture, the image/helper SHA, retention paths, maximum downtime and the permitted cutover actions.
