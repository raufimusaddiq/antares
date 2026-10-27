package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoSession is returned when a token hash names no live session.
var ErrNoSession = errors.New("store: no such session")

// User is the account row as the identity layer needs it. It carries no
// credential: the tenant binding's key material is never selected here.
type User struct {
	ID             string
	Issuer         string
	Sub            string
	Email          string
	DisplayName    string
	AvatarURL      string
	Role           string
	ApprovalStatus string
	TenantStatus   string
	CreatedAt      string
	// TenantBound is true only when a tenant binding row exists with status
	// READY. It is the one fact about the binding the identity layer may read.
	TenantBound bool
}

const userCols = `u.id, u.google_issuer, u.google_sub, u.email, COALESCE(u.display_name,''), COALESCE(u.avatar_url,''),
	u.role, u.approval_status, u.tenant_status, u.created_at,
	EXISTS (SELECT 1 FROM tenant_bindings b WHERE b.user_id = u.id AND b.status = 'READY')`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Issuer, &u.Sub, &u.Email, &u.DisplayName, &u.AvatarURL,
		&u.Role, &u.ApprovalStatus, &u.TenantStatus, &u.CreatedAt, &u.TenantBound)
	return u, err
}

// UpsertLogin records a verified Google sign-in. The durable key is
// (issuer, sub); email and profile fields are display metadata and are
// refreshed, never used to find the account (AUTH-002). A new identity becomes
// USER / PENDING / UNPROVISIONED and nothing else: no tenant binding, no key,
// no Hermes call (AUTH-003). Role and statuses of an existing user are never
// touched here.
func UpsertLogin(ctx context.Context, db *sql.DB, issuer, sub, email, name, avatar string, now time.Time) (u User, created bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, err
	}
	defer tx.Rollback()

	var id string
	switch err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE google_issuer = ? AND google_sub = ?`, issuer, sub).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		id, created = NewID(), true
		if _, err := tx.ExecContext(ctx, `INSERT INTO users
			(id, google_issuer, google_sub, email, display_name, avatar_url, role, approval_status, tenant_status, created_at, last_login_at)
			VALUES (?,?,?,?,?,?,'USER','PENDING','UNPROVISIONED',?,?)`,
			id, issuer, sub, email, nullIfEmpty(name), nullIfEmpty(avatar), ts(now), ts(now)); err != nil {
			return User{}, false, err
		}
	case err != nil:
		return User{}, false, err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, display_name = ?, avatar_url = ?, last_login_at = ?
			WHERE id = ?`, email, nullIfEmpty(name), nullIfEmpty(avatar), ts(now), id); err != nil {
			return User{}, false, err
		}
	}
	u, err = scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users u WHERE u.id = ?`, id))
	if err != nil {
		return User{}, false, err
	}
	return u, created, tx.Commit()
}

// CreateSession stores a session under the hash of its cookie token. The raw
// token never reaches the database.
func CreateSession(ctx context.Context, db *sql.DB, userID, tokenHash string, now time.Time, ttl time.Duration) error {
	_, err := db.ExecContext(ctx, `INSERT INTO app_sessions (id, user_id, token_hash, created_at, expires_at)
		VALUES (?,?,?,?,?)`, NewID(), userID, tokenHash, ts(now), ts(now.Add(ttl)))
	return err
}

// SessionUser resolves a live session (not revoked, not expired) to its user,
// reading the user row fresh so a status change takes effect on the very next
// request (AUTH-009). It returns ErrNoSession for anything else.
func SessionUser(ctx context.Context, db *sql.DB, tokenHash string, now time.Time) (User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userCols+`
		FROM app_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ?`, tokenHash, ts(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	return u, err
}

// RevokeSession invalidates one session server-side. Revoking a session that
// is unknown or already revoked is not an error.
func RevokeSession(ctx context.Context, db *sql.DB, tokenHash string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE app_sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		ts(now), tokenHash)
	return err
}

// RevokeUserSessions ends every live session of one user and forgets every device it would push alerts to (sign out
// everywhere, #150): a device that is signed out must not keep receiving the account's notifications. It touches only
// that user's rows, in one transaction, and reports how many sessions it ended.
func RevokeUserSessions(ctx context.Context, db *sql.DB, userID string, now time.Time) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE app_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, ts(now), userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id = ?`, userID); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// Audit appends to audit_log. actor may be empty when no account is known yet
// (a login that failed before identity was established).
func Audit(ctx context.Context, db *sql.DB, actor, action, targetKind, targetID, outcome, requestID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO audit_log
		(id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		NewID(), nullIfEmpty(actor), action, nullIfEmpty(targetKind), nullIfEmpty(targetID), outcome, nullIfEmpty(requestID), ts(now))
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
