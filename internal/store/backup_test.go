package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func countRows(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func addMessage(t *testing.T, db *sql.DB, user, chat, id, role string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO messages (id, user_id, chat_id, role, hermes_message_id, created_at) VALUES (?,?,?,?,?,?)`,
		id, user, chat, role, "h-"+id, ts(t0)); err != nil {
		t.Fatal(err)
	}
}

func migrationSet(t *testing.T) Set {
	t.Helper()
	set, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// The full loop the operator relies on: data -> backup -> destroy -> restore.
func TestBackupRestoreRoundTripKeepsChatAndMessages(t *testing.T) {
	ctx := context.Background()
	d, u := setupTwo(t)
	db := d.DB
	addMessage(t, db, u.a, u.chatA.ID, "m1", "user")
	addMessage(t, db, u.a, u.chatA.ID, "m2", "assistant")
	livePath := dbFile(t, db)

	bak := filepath.Join(t.TempDir(), "antares.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(bak); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode: %v %v", fi, err)
	}
	if err := Backup(ctx, db, bak); err == nil {
		t.Fatal("backup overwrote an existing file")
	}

	// Destroy: lose everything, then also leave a stale WAL behind.
	db.Close()
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.Remove(livePath + suf)
	}
	if err := os.WriteFile(livePath+"-wal", []byte("stale junk that must not be replayed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Restore(ctx, bak, livePath, migrationSet(t), RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	re, err := Open(livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	if _, err := Migrate(ctx, re, Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := SchemaCurrent(ctx, re, migrationSet(t)); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, re, `SELECT COUNT(*) FROM users`); n != 2 {
		t.Errorf("users = %d", n)
	}
	if n := countRows(t, re, `SELECT COUNT(*) FROM tenant_bindings`); n != 2 {
		t.Errorf("bindings = %d", n)
	}
	got, err := GetChat(ctx, re, u.a, u.chatA.ID)
	if err != nil || got.Title != "A's chat" {
		t.Fatalf("chat: %+v %v", got, err)
	}
	if n := countRows(t, re, `SELECT COUNT(*) FROM messages WHERE chat_id = '`+u.chatA.ID+`'`); n != 2 {
		t.Errorf("messages = %d", n)
	}
	rep, err := Check(ctx, livePath)
	if err != nil || !rep.OK() {
		t.Errorf("restored db check: %+v %v", rep, err)
	}
}

func dbFile(t *testing.T, db *sql.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// DoD: a backup taken while a writer is committing passes integrity_check and
// holds the latest row committed before the backup began.
func TestBackupDuringActiveWriter(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	user := mkUser(t, db, "w", "USER", "APPROVED", "READY")
	bind := mkBinding(t, db, user, "u_wwwwwwwwww", "READY")
	chat, err := CreateChat(ctx, db, user, bind, "sess-w", "w", t0)
	if err != nil {
		t.Fatal(err)
	}
	var written atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Exec(`INSERT INTO messages (id, user_id, chat_id, role, created_at) VALUES (?,?,?,?,?)`,
				fmt.Sprintf("w%06d", i), user, chat.ID, "user", ts(t0)); err != nil {
				t.Error(err)
				return
			}
			written.Store(int64(i))
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for written.Load() < 50 {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatal("writer made no progress")
		}
		time.Sleep(time.Millisecond)
	}
	floor := written.Load() // committed before the backup starts
	bak := filepath.Join(t.TempDir(), "live.bak")
	err = Backup(ctx, db, bak)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, bak, migrationSet(t)); err != nil {
		t.Fatal(err)
	}
	b, err := openRO(ctx, bak)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if n := countRows(t, b, `SELECT COUNT(*) FROM messages`); int64(n) < floor {
		t.Errorf("backup has %d messages, %d were committed before it started", n, floor)
	}
	var last string
	if err := b.QueryRow(`SELECT id FROM messages WHERE id = ?`, fmt.Sprintf("w%06d", floor)).Scan(&last); err != nil {
		t.Errorf("latest committed row missing: %v", err)
	}
}

func TestBackupToStreamsAVerifiableSnapshot(t *testing.T) {
	ctx := context.Background()
	db, p := migrated(t)
	var buf bytes.Buffer
	if err := BackupTo(ctx, db, p, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + BackupTempSuffix); err == nil {
		t.Error("staging file left behind")
	}
	out := filepath.Join(t.TempDir(), "s.bak")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(ctx, out, migrationSet(t)); err != nil {
		t.Fatal(err)
	}
	// The data dir must still be acceptable to Open (dedicated-directory rule).
	db.Close()
	if re, err := Open(p); err != nil {
		t.Fatal(err)
	} else {
		re.Close()
	}
}

// DoD: garbage, truncated and foreign files are refused and the live database
// is byte-for-byte untouched.
func TestRestoreRefusesBadBackupsAndLeavesLiveUntouched(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	user := mkUser(t, db, "keep", "USER", "APPROVED", "READY")
	good := filepath.Join(t.TempDir(), "good.bak")
	if err := Backup(ctx, db, good); err != nil {
		t.Fatal(err)
	}
	goodBytes, _ := os.ReadFile(good)
	// Make the live main file a valid database on its own, then commit one more
	// row that exists only in the WAL: restoring the live file onto itself would
	// verify fine and then silently drop that row.
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	walOnly := mkUser(t, db, "wal-only", "USER", "APPROVED", "READY")

	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// A valid SQLite file that is not Antares'.
	foreign := filepath.Join(dir, "foreign.db")
	f, err := sql.Open("sqlite", "file:"+foreign)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(`CREATE TABLE t (x)`)
	f.Close()
	// An Antares file claiming a migration this binary does not have.
	future := filepath.Join(dir, "future.db")
	copyFile(good, future, 0o600)
	fd, _ := sql.Open("sqlite", "file:"+future)
	if _, err := fd.Exec(`INSERT INTO schema_migrations VALUES (9999,'9999_x','abc','now')`); err != nil {
		t.Fatal(err)
	}
	fd.Close()

	cases := map[string]string{
		"garbage":   write("garbage.db", bytes.Repeat([]byte("not a database "), 500)),
		"truncated": write("trunc.db", goodBytes[:len(goodBytes)/2]),
		"empty":     write("empty.db", nil),
		"foreign":   foreign,
		"future":    future,
		"missing":   filepath.Join(dir, "nope.db"),
		"same file": live,
		"same via symlink": func() string {
			l := filepath.Join(dir, "link.db")
			if err := os.Symlink(live, l); err != nil {
				t.Fatal(err)
			}
			return l
		}(),
	}
	before := snapshotFiles(t, live)
	for name, src := range cases {
		err := Restore(ctx, src, live, migrationSet(t), RestoreOptions{})
		if err == nil {
			t.Errorf("%s: restore accepted it", name)
			continue
		}
		t.Logf("%s: %v", name, err)
		if after := snapshotFiles(t, live); after != before {
			t.Errorf("%s: live database files changed", name)
		}
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id IN ('`+user+`','`+walOnly+`')`); n != 2 {
		t.Error("live data changed")
	}
	if _, err := os.Stat(live + RestoreTempSuffix); err == nil {
		t.Error("staging file left behind")
	}
}

func snapshotFiles(t *testing.T, live string) string {
	t.Helper()
	var sb strings.Builder
	for _, suf := range []string{"", "-wal", "-shm"} {
		b, _ := os.ReadFile(live + suf)
		fmt.Fprintf(&sb, "%s:%d:%x|", suf, len(b), b)
	}
	return sb.String()
}

// A bit-flipped page inside an otherwise openable file must be caught by
// integrity_check, reported honestly, and refused by restore.
func TestCheckReportsCorruptionAndRestoreRefusesIt(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	user := mkUser(t, db, "c", "USER", "APPROVED", "READY")
	bind := mkBinding(t, db, user, "u_cccccccccc", "READY")
	chat, _ := CreateChat(ctx, db, user, bind, "s", "c", t0)
	for i := 0; i < 400; i++ {
		addMessage(t, db, user, chat.ID, fmt.Sprintf("c%04d", i), "user")
	}
	bak := filepath.Join(t.TempDir(), "c.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	if rep, err := Check(ctx, bak); err != nil || !rep.OK() {
		t.Fatalf("healthy backup: %+v %v", rep, err)
	}
	b, _ := os.ReadFile(bak)
	// Zero out a page in the middle, leaving the header and schema readable.
	for i := len(b) / 2; i < len(b)/2+4096 && i < len(b); i++ {
		b[i] = 0xFF
	}
	bad := filepath.Join(t.TempDir(), "bad.db")
	os.WriteFile(bad, b, 0o600)
	rep, err := Check(ctx, bad)
	if err != nil || rep.OK() || len(rep.Integrity) == 0 {
		t.Fatalf("corruption must be a non-OK report, not an error: %+v %v", rep, err)
	}
	t.Logf("check: %+v err=%v", rep, err)
	if err := VerifyBackup(ctx, bad, migrationSet(t)); err == nil {
		t.Fatal("corrupt file verified")
	}
}

func TestRestoreKeepsOldDatabaseAndRefusesWhileInUse(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	u1 := mkUser(t, db, "before", "USER", "APPROVED", "READY")
	bak := filepath.Join(t.TempDir(), "b.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	u2 := mkUser(t, db, "after", "USER", "APPROVED", "READY") // not in the backup
	keep := filepath.Join(t.TempDir(), "old")

	// An open write transaction in another connection = a running service.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE users SET email = email WHERE id = ?`, u1); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{KeepOldDir: keep}); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("restore with active writer: %v", err)
	}
	tx.Rollback()
	db.Close()

	if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{KeepOldDir: keep}); err != nil {
		t.Fatal(err)
	}
	re, err := Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	if countRows(t, re, `SELECT COUNT(*) FROM users WHERE id='`+u1+`'`) != 1 || countRows(t, re, `SELECT COUNT(*) FROM users WHERE id='`+u2+`'`) != 0 {
		t.Error("restored content is not the backup's")
	}
	ents, _ := os.ReadDir(keep)
	if len(ents) == 0 {
		t.Fatal("old database not kept")
	}
	kept := filepath.Join(keep, ents[0].Name())
	if kdb, err := openRO(ctx, kept); err != nil {
		t.Fatal(err)
	} else {
		defer kdb.Close()
		if countRows(t, kdb, `SELECT COUNT(*) FROM users WHERE id='`+u2+`'`) != 1 {
			t.Error("kept copy lacks the pre-restore row")
		}
	}
}

// A crash mid-backup or mid-restore must never make the next start refuse the
// data directory (the exact names the code itself stages and swaps).
func TestOpenToleratesCrashResidue(t *testing.T) {
	db, p := migrated(t)
	db.Close()
	for _, n := range []string{BackupTempSuffix, BackupTempSuffix + "-journal",
		RestoreTempSuffix, RestoreTempSuffix + ".old", RestoreTempSuffix + ".old-wal"} {
		if err := os.WriteFile(p+n, []byte("residue"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	re, err := Open(p)
	if err != nil {
		t.Fatalf("Open refused crash residue: %v", err)
	}
	re.Close()
	// A symlink wearing a residue name is not residue either.
	linkName := p + BackupTempSuffix + "-wal"
	os.Remove(linkName)
	if err := os.Symlink("/etc/passwd", linkName); err != nil {
		t.Fatal(err)
	}
	if re, err := Open(p); err == nil {
		re.Close()
		t.Fatal("Open accepted a symlink named like residue")
	}
	os.Remove(linkName)
	// A name that merely starts like residue is not residue.
	loose := p + BackupTempSuffix + "-anything"
	os.WriteFile(loose, nil, 0o600)
	if re, err := Open(p); err == nil {
		re.Close()
		t.Fatal("Open accepted a prefix-only match")
	}
	os.Remove(loose)
	// A directory wearing a residue name is not residue.
	dirName := p + RestoreTempSuffix + ".old"
	os.Remove(dirName)
	if err := os.Mkdir(dirName, 0o700); err != nil {
		t.Fatal(err)
	}
	if re, err := Open(p); err == nil {
		re.Close()
		t.Fatal("Open accepted a directory named like residue")
	}
	os.Remove(dirName)
	// A stranger is still refused.
	os.WriteFile(filepath.Join(filepath.Dir(p), "other.txt"), nil, 0o600)
	if re, err := Open(p); err == nil {
		re.Close()
		t.Fatal("Open accepted an unrelated file")
	}
}

// Backup must publish to a different filesystem than the data directory.
func TestBackupToAnotherFilesystem(t *testing.T) {
	ctx := context.Background()
	db, p := migrated(t)
	other, err := os.MkdirTemp("/dev/shm", "antares-bak")
	if err != nil {
		t.Skipf("no second filesystem available: %v", err)
	}
	defer os.RemoveAll(other)
	var a, b os.FileInfo
	if a, err = os.Stat(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	b, _ = os.Stat(other)
	if sameDev(a, b) {
		t.Skip("/dev/shm is on the same device as the temp dir")
	}
	dest := filepath.Join(other, "x.bak")
	if err := Backup(ctx, db, dest); err != nil {
		t.Fatalf("cross-device backup: %v", err)
	}
	if err := VerifyBackup(ctx, dest, migrationSet(t)); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(other); len(ents) != 1 {
		t.Errorf("leftovers: %v", ents)
	}
}

func sameDev(a, b os.FileInfo) bool {
	return a.Sys().(*syscall.Stat_t).Dev == b.Sys().(*syscall.Stat_t).Dev
}

// A wrong ANTARES_DB_PATH must not produce a valid, empty, "verified" backup.
func TestOpenForBackupRefusesMissingOrUninitialised(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	missing := filepath.Join(dir, "typo", "antares.sqlite")
	if _, err := OpenForBackup(ctx, missing); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(filepath.Dir(missing)); err == nil {
		t.Error("OpenForBackup created the directory")
	}
	empty := filepath.Join(dir, "empty", "antares.sqlite")
	db, err := Open(empty) // what a mistyped path used to produce
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := OpenForBackup(ctx, empty); err == nil {
		t.Fatal("uninitialised database accepted")
	}
	good, gp := migrated(t)
	good.Close()
	g, err := OpenForBackup(ctx, gp)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
}

func TestRestoreIdleProbe(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	bak := filepath.Join(t.TempDir(), "b.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Unprovable idleness (permission error) refuses and touches nothing.
	if os.Geteuid() != 0 {
		before := snapshotFiles(t, live)
		os.Chmod(live, 0)
		err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{})
		os.Chmod(live, 0o600)
		if err == nil {
			t.Fatal("restore passed the in-use gate on an unreadable live file")
		}
		if snapshotFiles(t, live) != before {
			t.Error("live files changed")
		}
	}
	// A corrupt live file is exactly what restore is for.
	if err := os.WriteFile(live, bytes.Repeat([]byte("corrupt!"), 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{}); err != nil {
		t.Fatalf("restore over a corrupt live file: %v", err)
	}
	if err := VerifyBackup(ctx, live, migrationSet(t)); err != nil {
		t.Fatal(err)
	}
}

// If swapping the restored file in fails, the live database and its WAL are
// exactly as they were.
func TestRestoreFailureLeavesLiveFilesIntact(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	bak := filepath.Join(t.TempDir(), "b.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	db.Close()
	os.WriteFile(live, bytes.Repeat([]byte("main"), 100), 0o600)
	os.WriteFile(live+"-shm", []byte("shared memory"), 0o600)
	// Make moving a sidecar aside fail after the main file already moved.
	block := live + RestoreTempSuffix + ".old-shm"
	os.MkdirAll(filepath.Join(block, "x"), 0o700)
	before := snapshotFiles(t, live)
	if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{}); err == nil {
		t.Fatal("restore succeeded despite a blocked swap")
	}
	if snapshotFiles(t, live) != before {
		t.Error("live files not restored after failed swap")
	}
}

func TestRestoreTwiceInOneSecondKeepsBothCopies(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	bak := filepath.Join(t.TempDir(), "b.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	db.Close()
	keep := filepath.Join(t.TempDir(), "old")
	for i := 0; i < 2; i++ {
		if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{KeepOldDir: keep}); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
	}
	if ents, _ := os.ReadDir(keep); len(ents) != 2 {
		t.Errorf("kept %d copies, want 2", len(ents))
	}
}

// A running service with an idle pool (no open transaction) must still block
// restore: otherwise it keeps serving from an unlinked file.
func TestRestoreRefusesWhileDatabaseIsOpenButIdle(t *testing.T) {
	ctx := context.Background()
	db, live := migrated(t)
	user := mkUser(t, db, "idle", "USER", "APPROVED", "READY")
	bak := filepath.Join(t.TempDir(), "b.bak")
	if err := Backup(ctx, db, bak); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err != nil { // pool holds a live connection, no transaction
		t.Fatal(err)
	}
	before := snapshotFiles(t, live)
	err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("restore with an idle open pool: %v", err)
	}
	if snapshotFiles(t, live) != before {
		t.Error("live files changed")
	}
	if _, err := os.Stat(live + RestoreTempSuffix + ".old"); err == nil {
		t.Error("live file was moved aside")
	}
	// The service keeps working, and once it is closed the restore goes through.
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id='`+user+`'`); n != 1 {
		t.Error("live data changed")
	}
	db.Close()
	if err := Restore(ctx, bak, live, migrationSet(t), RestoreOptions{}); err != nil {
		t.Fatalf("restore after close: %v", err)
	}
}

// While a restore holds the exclusive lock, Open refuses instead of attaching
// to the file that is about to be replaced.
func TestOpenRefusesDuringRestoreLock(t *testing.T) {
	db, live := migrated(t)
	db.Close()
	lock, err := flockFile(filepath.Dir(live), syscall.LOCK_EX)
	if err != nil {
		t.Fatal(err)
	}
	if re, err := Open(live); err == nil {
		re.Close()
		t.Fatal("Open succeeded under an exclusive lock")
	}
	lock.Close()
	re, err := Open(live)
	if err != nil {
		t.Fatal(err)
	}
	re.Close()
}

func TestSameFileSurfacesStatErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can stat everything")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "d"), 0o700)
	os.WriteFile(filepath.Join(dir, "d", "f"), nil, 0o600)
	os.Chmod(filepath.Join(dir, "d"), 0)
	defer os.Chmod(filepath.Join(dir, "d"), 0o700)
	if _, err := sameFile(filepath.Join(dir, "d", "f"), filepath.Join(dir, "other")); err == nil {
		t.Error("stat permission error swallowed")
	}
	if same, err := sameFile(filepath.Join(dir, "missing"), filepath.Join(dir, "other")); same || err != nil {
		t.Errorf("missing source: %v %v", same, err)
	}
}

// /data is the only writable path in the container, so a backup written there
// is the reachable mistake: it must be refused, and Open must keep working.
func TestBackupRefusesDestinationInDataDir(t *testing.T) {
	ctx := context.Background()
	db, p := migrated(t)
	for _, dest := range []string{p + ".bak", filepath.Join(filepath.Dir(p), "other.bak")} {
		err := Backup(ctx, db, dest)
		if err == nil || !strings.Contains(err.Error(), "outside the data directory") {
			t.Fatalf("Backup to %s: %v", dest, err)
		}
		if _, serr := os.Stat(dest); serr == nil {
			t.Errorf("%s was written", dest)
		}
	}
	// Through a symlinked directory too.
	link := filepath.Join(t.TempDir(), "via")
	if err := os.Symlink(filepath.Dir(p), link); err != nil {
		t.Fatal(err)
	}
	if err := Backup(ctx, db, filepath.Join(link, "x.bak")); err == nil {
		t.Error("Backup through a symlink to the data dir was accepted")
	}
	db.Close()
	re, err := Open(p)
	if err != nil {
		t.Fatalf("Open after refused backups: %v", err)
	}
	re.Close()
}

// The swap window: restore has moved the live file aside and not yet renamed
// the new one in. A start-up then must not create an empty database under the
// live name (it would be orphaned when the rename lands).
func TestOpenCannotCreateDuringSwapWindow(t *testing.T) {
	db, live := migrated(t)
	db.Close()
	lock, err := flockFile(filepath.Dir(live), syscall.LOCK_EX) // what Restore holds
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, suf := range dbSuffixes {
		os.Rename(live+suf, live+RestoreTempSuffix+".old"+suf) // the aside step
	}
	if re, err := Open(live); err == nil {
		re.Close()
		t.Fatal("Open succeeded inside the swap window")
	}
	if _, err := os.Stat(live); err == nil {
		t.Fatal("Open created the database file inside the swap window")
	}
}

// 'check' must not report on a different file than the one named.
func TestCheckRefusesDriverURICharactersAndProvesThePath(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "name.sqlite")
	if err := Backup(ctx, db, good); err != nil {
		t.Fatal(err)
	}
	// A real file whose NAME contains a URI character, next to a valid database
	// that the driver would read instead: "name?x.sqlite" -> "name" + parameters,
	// "dot%2Esqlite" -> "dot.sqlite". The operator named the garbage file; an "ok"
	// would be about the other one.
	os.Rename(good, filepath.Join(dir, "name"))
	copyFile(filepath.Join(dir, "name"), filepath.Join(dir, "dot.sqlite"), 0o600)
	for _, bad := range []string{"name?x.sqlite", "name#x.sqlite", "dot%2Esqlite"} {
		p := filepath.Join(dir, bad)
		if err := os.WriteFile(p, bytes.Repeat([]byte("garbage "), 600), 0o600); err != nil {
			t.Fatal(err)
		}
		if rep, err := Check(ctx, p); err == nil {
			t.Errorf("Check verified something for %q: %+v", bad, rep)
		}
		if err := VerifyBackup(ctx, p, migrationSet(t)); err == nil {
			t.Errorf("VerifyBackup accepted %q", bad)
		}
	}
	if rep, err := Check(ctx, filepath.Join(dir, "name")); err != nil || !rep.OK() {
		t.Errorf("valid path: %+v %v", rep, err)
	}
	// Through a symlink it still checks the target.
	link := filepath.Join(dir, "link")
	os.Symlink(filepath.Join(dir, "name"), link)
	if rep, err := Check(ctx, link); err != nil || !rep.OK() {
		t.Errorf("symlinked path: %+v %v", rep, err)
	}
}
