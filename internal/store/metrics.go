package store

import (
	"context"
	"database/sql"
	"time"
)

// Metrics are the operator's aggregate counts (PRD §13.2, issue #114): numbers only, never an id, a name or content.
type Metrics struct {
	Accounts       map[string]int `json:"accounts"`       // by approval status (PENDING = pending registrations)
	Tenants        map[string]int `json:"tenants"`        // by tenant status (READY = Hermes tenant readiness)
	Runs           map[string]int `json:"runs"`           // by status, all time (chat/run success, failure, cancel)
	Runs24h        map[string]int `json:"runs24h"`        // by status, started in the last 24 hours
	OpenPrompts    int            `json:"openPrompts"`    // tool approvals and questions waiting for a person
	Assets         int            `json:"assets"`         // stored uploads
	AssetBytes     int64          `json:"assetBytes"`     // their total size
	Approvals      map[string]int `json:"approvals"`      // admin decisions by outcome
	QueuedMessages int            `json:"queuedMessages"` // messages waiting in chat queues
}

func countBy(ctx context.Context, db *sql.DB, query string, args ...any) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k sql.NullString
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		key := k.String
		if !k.Valid || key == "" {
			key = "UNKNOWN"
		}
		out[key] = n
	}
	return out, rows.Err()
}

// ReadMetrics reads the aggregate counts.
func ReadMetrics(ctx context.Context, db *sql.DB, now time.Time) (Metrics, error) {
	var m Metrics
	var err error
	if m.Accounts, err = countBy(ctx, db, `SELECT approval_status, COUNT(*) FROM users GROUP BY approval_status`); err != nil {
		return m, err
	}
	if m.Tenants, err = countBy(ctx, db, `SELECT tenant_status, COUNT(*) FROM users GROUP BY tenant_status`); err != nil {
		return m, err
	}
	if m.Runs, err = countBy(ctx, db, `SELECT status, COUNT(*) FROM runs GROUP BY status`); err != nil {
		return m, err
	}
	if m.Runs24h, err = countBy(ctx, db, `SELECT status, COUNT(*) FROM runs WHERE created_at >= ? GROUP BY status`, ts(now.Add(-24*time.Hour))); err != nil {
		return m, err
	}
	if m.Approvals, err = countBy(ctx, db, `SELECT action, COUNT(*) FROM approvals GROUP BY action`); err != nil {
		return m, err
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_prompts WHERE status IN ('PENDING','SENDING')`).Scan(&m.OpenPrompts); err != nil {
		return m, err
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM assets`).Scan(&m.Assets, &m.AssetBytes); err != nil {
		return m, err
	}
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items`).Scan(&m.QueuedMessages)
	return m, err
}
