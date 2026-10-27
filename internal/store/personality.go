package store

import (
	"context"
	"database/sql"
	"time"
)

// Personality is the user's own additional instructions (#111); "" means none.
func Personality(ctx context.Context, db *sql.DB, userID string) (string, error) {
	var p sql.NullString
	err := db.QueryRowContext(ctx, `SELECT personality FROM user_preferences WHERE user_id = ?`, userID).Scan(&p)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return p.String, err
}

// SetPersonality replaces the user's own instructions; "" clears them.
func SetPersonality(ctx context.Context, db *sql.DB, userID, text string, now time.Time) error {
	var v any
	if text != "" {
		v = text
	}
	_, err := db.ExecContext(ctx, `INSERT INTO user_preferences (user_id, personality, updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET personality = excluded.personality, updated_at = excluded.updated_at`, userID, v, ts(now))
	return err
}
