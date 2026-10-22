package store

import (
	"context"
	"database/sql"
	"time"
)

// BootstrapAdmin creates the first admin from configuration (AUTH-008). It
// returns created=true only for the call that wins; every later call, from this
// or any other process, returns false and changes nothing.
//
// Single-shot comes from the database, not from configuration: the check of the
// admin_bootstrap latch and the inserts run in one IMMEDIATE transaction, so
// concurrent starters serialise on the write lock, and the latch's
// PRIMARY KEY (id = 1) is a second line of defence.
//
// If a user with this (issuer, sub) already signed in, that row is promoted
// instead of a duplicate being created.
func BootstrapAdmin(ctx context.Context, db *sql.DB, issuer, sub, email string, now time.Time) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM admin_bootstrap WHERE id = 1`).Scan(&one); {
	case err == nil:
		return false, nil
	case err != sql.ErrNoRows:
		return false, err
	}

	var id string
	switch err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE google_issuer = ? AND google_sub = ?`, issuer, sub).Scan(&id); {
	case err == sql.ErrNoRows:
		id = NewID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users
			(id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at, approved_at)
			VALUES (?,?,?,?,'ADMIN','APPROVED','UNPROVISIONED',?,?)`,
			id, issuer, sub, email, ts(now), ts(now)); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role = 'ADMIN', approval_status = 'APPROVED',
			approved_at = COALESCE(approved_at, ?) WHERE id = ?`, ts(now), id); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_bootstrap (id, user_id, created_at) VALUES (1,?,?)`,
		id, ts(now)); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, outcome, created_at)
		VALUES (?,NULL,'admin.bootstrap','user',?,'ok',?)`, NewID(), id, ts(now)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
