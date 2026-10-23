package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Push modes (the CHECK constraint in 0008_push.sql is the source of truth).
const (
	PushOff      = "off"
	PushAll      = "all"
	PushFailures = "failures"
)

// ErrNoSubscription is returned for a subscription that does not exist OR belongs
// to someone else; the two are indistinguishable.
var ErrNoSubscription = errors.New("store: no such push subscription")

// PushSub is one stored device. The sealed fields are opened by the caller.
type PushSub struct {
	ID, UserID, EndpointHash   string
	SealedEndpoint, SealedKeys []byte
	CreatedAt                  string
}

// MaxPushSubscriptions bounds the devices one account can register.
const MaxPushSubscriptions = 10

// PushMode returns the user's mode; an account that never chose has PushOff.
func PushMode(ctx context.Context, db *sql.DB, userID string) (string, error) {
	var m string
	err := db.QueryRowContext(ctx, `SELECT push_mode FROM user_preferences WHERE user_id = ?`, userID).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return PushOff, nil
	}
	return m, err
}

func SetPushMode(ctx context.Context, db *sql.DB, userID, mode string, now time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO user_preferences (user_id, push_mode, updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET push_mode = excluded.push_mode, updated_at = excluded.updated_at`, userID, mode, ts(now))
	return err
}

// ErrTooManyDevices means the account is at MaxPushSubscriptions.
var ErrTooManyDevices = errors.New("store: too many push subscriptions")

// SavePushSub registers a device for userID and, in the same transaction, sets the mode to
// "all" if the account never chose one (the first opt-in gesture). A mode the user already
// chose, including "off", is kept: adding a device does not undo a mute. A subscription for the
// same endpoint held by another account is removed first: the browser is now
// signed in as userID, so the previous holder must stop receiving on it.
// s.ID is the new row id; the caller sealed the secrets with it as additional data.
func SavePushSub(ctx context.Context, db *sql.DB, s PushSub, now time.Time) (id string, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var oldID, oldUser string
	switch err := tx.QueryRowContext(ctx, `SELECT id, user_id FROM push_subscriptions WHERE endpoint_hash = ?`, s.EndpointHash).Scan(&oldID, &oldUser); {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, oldID); err != nil {
			return "", err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`, s.UserID).Scan(&n); err != nil {
		return "", err
	}
	if n >= MaxPushSubscriptions {
		return "", ErrTooManyDevices
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO push_subscriptions (id, user_id, endpoint_hash, sealed_endpoint, sealed_keys, created_at)
		VALUES (?,?,?,?,?,?)`, s.ID, s.UserID, s.EndpointHash, s.SealedEndpoint, s.SealedKeys, ts(now)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_preferences (user_id, push_mode, updated_at) VALUES (?, 'all', ?)
		ON CONFLICT(user_id) DO NOTHING`, s.UserID, ts(now)); err != nil {
		return "", err
	}
	return s.ID, tx.Commit()
}

// ListPushSubs returns the account's devices, oldest first.
func ListPushSubs(ctx context.Context, db *sql.DB, userID string) ([]PushSub, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, endpoint_hash, sealed_endpoint, sealed_keys, created_at
		FROM push_subscriptions WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var s PushSub
		if err := rows.Scan(&s.ID, &s.UserID, &s.EndpointHash, &s.SealedEndpoint, &s.SealedKeys, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeletePushSub revokes one of the owner's devices; delivery stops at once because
// the sender reads the table for every run.
func DeletePushSub(ctx context.Context, db *sql.DB, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSubscription
	}
	return nil
}

// DeleteAllPushSubs removes every device of an account (a user who is no longer
// eligible keeps no live subscription).
func DeleteAllPushSubs(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id = ?`, userID)
	return err
}

// ClaimPushDelivery records the intent to send {subscription, run, kind}. It
// returns false when that notification was already claimed, so it is sent at most
// once. The row is scoped to the owner of both the subscription and the run.
func ClaimPushDelivery(ctx context.Context, db *sql.DB, userID, subID, runID, kind string, now time.Time) (bool, error) {
	res, err := db.ExecContext(ctx, `INSERT INTO push_delivery_events (id, user_id, subscription_id, run_id, event_kind, created_at)
		SELECT ?, s.user_id, s.id, r.id, ?, ? FROM push_subscriptions s, runs r
		WHERE s.id = ? AND s.user_id = ? AND r.id = ? AND r.user_id = ?
		ON CONFLICT(subscription_id, run_id, event_kind) DO NOTHING`, NewID(), kind, ts(now), subID, userID, runID, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkPushDelivered stamps a claimed delivery as accepted by the push service.
func MarkPushDelivered(ctx context.Context, db *sql.DB, subID, runID, kind string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE push_delivery_events SET delivered_at = ? WHERE subscription_id = ? AND run_id = ? AND event_kind = ?`,
		ts(now), subID, runID, kind)
	return err
}

// PrunePushDeliveries drops delivery records older than cutoff.
func PrunePushDeliveries(ctx context.Context, db *sql.DB, cutoff time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM push_delivery_events WHERE created_at < ?`, ts(cutoff))
	return err
}
