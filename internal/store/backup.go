package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"modernc.org/sqlite"
)

// BackupTempSuffix names the staging file a streamed backup uses next to the
// database. It is the one extra file requireDedicated tolerates.
const BackupTempSuffix = ".backup-tmp"

// RestoreTempSuffix names the staging file Restore uses next to the database.
const RestoreTempSuffix = ".restore-tmp"

// dsnPath refuses paths the driver would reinterpret when they are placed in a
// "file:" URI ('?' starts parameters, '#' a fragment, '%' an escape), as Open does.
func dsnPath(path string) error {
	if path == "" || strings.ContainsAny(path, "?#%") {
		return fmt.Errorf("invalid database path %q: '?', '#' and '%%' are not allowed", path)
	}
	return nil
}

// provesPath checks, through PRAGMA database_list, that the driver opened the
// file the caller named and not whatever a rewritten URI resolved to.
func provesPath(ctx context.Context, db *sql.DB, path string) error {
	var seq int
	var schema, opened string
	if err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &schema, &opened); err != nil {
		return fmt.Errorf("read database list: %w", err)
	}
	want, err1 := filepath.EvalSymlinks(path)
	got, err2 := filepath.EvalSymlinks(opened)
	if err1 != nil || err2 != nil || want != got {
		return errors.New("driver opened a different file than the one named")
	}
	return nil
}

// openRO opens an existing SQLite file read-only through the driver. It never
// creates the file, and never applies the WAL/pragma setup Open does.
func openRO(ctx context.Context, path string) (*sql.DB, error) {
	if err := dsnPath(path); err != nil {
		return nil, err
	}
	// mode=ro would also fail on a missing file, but with only "unable to open
	// database file (14)"; the stat gives the operator the real reason.
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := provesPath(ctx, db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Report is the outcome of Check.
type Report struct {
	Integrity  []string // lines from PRAGMA integrity_check; exactly ["ok"] when healthy
	ForeignKey []string // violations from PRAGMA foreign_key_check, if any
	// Truncated is set when either list hit its 50-line cap, so more problems
	// exist than are listed.
	Truncated bool
}

// OK reports whether the database is intact. integrity_check(50) returns the
// single row "ok" when healthy and otherwise one row per problem, so exactly
// ["ok"] is the only healthy answer.
func (r Report) OK() bool {
	return len(r.Integrity) == 1 && r.Integrity[0] == "ok" && len(r.ForeignKey) == 0
}

// Check runs PRAGMA integrity_check and foreign_key_check on the file at path
// and reports what SQLite said. A file SQLite cannot read at all is an error;
// one that opens but is damaged is a Report whose OK is false. At most 50
// problems of each kind are listed; Truncated says when that cap was hit.
func Check(ctx context.Context, path string) (Report, error) {
	db, err := openRO(ctx, path)
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	var r Report
	if r.Integrity, err = pragmaLines(ctx, db, `PRAGMA integrity_check(50)`); err != nil {
		if damaged(err) {
			// SQLite could not even walk the file: that is the finding, not a
			// failure to run the check.
			return Report{Integrity: []string{err.Error()}}, nil
		}
		return Report{}, fmt.Errorf("integrity_check: %w", err)
	}
	r.Truncated = len(r.Integrity) >= 50
	if !r.OK() {
		return r, nil // table walks below are unreliable on a damaged file
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return Report{}, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(r.ForeignKey) == 50 {
			r.Truncated = true
			break
		}
		var table, parent sql.NullString
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return Report{}, err
		}
		r.ForeignKey = append(r.ForeignKey, fmt.Sprintf("table %s rowid %d references missing row in %s", table.String, rowid.Int64, parent.String))
	}
	return r, rows.Err()
}

// damaged reports whether err is SQLite saying the file is corrupt (11) or not
// a database at all (26).
func damaged(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code() & 0xff
		return c == 11 || c == 26
	}
	return false
}

func pragmaLines(ctx context.Context, db *sql.DB, q string) ([]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// VerifyBackup proves path is an intact Antares database this binary can run:
// SQLite reads it, integrity_check passes, and every migration it records is
// one this binary embeds with the same checksum. An older schema is accepted
// (startup migrates it forward); a newer or foreign one is refused.
func VerifyBackup(ctx context.Context, path string, set Set) error {
	rep, err := Check(ctx, path)
	if err != nil {
		return fmt.Errorf("not a readable SQLite database: %w", err)
	}
	if len(rep.Integrity) != 1 || rep.Integrity[0] != "ok" {
		return fmt.Errorf("integrity_check failed: %s", strings.Join(rep.Integrity, "; "))
	}
	if len(rep.ForeignKey) > 0 {
		return fmt.Errorf("foreign_key_check failed: %s", strings.Join(rep.ForeignKey, "; "))
	}
	db, err := openRO(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("not an Antares database (no schema_migrations): %w", err)
	}
	defer rows.Close()
	want := map[int]migration{}
	for _, m := range set {
		want[m.version] = m
	}
	n := 0
	for rows.Next() {
		var v int
		var name, sum string
		if err := rows.Scan(&v, &name, &sum); err != nil {
			return err
		}
		m, ok := want[v]
		switch {
		case !ok:
			return fmt.Errorf("wrong schema: backup has migration %d unknown to this binary (taken by a newer version?)", v)
		case m.checksum != sum || m.name != name:
			return fmt.Errorf("wrong schema: backup migration %d differs from this binary's", v)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return errors.New("not an Antares database: schema_migrations is empty")
	}
	return nil
}

// Backup writes a consistent snapshot of the live database db to dest using
// VACUUM INTO, which reads inside one transaction and so is safe beside active
// writers and WAL (a plain file copy is not). dest must not exist. The snapshot
// is staged beside dest under a temporary name, checked with integrity_check,
// set to 0600 and only then published, so dest is either a verified backup or
// absent.
func Backup(ctx context.Context, db *sql.DB, dest string) error {
	if !filepath.IsAbs(dest) {
		return errors.New("backup: destination must be an absolute path")
	}
	if inLiveDir(db, dest) {
		return errors.New("backup: the destination is inside the database's own data directory, which must hold only " +
			"the database files (antares would refuse to start); write it outside the data directory, " +
			"or stream it with `antares backup -`")
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("backup: %s already exists; refusing to overwrite", dest)
	}
	tmp := dest + ".partial"
	defer os.Remove(tmp)
	if err := snapshot(ctx, db, tmp); err != nil {
		return err
	}
	// tmp is in dest's directory, so this never crosses a filesystem. Link
	// cannot replace a dest created meanwhile; where the filesystem has no hard
	// links, fall back to rename after re-checking dest is still absent.
	if err := os.Link(tmp, dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("backup: %s already exists; refusing to overwrite", dest)
		}
		if _, lerr := os.Lstat(dest); lerr == nil {
			return fmt.Errorf("backup: %s already exists; refusing to overwrite", dest)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	// Without this the "verified" file can exist only in the page cache.
	if err := syncDir(filepath.Dir(dest)); err != nil {
		return fmt.Errorf("backup: the verified backup HAS been written to %s, but syncing its directory failed (%w); "+
			"it may not survive a crash: run `antares check` on it, or take another", dest, err)
	}
	return nil
}

// inLiveDir reports whether dest would be created in the directory that holds
// db's main database file. If either side cannot be resolved it answers true:
// the refusal is the safe side.
func inLiveDir(db *sql.DB, dest string) bool {
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil || file == "" {
		return true
	}
	live, err1 := filepath.EvalSymlinks(filepath.Dir(file))
	out, err2 := filepath.EvalSymlinks(filepath.Dir(dest))
	if err1 != nil || err2 != nil {
		return err1 != nil
	}
	return live == out
}

// snapshot writes a verified, fsynced 0600 VACUUM INTO copy of db at path,
// which must not exist.
func snapshot(ctx context.Context, db *sql.DB, path string) error {
	os.Remove(path) // residue of an earlier crash
	// The path is a bind parameter; VACUUM INTO accepts an expression.
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	rep, err := Check(ctx, path)
	if err != nil {
		return fmt.Errorf("backup: verify snapshot: %w", err)
	}
	if !rep.OK() {
		return fmt.Errorf("backup: snapshot failed integrity_check: %s", strings.Join(append(rep.Integrity, rep.ForeignKey...), "; "))
	}
	return syncFile(path)
}

// BackupTo streams a verified snapshot of db to w, staging it in the database's
// own directory (the only writable place in the read-only container). The
// stage is a single file named dbPath+BackupTempSuffix; a crash can leave only
// that name (or a SQLite sidecar of it), which Open tolerates.
func BackupTo(ctx context.Context, db *sql.DB, dbPath string, w io.Writer) error {
	stage := dbPath + BackupTempSuffix
	defer os.Remove(stage)
	if err := snapshot(ctx, db, stage); err != nil {
		return err
	}
	f, err := os.Open(stage)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// OpenForBackup opens the live database for backup. Unlike Open it never
// creates anything: a mistyped ANTARES_DB_PATH or an unmounted volume must fail,
// not yield a valid but empty snapshot that "verifies". The file must exist and
// already record migrations.
func OpenForBackup(ctx context.Context, path string) (*sql.DB, error) {
	if fi, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("backup: database %s: %w", path, err)
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("backup: %s is not a regular file", path)
	}
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		db.Close()
		if err == nil {
			err = errors.New("schema_migrations is empty")
		}
		return nil, fmt.Errorf("backup: %s is not an initialised Antares database (wrong ANTARES_DB_PATH?): %w", path, err)
	}
	return db, nil
}

// RestoreOptions control Restore.
type RestoreOptions struct {
	// KeepOldDir receives copies of the live database files before they are
	// replaced. Empty means the caller has chosen to discard them.
	KeepOldDir string
}

// Restore replaces the database at livePath with the backup at src. The
// service must be stopped. Order matters, and nothing live is touched until
// every check has passed:
//  1. src is verified (VerifyBackup);
//  2. src is copied beside the live file and the copy is verified again;
//  3. an active writer on the live file aborts the restore;
//  4. the live files are copied to KeepOldDir, if set;
//  5. the live -wal/-shm/-journal are removed (a stale WAL replayed onto the
//     restored file would corrupt it) and the verified copy is renamed over it.
func Restore(ctx context.Context, src, livePath string, set Set, opt RestoreOptions) error {
	if !filepath.IsAbs(livePath) {
		return errors.New("restore: database path must be absolute")
	}
	// Restoring the live file onto itself would stage a plain copy without its
	// WAL, then move the WAL aside: committed, uncheckpointed rows would vanish.
	if same, err := sameFile(src, livePath); err != nil {
		return fmt.Errorf("restore: cannot inspect the backup file or the database: %w", err)
	} else if same {
		return errors.New("restore: source and destination are the same file")
	}
	if err := VerifyBackup(ctx, src, set); err != nil {
		return fmt.Errorf("restore: refusing %s: %w", src, err)
	}
	dir := filepath.Dir(livePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	staged := livePath + RestoreTempSuffix
	os.Remove(staged)
	defer os.Remove(staged)
	if err := copyFile(src, staged, 0o600); err != nil {
		return fmt.Errorf("restore: stage copy: %w", err)
	}
	if err := VerifyBackup(ctx, staged, set); err != nil {
		return fmt.Errorf("restore: staged copy failed verification: %w", err)
	}
	release, err := requireIdle(ctx, livePath)
	if err != nil {
		return err
	}
	defer release() // held through the swap: nothing can Open the old inode meanwhile
	if opt.KeepOldDir != "" {
		if filepath.Clean(opt.KeepOldDir) == filepath.Clean(dir) {
			return errors.New("restore: the keep-old directory must not be the data directory")
		}
		if err := os.MkdirAll(opt.KeepOldDir, 0o700); err != nil {
			return err
		}
		stamp := time.Now().UTC().Format("20060102T150405.000000Z")
		for _, suf := range dbSuffixes {
			err := copyFile(livePath+suf, filepath.Join(opt.KeepOldDir, filepath.Base(livePath)+suf+"."+stamp), 0o600)
			if err != nil && !errors.Is(err, os.ErrNotExist) { // absent live files (fresh volume) have nothing to keep
				return fmt.Errorf("restore: keep old %s: %w", filepath.Base(livePath)+suf, err)
			}
		}
	}
	// Swap, never delete first: the live files (WAL included) are renamed aside
	// and only removed once the restored file is in place, so a failed rename
	// puts every committed row back. The WAL must not stay under the live name:
	// replayed onto the restored file it would corrupt it.
	aside := livePath + RestoreTempSuffix + ".old"
	var moved []string
	// undo puts the moved files back; a file that cannot be put back is named,
	// because it is then still under its ".old" name and Open would start empty.
	undo := func() string {
		var stuck []string
		for _, suf := range moved {
			if err := os.Rename(aside+suf, livePath+suf); err != nil {
				stuck = append(stuck, fmt.Sprintf("%s (%v)", aside+suf, err))
			}
		}
		if len(stuck) > 0 {
			return "; COULD NOT put back, rename by hand before starting antares: " + strings.Join(stuck, ", ")
		}
		return ""
	}
	for _, suf := range dbSuffixes {
		if err := os.Rename(livePath+suf, aside+suf); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("restore: move %s aside: %w%s", filepath.Base(livePath)+suf, err, undo())
		}
		moved = append(moved, suf)
	}
	if err := os.Rename(staged, livePath); err != nil {
		if left := undo(); left != "" {
			return fmt.Errorf("restore: %w%s", err, left)
		}
		return fmt.Errorf("restore: %w (live database left as it was)", err)
	}
	// The restored file is in place from here on. Do the cleanup whatever the
	// directory sync says, then report everything that did not go cleanly.
	syncErr := syncDir(dir)
	var leftovers []string
	for _, suf := range moved {
		if err := os.Remove(aside + suf); err != nil && !errors.Is(err, os.ErrNotExist) {
			leftovers = append(leftovers, fmt.Sprintf("%s (%v)", aside+suf, err))
		}
	}
	switch {
	case syncErr != nil && len(leftovers) > 0:
		return fmt.Errorf("restore: the database HAS been replaced, but syncing the directory failed (%w) and these old files remain: %s; "+
			"run the restore again, or start antares and verify, then delete them", syncErr, strings.Join(leftovers, ", "))
	case syncErr != nil:
		return fmt.Errorf("restore: the database HAS been replaced, but syncing the directory failed (%w); "+
			"it may not survive a crash: run the restore again, or start antares and verify", syncErr)
	case len(leftovers) > 0:
		return fmt.Errorf("restore: the database HAS been replaced; the old files could not be removed and are safe to delete once antares is verified: %s", strings.Join(leftovers, ", "))
	}
	return nil
}

var dbSuffixes = []string{"", "-wal", "-shm", "-journal"}

// sameFile reports whether a and b name the same file: equal cleaned paths, or
// the same inode through a symlink or hard link. A stat error other than "does
// not exist" is returned so the caller refuses rather than guesses.
func sameFile(a, b string) (bool, error) {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true, nil
	}
	fa, err := os.Stat(a)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil // a missing source is reported by VerifyBackup
	} else if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

// requireIdle proves, as far as it can, that nothing is using the live
// database, and returns a release func that keeps the proof in force.
//
//  1. An exclusive flock on the data directory: it fails while any store.Open handle
//     (the running service, backup, another restore) holds the database, even
//     one that is idle between transactions. This is the real check.
//  2. A write transaction that begins and rolls back: catches a writer that
//     does not go through store.Open (the sqlite3 CLI).
//
// A file SQLite reports as corrupt or not a database (the case restore exists
// for) does not fail step 2; every other failure means idleness is unproven.
// A missing live file is an empty volume. The lock is on the directory, not the
// file, so it also covers the moment during the swap when the live name does
// not exist: Open takes the shared lock before it may create the file.
func requireIdle(ctx context.Context, path string) (func(), error) {
	none := func() {}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return none, fmt.Errorf("restore: cannot resolve the data directory: %w", err)
	}
	lock, err := flockFile(dir, syscall.LOCK_EX)
	if errors.Is(err, errLocked) {
		return none, errors.New("restore: the live database is in use (held open by antares or another store user); stop antares first")
	} else if err != nil {
		return none, fmt.Errorf("restore: cannot lock the data directory: %w", err)
	}
	release := func() { lock.Close() }
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return release, nil
	} else if err != nil {
		release()
		return none, fmt.Errorf("restore: cannot inspect the live database: %w", err)
	}
	if err := dsnPath(path); err != nil {
		release()
		return none, fmt.Errorf("restore: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(500)")
	if err != nil {
		release()
		return none, fmt.Errorf("restore: cannot open the live database to test it: %w", err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if damaged(err) {
			return release, nil
		}
		release()
		if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
			return none, errors.New("restore: the live database is in use (a writer holds its lock); stop whatever is using it first")
		}
		return none, fmt.Errorf("restore: cannot prove the live database is idle: %w", err)
	}
	tx.Rollback()
	return release, nil
}

// syncDir makes a create/rename in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
