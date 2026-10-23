package store

import (
	"context"
	"database/sql"
	"time"
)

// RunUsage is what Hermes reported for one run.
type RunUsage struct {
	Input, Output, CacheRead, CacheWrite int64
	Model                                string
}

// ModelUsage is the usage of one chat's runs that were served by one model.
type ModelUsage struct {
	Model string // "" when Hermes did not say which model served the run
	Runs  int    // finished runs in this group
	RunUsage
}

// ChatUsage sums reported usage. Runs without reported usage are counted in
// Finished but contribute no tokens: unknown is not zero.
type ChatUsage struct {
	Finished  int // terminal runs
	Reported  int // of those, runs with reported usage (never more than Finished)
	ByModel   []ModelUsage
	LastModel string // model of the newest run that reported one
}

// RecordRunUsage stores what Hermes reported for the user's own run.
func RecordRunUsage(ctx context.Context, db *sql.DB, userID, runID string, u RunUsage) error {
	_, err := db.ExecContext(ctx, `UPDATE runs SET input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
		cache_write_tokens = ?, usage_model = NULLIF(?, '') WHERE id = ? AND user_id = ?`,
		u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Model, runID, userID)
	return err
}

// GetChatUsage reads the usage of the user's own chat; ownership is in the query.
func GetChatUsage(ctx context.Context, db *sql.DB, userID, chatID string) (ChatUsage, error) {
	var cu ChatUsage
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE user_id = ? AND chat_id = ?
		AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT')`, userID, chatID).Scan(&cu.Finished); err != nil {
		return cu, err
	}
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(usage_model,''), COUNT(*), SUM(input_tokens), SUM(output_tokens),
		SUM(cache_read_tokens), SUM(cache_write_tokens) FROM runs
		WHERE user_id = ? AND chat_id = ? AND input_tokens IS NOT NULL
		AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT') GROUP BY usage_model ORDER BY usage_model`, userID, chatID)
	if err != nil {
		return cu, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.Runs, &m.Input, &m.Output, &m.CacheRead, &m.CacheWrite); err != nil {
			return cu, err
		}
		cu.Reported += m.Runs
		cu.ByModel = append(cu.ByModel, m)
	}
	if err := rows.Err(); err != nil {
		return cu, err
	}
	err = db.QueryRowContext(ctx, `SELECT COALESCE(usage_model,'') FROM runs WHERE user_id = ? AND chat_id = ?
		AND usage_model IS NOT NULL ORDER BY created_at DESC, id DESC LIMIT 1`, userID, chatID).Scan(&cu.LastModel)
	if err == sql.ErrNoRows {
		err = nil
	}
	return cu, err
}

// TokenBudget returns the user's per-chat token limit; 0 means they set none.
func TokenBudget(ctx context.Context, db *sql.DB, userID string) (int64, error) {
	var n sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT token_budget FROM user_preferences WHERE user_id = ?`, userID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n.Int64, err
}

// SetTokenBudget stores the user's own limit; 0 clears it.
func SetTokenBudget(ctx context.Context, db *sql.DB, userID string, limit int64, now time.Time) error {
	var v any
	if limit > 0 {
		v = limit
	}
	_, err := db.ExecContext(ctx, `INSERT INTO user_preferences (user_id, token_budget, updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET token_budget = excluded.token_budget, updated_at = excluded.updated_at`,
		userID, v, ts(now))
	return err
}
