package store

import (
	"context"
	"database/sql"
	"errors"
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
	// Turns are the chat's newest finished runs, oldest first, one per reply. Tokens
	// are the numbers Hermes reported for that run; Reported is false when it gave none.
	Turns          []TurnUsage
	TurnsTruncated bool
}

// MaxTurns bounds the per-turn list a usage read returns.
const MaxTurns = 100

// TurnUsage is the usage of one run (one reply).
type TurnUsage struct {
	RunID    string
	Status   string
	Reported bool
	RunUsage
}

// UserReportedTokens is the input+output tokens Hermes reported across all of the
// user's chats, and whether any run reported at all (unknown is not zero).
func UserReportedTokens(ctx context.Context, db *sql.DB, userID string) (used int64, known bool, err error) {
	var n sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT SUM(input_tokens + output_tokens) FROM runs
		WHERE user_id = ? AND input_tokens IS NOT NULL`, userID).Scan(&n)
	if err != nil {
		return 0, false, err
	}
	// Tokens of chats the user deleted still count: deleting a chat does not reset the budget.
	var gone sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT input_tokens + output_tokens FROM deleted_chat_usage WHERE user_id = ?`, userID).Scan(&gone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	return n.Int64 + gone.Int64, n.Valid || gone.Valid, nil
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
	} else if err != nil {
		return cu, err
	}
	trows, err := db.QueryContext(ctx, `SELECT id, status, input_tokens IS NOT NULL, COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		COALESCE(cache_read_tokens,0), COALESCE(cache_write_tokens,0), COALESCE(usage_model,'') FROM runs
		WHERE user_id = ? AND chat_id = ? AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT')
		ORDER BY created_at DESC, id DESC LIMIT ?`, userID, chatID, MaxTurns+1)
	if err != nil {
		return cu, err
	}
	defer trows.Close()
	for trows.Next() {
		var t TurnUsage
		if err := trows.Scan(&t.RunID, &t.Status, &t.Reported, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite, &t.Model); err != nil {
			return cu, err
		}
		cu.Turns = append(cu.Turns, t)
	}
	if err := trows.Err(); err != nil {
		return cu, err
	}
	if len(cu.Turns) > MaxTurns {
		cu.Turns, cu.TurnsTruncated = cu.Turns[:MaxTurns], true
	}
	for i, j := 0, len(cu.Turns)-1; i < j; i, j = i+1, j-1 {
		cu.Turns[i], cu.Turns[j] = cu.Turns[j], cu.Turns[i]
	}
	return cu, nil
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

// SpendingLimits are a user's optional money limits in the price list's currency (#152). Zero means not set.
type SpendingLimits struct {
	DailySoft, MonthlySoft, DailyHard float64
}

// GetSpendingLimits reads the caller's limits; a user who set none gets the zero value.
func GetSpendingLimits(ctx context.Context, db Querier, userID string) (SpendingLimits, error) {
	var a, b, c sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT daily_soft_idr, monthly_soft_idr, daily_hard_idr FROM user_preferences WHERE user_id = ?`, userID).Scan(&a, &b, &c)
	if err == sql.ErrNoRows {
		return SpendingLimits{}, nil
	}
	return SpendingLimits{DailySoft: a.Float64, MonthlySoft: b.Float64, DailyHard: c.Float64}, err
}

// SetSpendingLimits replaces the caller's three limits at once (zero clears one). Repeating it is harmless.
func SetSpendingLimits(ctx context.Context, db *sql.DB, userID string, l SpendingLimits, now time.Time) error {
	v := func(f float64) any {
		if f > 0 {
			return f
		}
		return nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO user_preferences (user_id, daily_soft_idr, monthly_soft_idr, daily_hard_idr, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET daily_soft_idr = excluded.daily_soft_idr, monthly_soft_idr = excluded.monthly_soft_idr,
		daily_hard_idr = excluded.daily_hard_idr, updated_at = excluded.updated_at`,
		userID, v(l.DailySoft), v(l.MonthlySoft), v(l.DailyHard), ts(now))
	return err
}

// UserModelUsage sums the user's finished replies that reported usage and started at or after since, per model, across
// all of the user's chats (#152 spending windows, #151 usage dashboard), deleted chats included (deleted_run_usage,
// kept per UTC day: for a deleted chat the window starts at since's day).
func UserModelUsage(ctx context.Context, db Querier, userID string, since time.Time) ([]ModelUsage, error) {
	// Live runs, plus what deleted chats' runs cost (kept per UTC day, so the window is compared by day).
	rows, err := db.QueryContext(ctx, `SELECT model, SUM(n), SUM(i), SUM(o), SUM(cr), SUM(cw) FROM (
		SELECT COALESCE(usage_model,'') AS model, COUNT(*) AS n, SUM(input_tokens) AS i, SUM(output_tokens) AS o,
			SUM(COALESCE(cache_read_tokens,0)) AS cr, SUM(COALESCE(cache_write_tokens,0)) AS cw FROM runs
		WHERE user_id = ? AND input_tokens IS NOT NULL AND created_at >= ?
		AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT') GROUP BY usage_model
		UNION ALL
		SELECT model, SUM(runs), SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens)
		FROM deleted_run_usage WHERE user_id = ? AND day >= ? GROUP BY model
	) GROUP BY model ORDER BY model`, userID, ts(since), userID, since.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.Runs, &m.Input, &m.Output, &m.CacheRead, &m.CacheWrite); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UsageRow is one group of a user's finished replies in a usage range (#151): one chat, one UTC day, one model.
// ChatID is "" for chats the user has deleted (kept per day and model only). Reported is false for the group of
// replies that finished with no usage reported: they are counted, never as zero tokens.
type UsageRow struct {
	ChatID, Title, Day string
	Reported           bool
	ModelUsage
}

// UsageRows groups the user's finished replies started at or after since (deleted chats: from since's day).
// Ownership is in the query: only the caller's own runs and chats.
func UsageRows(ctx context.Context, db Querier, userID string, since time.Time) ([]UsageRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.chat_id, COALESCE(c.title,''), substr(r.created_at,1,10), r.input_tokens IS NOT NULL, COALESCE(r.usage_model,''), COUNT(*),
			COALESCE(SUM(r.input_tokens),0), COALESCE(SUM(r.output_tokens),0), COALESCE(SUM(r.cache_read_tokens),0), COALESCE(SUM(r.cache_write_tokens),0)
		FROM runs r JOIN chats c ON c.id = r.chat_id AND c.user_id = r.user_id
		WHERE r.user_id = ? AND r.created_at >= ? AND r.status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT')
		GROUP BY r.chat_id, substr(r.created_at,1,10), r.input_tokens IS NOT NULL, COALESCE(r.usage_model,'')
		UNION ALL
		SELECT '', '', day, 1, model, runs, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens
		FROM deleted_run_usage WHERE user_id = ? AND day >= ?
		ORDER BY 3, 1, 5`, userID, ts(since), userID, since.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var u UsageRow
		if err := rows.Scan(&u.ChatID, &u.Title, &u.Day, &u.Reported, &u.Model, &u.Runs, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
