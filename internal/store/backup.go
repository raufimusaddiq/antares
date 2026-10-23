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
	"time"
)

// BackupTempSuffix names the staging file a streamed backup uses next to the
// database. It is the one extra file requireDedicated tolerates.
const BackupTempSuffix = ".backup-tmp"

// RestoreTempSuffix names the staging file Restore uses next to the database.
const RestoreTempSuffix = ".restore-tmp"

// openRO opens an existing SQLite file read-only through the driver. It never
// creates the file, and never applies the WAL/pragma setup Open does.
func openRO(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Report is the outcome of Check.
type Report struct {
	Integrity  []string // lines from PRAGMA integrity_check; exactly ["ok"] when healthy
	ForeignKey []string // violations from PRAGMA foreign_key_check, if any
}

// OK reports whether the database is intact.
func (r Report) OK() bool {
	return len(r.Integrity) == 1 && r.Integrity[0] == "ok" && len(r.ForeignKey) == 0
}

// Check runs PRAGMA integrity_check and foreign_key_check on the file at path
// and reports what SQLite said. A file SQLite cannot read at all is an error;
// one that opens but is damaged is a Report whose OK is false. At most 50
// problems of each kind are listed.
func Check(ctx context.Context, path string) (Report, error) {
	db, err := openRO(path)
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	var r Report
	if r.Integrity, err = pragmaLines(ctx, db, `PRAGMA integrity_check(50)`); err != nil {
		return Report{}, fmt.Errorf("integrity_check: %w", err)
	}
	if !r.OK() {
		return r, nil // table walks below are unreliable on a damaged file
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return Report{}, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	for rows.Next() && len(r.ForeignKey) < 50 {
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
	db, err := openRO(path)
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
// is staged under a temporary name, checked with integrity_check, set to 0600
// and only then renamed into place, so dest is either a verified backup or absent.
func Backup(ctx context.Context, db *sql.DB, dest string) error {
	if !filepath.IsAbs(dest) {
		return errors.New("backup: destination must be an absolute path")
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("backup: %s already exists; refusing to overwrite", dest)
	}
	tmp := dest + ".partial"
	os.Remove(tmp)
	defer os.Remove(tmp)
	// The path is a bind parameter; VACUUM INTO accepts an expression.
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	rep, err := Check(ctx, tmp)
	if err != nil {
		return fmt.Errorf("backup: verify snapshot: %w", err)
	}
	if !rep.OK() {
		return fmt.Errorf("backup: snapshot failed integrity_check: %s", strings.Join(append(rep.Integrity, rep.ForeignKey...), "; "))
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	// Link then unlink: unlike rename it cannot replace a dest created meanwhile.
	if err := os.Link(tmp, dest); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// BackupTo streams a verified snapshot of db to w, staging it in the database's
// own directory (the only writable place in the read-only container).
func BackupTo(ctx context.Context, db *sql.DB, dbPath string, w io.Writer) error {
	stage := dbPath + BackupTempSuffix
	os.Remove(stage)
	defer os.Remove(stage)
	if err := Backup(ctx, db, stage); err != nil {
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
	if err := requireIdle(ctx, livePath); err != nil {
		return err
	}
	if opt.KeepOldDir != "" {
		if filepath.Clean(opt.KeepOldDir) == filepath.Clean(dir) {
			return errors.New("restore: the keep-old directory must not be the data directory")
		}
		if err := os.MkdirAll(opt.KeepOldDir, 0o700); err != nil {
			return err
		}
		stamp := time.Now().UTC().Format("20060102T150405Z")
		for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
			err := copyFile(livePath+suf, filepath.Join(opt.KeepOldDir, filepath.Base(livePath)+suf+"."+stamp), 0o600)
			if err != nil && !errors.Is(err, os.ErrNotExist) { // absent live files (fresh volume) have nothing to keep
				return fmt.Errorf("restore: keep old %s: %w", filepath.Base(livePath)+suf, err)
			}
		}
	}
	for _, suf := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(livePath + suf); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(staged, livePath); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	return nil
}

// requireIdle refuses when another process holds the live database's write
// lock. A live file SQLite cannot open at all (the corruption case) is not an
// obstacle: that is what restore is for.
func requireIdle(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(500)")
	if err != nil {
		return nil
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
			return errors.New("restore: the live database is in use; stop antares first")
		}
		return nil
	}
	tx.Rollback()
	return nil
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
