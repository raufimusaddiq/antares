package store

import (
	"context"
	"database/sql"
	"time"
)

// The administrator's switch for the built-in writing rules (specs/005-antislop-writing). No row means on.

// WritingRulesEnabled reports whether the rules go out. A read error returns true with the error: a failing read must never
// turn the rules off.
func WritingRulesEnabled(ctx context.Context, db Querier) (bool, error) {
	var on int
	err := db.QueryRowContext(ctx, `SELECT enabled FROM writing_rules_setting WHERE id = 1`).Scan(&on)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return on == 1, nil
}

// SetWritingRules saves the switch and audits it, in one transaction. Only an administrator.
func SetWritingRules(ctx context.Context, db *sql.DB, actor string, enabled bool, reqID string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdmin(ctx, tx, actor); err != nil {
		return err
	}
	v := 0
	if enabled {
		v = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO writing_rules_setting (id, enabled, updated_by, updated_at) VALUES (1,?,?,?)
		ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, updated_by = excluded.updated_by, updated_at = excluded.updated_at`, v, actor, ts(now)); err != nil {
		return err
	}
	detail := "enabled=false"
	if enabled {
		detail = "enabled=true"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, outcome, request_id, created_at, detail)
		VALUES (?,?,?,?,?,?,?,?,?)`, NewID(), actor, "writing_rules.set", "writing_rules", "1", "OK", nullIfEmpty(reqID), ts(now), detail); err != nil {
		return err
	}
	return tx.Commit()
}

// WritingRulesCount is how many runs and post checks started in a window carried the rules or not. Counts only: it selects no
// user, chat or post column.
type WritingRulesCount struct {
	With       int64 `json:"with"`
	Without    int64 `json:"without"`
	NotApplied int64 `json:"notApplied"`
}

// WritingRulesCounts counts chat runs and post checks created at or after since. With = 'v1'; Without = 'off' or NULL;
// NotApplied = NULL on rows created at or after migration 29 was applied (older rows predate the feature and are not blamed).
func WritingRulesCounts(ctx context.Context, db Querier, since time.Time) (WritingRulesCount, error) {
	var applied string
	if err := db.QueryRowContext(ctx, `SELECT applied_at FROM schema_migrations WHERE version = 29`).Scan(&applied); err != nil {
		return WritingRulesCount{}, err
	}
	var c WritingRulesCount
	for _, q := range []string{
		`SELECT COALESCE(SUM(writing_rules = 'v1'),0), COALESCE(SUM(writing_rules IS NULL OR writing_rules = 'off'),0),
		        COALESCE(SUM(writing_rules IS NULL AND created_at >= ?2),0) FROM runs WHERE created_at >= ?1`,
		`SELECT COALESCE(SUM(writing_rules = 'v1'),0), COALESCE(SUM(writing_rules IS NULL OR writing_rules = 'off'),0),
		        COALESCE(SUM(writing_rules IS NULL AND started_at >= ?2),0) FROM post_checks WHERE started_at >= ?1`,
	} {
		var w, wo, na int64
		if err := db.QueryRowContext(ctx, q, ts(since), applied).Scan(&w, &wo, &na); err != nil {
			return WritingRulesCount{}, err
		}
		c.With += w
		c.Without += wo
		c.NotApplied += na
	}
	return c, nil
}

// Writing-rules values of a run or a check (see migration 0029).
const (
	RulesAttached = "v1"
	RulesOff      = "off"
)

// SetRunWritingRules records what a run was sent with: RulesAttached, RulesOff, or "" for "expected and not attached" (NULL).
func SetRunWritingRules(ctx context.Context, db Querier, userID, runID, value string) error {
	var v any
	if value != "" {
		v = value
	}
	_, err := db.ExecContext(ctx, `UPDATE runs SET writing_rules = ? WHERE id = ? AND user_id = ?`, v, runID, userID)
	return err
}
