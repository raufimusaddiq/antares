package store

import (
	"context"
	"database/sql"
	"time"
)

// JobShutdownPending is the tenant detail of a suspended account whose scheduled jobs are not yet verified paused
// upstream (PRD §12 rule 3): Decide sets it with the suspension, and it is cleared once a pause is verified. Until then
// the jobs may still run, and the admin user screen shows it as recorded.
const JobShutdownPending = "SUSPENSION_PENDING_JOB_SHUTDOWN: this account's scheduled jobs could not be paused in its workspace and may still run. Antares retries every few minutes."

// ClearJobShutdownPending removes the mark once the jobs are paused; any other detail is left alone.
func ClearJobShutdownPending(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET tenant_detail = NULL WHERE id = ? AND tenant_detail = ?`, userID, JobShutdownPending)
	return err
}

// PendingJobShutdowns lists the suspended accounts still marked with an id after `after` ("" from the start), in id
// order, at most limit.
func PendingJobShutdowns(ctx context.Context, db *sql.DB, after string, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM users WHERE approval_status = 'SUSPENDED' AND tenant_detail = ? AND id > ?
		ORDER BY id LIMIT ?`, JobShutdownPending, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AuditSystem is Audit for a background action with no acting user.
func AuditSystem(ctx context.Context, db *sql.DB, action, targetID, outcome string, now time.Time) error {
	return Audit(ctx, db, "", action, "user", targetID, outcome, "", now)
}
