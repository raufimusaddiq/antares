package store

import (
	"context"
	"database/sql"
	"time"
)

// The admin's default model for the purposes that have none of their own storage (specs/004-admin-full-control). No row means
// "the service default". Images and videos keep their place on the tenant tool policy (TenantMediaModels).

// DefaultPurposes are the purposes a model default can be set for here.
var DefaultPurposes = []string{"chat", "summary", "blog_assistant"}

// ModelDefaults returns the saved default per purpose; a purpose with none is absent.
func ModelDefaults(ctx context.Context, db Querier) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT purpose, model_id FROM model_defaults`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, m string
		if err := rows.Scan(&p, &m); err != nil {
			return nil, err
		}
		out[p] = m
	}
	return out, rows.Err()
}

// ModelDefault is one purpose's saved default, "" when none.
func ModelDefault(ctx context.Context, db Querier, purpose string) (string, error) {
	var m string
	err := db.QueryRowContext(ctx, `SELECT model_id FROM model_defaults WHERE purpose = ?`, purpose).Scan(&m)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return m, err
}

// SetModelDefaults saves the given defaults ("" removes one) and audits each change, in one transaction. The caller has
// validated the models; only an administrator may call it (checked here as well).
func SetModelDefaults(ctx context.Context, db *sql.DB, actor string, changes map[string]string, reqID string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdmin(ctx, tx, actor); err != nil {
		return err
	}
	for _, purpose := range DefaultPurposes { // a fixed order, so the audit rows are the same however the map is walked
		model, ok := changes[purpose]
		if !ok {
			continue
		}
		if model == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM model_defaults WHERE purpose = ?`, purpose)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO model_defaults (purpose, model_id, set_by, set_at) VALUES (?,?,?,?)
				ON CONFLICT (purpose) DO UPDATE SET model_id = excluded.model_id, set_by = excluded.set_by, set_at = excluded.set_at`, purpose, model, actor, ts(now))
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at, detail)
			VALUES (?,?,?,?,?,?,?,?,?)`, NewID(), actor, "model_defaults.set", "model_defaults", purpose, "OK", nullIfEmpty(reqID), ts(now), purpose+"="+model); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ModelTotal is what a model was used for over a window: calls and tokens, never who.
type ModelTotal struct {
	Calls  int64 `json:"calls"`
	Tokens int64 `json:"tokens"`
}

// ModelUsageTotals sums, per model, the finished chat replies and blog post checks that reported usage and started at or after
// since. The query selects no user column: there is nothing here to say whose usage it was. Titles and summaries are not
// recorded, so they are not in it.
func ModelUsageTotals(ctx context.Context, db Querier, since time.Time) (map[string]ModelTotal, error) {
	rows, err := db.QueryContext(ctx, `SELECT model, SUM(n), SUM(t) FROM (
		SELECT COALESCE(usage_model,'') AS model, COUNT(*) AS n, SUM(COALESCE(input_tokens,0) + COALESCE(output_tokens,0)) AS t FROM runs
		WHERE input_tokens IS NOT NULL AND created_at >= ? AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT') GROUP BY usage_model
		UNION ALL
		SELECT COALESCE(usage_model,''), COUNT(*), SUM(COALESCE(input_tokens,0) + COALESCE(output_tokens,0)) FROM post_checks
		WHERE input_tokens IS NOT NULL AND started_at >= ? GROUP BY usage_model
	) WHERE model <> '' GROUP BY model`, ts(since), ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ModelTotal{}
	for rows.Next() {
		var m string
		var t ModelTotal
		if err := rows.Scan(&m, &t.Calls, &t.Tokens); err != nil {
			return nil, err
		}
		out[m] = t
	}
	return out, rows.Err()
}
