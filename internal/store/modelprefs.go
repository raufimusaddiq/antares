package store

import (
	"context"
	"database/sql"
	"time"
)

// Model visibility (PRD §26.1, LLM-ACC-003, issue #115). Hiding is a per-owner view
// preference; the block list is the admin's and wins over it.

// MaxModelIDs bounds either list.
const MaxModelIDs = 500

// HiddenModels returns the models the user has hidden from their own picker.
func HiddenModels(ctx context.Context, db *sql.DB, userID string) ([]string, error) {
	return modelIDs(ctx, db, `SELECT model_id FROM model_hidden WHERE user_id = ? ORDER BY model_id LIMIT ?`, userID, MaxModelIDs)
}

// SetHiddenModels replaces the user's hidden set. An empty set reveals everything.
func SetHiddenModels(ctx context.Context, db *sql.DB, userID string, ids []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_hidden WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_hidden (user_id, model_id) VALUES (?,?)`, userID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BlockedModels returns the models the admin has blocked for everyone.
func BlockedModels(ctx context.Context, db *sql.DB) ([]string, error) {
	return modelIDs(ctx, db, `SELECT model_id FROM model_blocked ORDER BY model_id LIMIT ?`, MaxModelIDs)
}

// SetBlockedModels replaces the global block list and records who did it.
func SetBlockedModels(ctx context.Context, db *sql.DB, actor string, ids []string, requestID string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_blocked`); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO model_blocked (model_id, blocked_by, created_at) VALUES (?,?,?)`,
			id, actor, ts(now)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, NewID(), actor, "model_policy.set", "model_policy", "global", "OK", nullIfEmpty(requestID), ts(now)); err != nil {
		return err
	}
	return tx.Commit()
}

func modelIDs(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
