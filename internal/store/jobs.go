package store

import (
	"context"
	"database/sql"
	"time"
)

// JobShutdownPending is the tenant detail of a suspended account whose scheduled jobs could not be paused upstream
// (PRD §12 rule 3): they may still run until a retry pauses them. The admin user screen shows it as recorded.
const JobShutdownPending = "SUSPENSION_PENDING_JOB_SHUTDOWN: this account's scheduled jobs could not be paused in its workspace and may still run. Antares retries every few minutes."

// MarkJobShutdownPending records the pending shutdown on a suspended account (only while it is still suspended).
func MarkJobShutdownPending(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET tenant_detail = ? WHERE id = ? AND approval_status = 'SUSPENDED'`, JobShutdownPending, userID)
	return err
}

// ClearJobShutdownPending removes the mark once the jobs are paused; any other detail is left alone.
func ClearJobShutdownPending(ctx context.Context, db *sql.DB, userID string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET tenant_detail = NULL WHERE id = ? AND tenant_detail = ?`, userID, JobShutdownPending)
	return err
}

// PendingJobShutdowns lists the suspended accounts still marked, oldest id first, at most limit.
func PendingJobShutdowns(ctx context.Context, db *sql.DB, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM users WHERE approval_status = 'SUSPENDED' AND tenant_detail = ? ORDER BY id LIMIT ?`,
		JobShutdownPending, limit)
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
