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
	// MkdirAll's mode is ignored for an existing directory, so tighten it
	// explicitly, and resolve symlinks so everything below acts on the real
	// location (a data dir mounted through a symlink is legitimate).
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve data dir: %w", err)
	}
	if err := os.Chmod(realDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: secure data dir: %w", err)
	}
	path = filepath.Join(realDir, filepath.Base(path))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	f.Close()
	if path, err = filepath.EvalSymlinks(path); err != nil { // a symlinked db file
		return nil, fmt.Errorf("store: resolve database file: %w", err)
	}
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

// secureFiles forces 0600 on the database file and its WAL sidecars. Missing
// sidecars are fine. The mode of the database file itself is not inherited by
// one that already exists (a restored backup), hence the explicit chmod.
func secureFiles(path string) error {
	for _, suf := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suf, 0o600); err != nil && !(suf != "" && errors.Is(err, os.ErrNotExist)) {
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

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }
