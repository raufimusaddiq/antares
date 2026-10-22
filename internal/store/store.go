// Package store owns the SQLite database: opening it with bounded, safe
// settings, migrating the schema and the one-shot admin bootstrap.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO
)

// Pool bounds (PERF-002). SQLite serialises writers, so a large pool buys
// nothing but memory; a few connections let WAL readers run beside a writer.
const (
	maxConns    = 4
	busyTimeout = 5 * time.Second
)

// Open opens (creating if needed) the database at path with WAL, a busy
// timeout, foreign keys on and a bounded pool. Transactions begin IMMEDIATE so
// two processes contend on the write lock up front, honouring busy_timeout,
// instead of failing on a lock upgrade mid-transaction.
func Open(path string) (*sql.DB, error) {
	if path == "" || strings.ContainsAny(path, "?#%") {
		return nil, errors.New("store: invalid database path")
	}
	// Refuse before touching anything: Open chmods the data directory, so a
	// relative path would chmod the working directory, and a trailing
	// separator would nest the database one level down.
	if !filepath.IsAbs(path) {
		return nil, errors.New("store: database path must be absolute")
	}
	if strings.HasSuffix(path, "/") || filepath.Base(path) == "." {
		return nil, errors.New("store: database path must name a file, not a directory")
	}
	path = filepath.Clean(path)
	// Everything below checks and acts on the RESOLVED location: a symlinked
	// database file must not let the configured directory stand in for the one
	// SQLite will really use.
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if path, err = filepath.EvalSymlinks(path); err != nil {
			return nil, fmt.Errorf("store: resolve database symlink: %w", err)
		}
	}
	if filepath.Dir(path) == string(filepath.Separator) {
		return nil, errors.New("store: database must live in its own directory, not the filesystem root")
	}
	// MkdirAll's mode is ignored for an existing directory, so tighten it
	// explicitly (a data dir mounted through a symlink is legitimate).
	dir := filepath.Dir(path)
	_, statErr := os.Stat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve data dir: %w", err)
	}
	// Forcing 0700 is right for a directory that is ours alone and wrong for a
	// shared one (/var/lib). An existing directory is dedicated only if the
	// running user owns it and it holds nothing but this database's files.
	if !created {
		if err := requireDedicated(realDir, filepath.Base(path)); err != nil {
			return nil, err
		}
	}
	if realDir == string(filepath.Separator) {
		return nil, errors.New("store: data directory resolves to the filesystem root")
	}
	if err := os.Chmod(realDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: secure data dir: %w", err)
	}
	path = filepath.Join(realDir, filepath.Base(path))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	f.Close()
	if err := secureFiles(path); err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)"+
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path, busyTimeout.Milliseconds())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), busyTimeout)
	defer cancel()
	var mode string
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: read journal mode: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: read foreign_keys: %w", err)
	}
	// The file the driver opened must be the file secured above. '?', '#' and
	// '%' are rejected because the driver would rewrite them; this proves it.
	var seq int
	var schema, opened string
	if err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &schema, &opened); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: read database list: %w", err)
	}
	if real, err := filepath.EvalSymlinks(opened); err != nil || real != path {
		db.Close()
		return nil, errors.New("store: driver opened a different file than the one secured")
	}
	if mode != "wal" || fk != 1 {
		db.Close()
		return nil, fmt.Errorf("store: pragmas not applied (journal_mode=%s foreign_keys=%d)", mode, fk)
	}
	// SQLite creates -wal/-shm lazily and only copies the db file's mode when it
	// creates them; one that came along with a restore keeps its old mode.
	// Never delete them (that would lose committed rows): chmod, and repeat now
	// that the pragmas have created whatever was missing.
	if err := secureFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// requireDedicated refuses a directory that is not exclusively Antares': owned
// by another user, or holding any entry other than the database and its
// SQLite sidecars. It runs before any chmod.
func requireDedicated(dir, base string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("store: stat data dir: %w", err)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
		return errors.New("store: data directory is not owned by the running user; use a dedicated directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("store: read data dir: %w", err)
	}
	own := map[string]bool{"lost+found": true, base: true, base + "-wal": true, base + "-shm": true, base + "-journal": true}
	for _, e := range entries {
		if !own[e.Name()] {
			return errors.New("store: data directory is not dedicated to Antares (it holds other files); " +
				"set ANTARES_DB_PATH inside a directory used only for this database")
		}
	}
	return nil
}

// secureFiles forces 0600 on the database file and its WAL sidecars. Missing
// sidecars are fine. Each file is opened O_NOFOLLOW and chmod-ed through the
// descriptor, so a symlinked or non-regular file is refused rather than
// followed to whatever it points at (no check-then-chmod race either).
func secureFiles(path string) error {
	for _, suf := range []string{"", "-wal", "-shm", "-journal"} {
		f, err := os.OpenFile(path+suf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			if suf != "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("store: database file%s must be a regular file: %w", suf, err)
		}
		st, err := f.Stat()
		if err == nil && !st.Mode().IsRegular() {
			err = errors.New("not a regular file")
		}
		if err == nil {
			err = f.Chmod(0o600)
		}
		f.Close()
		if err != nil {
			return fmt.Errorf("store: secure database file%s: %w", suf, err)
		}
	}
	return nil
}

// NewID returns a random 128-bit identifier.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// tsLayout is fixed-width (nanoseconds, always nine digits) so that string
// order equals time order; RFC3339Nano trims zeros and would not sort.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }
