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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	// The mode argument above is ignored for an existing file (a restored
	// backup, a copied volume), so tighten it explicitly.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: secure database file: %w", err)
	}
	f.Close()

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
	want, err := filepath.Abs(path)
	if err != nil || opened != want {
		db.Close()
		return nil, errors.New("store: driver opened a different file than the one secured")
	}
	if mode != "wal" || fk != 1 {
		db.Close()
		return nil, fmt.Errorf("store: pragmas not applied (journal_mode=%s foreign_keys=%d)", mode, fk)
	}
	return db, nil
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
