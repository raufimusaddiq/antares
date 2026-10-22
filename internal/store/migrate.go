package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the embedded migration set.
func Migrations() fs.FS {
	sub, _ := fs.Sub(embedded, "migrations")
	return sub
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func load(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []migration
	for _, n := range names {
		num, _, ok := strings.Cut(n, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migrate: bad file name %q (want NNNN_name.sql)", n)
		}
		if len(out) > 0 && out[len(out)-1].version == v {
			return nil, fmt.Errorf("migrate: duplicate version %d", v)
		}
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, migration{v, n, string(b), hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// Migrate applies, in order, every migration in fsys not yet recorded.
//
// Each migration runs in its own transaction together with its
// schema_migrations row, so a failure rolls back completely and leaves the
// previous version intact (SQLite DDL is transactional). Because transactions
// begin IMMEDIATE and the applied set is re-read inside them, two processes
// migrating at once serialise and the second finds nothing to do.
//
// Detection: a recorded version whose checksum differs from the file, or a
// recorded version this binary does not know, is an error — the database was
// migrated by something else, and starting would be guessing. Recovery is to
// restore the pre-migration backup (deployment runbook) or deploy the matching
// binary; there is no down-migration.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) ([]int, error) {
	migs, err := load(fsys)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	var applied []int
	for _, m := range migs {
		did, err := applyOne(ctx, db, m)
		if err != nil {
			return applied, fmt.Errorf("migrate: %s: %w", m.name, err)
		}
		if did {
			applied = append(applied, m.version)
		}
	}
	return applied, checkUnknown(ctx, db, migs)
}

func applyOne(ctx context.Context, db *sql.DB, m migration) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sum string
	switch err := tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, m.version).Scan(&sum); {
	case err == nil:
		if sum != m.checksum {
			return false, fmt.Errorf("checksum mismatch: applied migration was edited")
		}
		return false, nil
	case err != sql.ErrNoRows:
		return false, err
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?,?,?,?)`,
		m.version, m.name, m.checksum, ts(time.Now())); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func checkUnknown(ctx context.Context, db *sql.DB, migs []migration) error {
	known := map[int]bool{}
	for _, m := range migs {
		known[m.version] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return err
		}
		if !known[v] {
			return fmt.Errorf("migrate: database has version %d unknown to this binary (newer deploy or downgrade)", v)
		}
	}
	return rows.Err()
}

// SchemaCurrent reports whether the applied set is exactly the migrations in
// fsys: none missing (a gap below the maximum is not ready) and none unknown.
func SchemaCurrent(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	migs, err := load(fsys)
	if err != nil {
		return err
	}
	if err := checkUnknown(ctx, db, migs); err != nil {
		return err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		return err
	}
	if n != len(migs) {
		return fmt.Errorf("schema has %d migrations applied, want %d", n, len(migs))
	}
	return nil
}
