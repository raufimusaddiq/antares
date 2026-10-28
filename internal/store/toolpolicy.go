package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// TenantToolPolicy returns the admin's tenant toolset policy and a version that changes with every write (when and
// what). With none set it returns nil names: the configured default (ANTARES_TENANT_ALLOWED_TOOLSETS) applies.
func TenantToolPolicy(ctx context.Context, db *sql.DB) ([]string, string, error) {
	var raw, at string
	err := db.QueryRowContext(ctx, `SELECT toolsets, updated_at FROM tenant_tool_policy WHERE id = 1`).Scan(&raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, "", errors.New("store: tenant tool policy is not a JSON list")
	}
	return names, at + "|" + raw, nil
}

// SetTenantToolPolicy stores the policy (names already validated by the caller) and, in the same transaction,
// puts every approved account with a ready or failed binding back through verification, the same as an admin's
// tenant-verify: each run reapplies the policy to the profile and verifies it. It returns those accounts, for the
// caller to start. A run already in flight notices the change before it would mark the account ready.
func SetTenantToolPolicy(ctx context.Context, db *sql.DB, actor string, names []string, reqID string, now time.Time) ([]string, error) {
	raw, err := json.Marshal(names)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireAdmin(ctx, tx, actor); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenant_tool_policy (id, toolsets, updated_by, updated_at) VALUES (1,?,?,?)
		ON CONFLICT (id) DO UPDATE SET toolsets = excluded.toolsets, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		string(raw), actor, ts(now)); err != nil {
		return nil, err
	}
	if err := audit(ctx, tx, actor, "tool_policy.set", "global", "ok", reqID, now); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id FROM users u JOIN tenant_bindings b ON b.user_id = u.id
		WHERE u.approval_status = 'APPROVED' AND u.tenant_status IN ('READY','FAILED') ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET tenant_status = 'CONFIGURING', tenant_detail = NULL, tenant_attempts = 0,
			tenant_next_attempt_at = NULL WHERE id = ?`, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tenant_bindings SET status = 'CONFIGURING' WHERE user_id = ? AND status IN ('READY','FAILED','CONFIGURING')`, id); err != nil {
			return nil, err
		}
		if err := audit(ctx, tx, actor, "admin."+RequeueVerify, id, "ok", reqID, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
