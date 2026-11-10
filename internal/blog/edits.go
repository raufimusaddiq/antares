package blog

import (
	"context"
	"database/sql"
	"errors"
)

// EditView is one suggested change to the author's text as the author sees it. The server records only the author's decision: the change itself is made
// by the editor in the browser, so the draft has one writer (specs/006-ai-editor-blog, plan Complexity Tracking).
type EditView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Part     int    `json:"part"`
	Anchor   string `json:"anchor"`
	Original string `json:"original"`
	Proposed string `json:"proposed"`
	Reason   string `json:"reason"`
	Decision string `json:"decision"`
}

func readEdits(ctx context.Context, q querier, checkID string) ([]EditView, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, kind, part, anchor, original, proposed, reason, decision FROM post_edits WHERE check_id = ? ORDER BY position`, checkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EditView{}
	for rows.Next() {
		var e EditView
		if err := rows.Scan(&e.ID, &e.Kind, &e.Part, &e.Anchor, &e.Original, &e.Proposed, &e.Reason, &e.Decision); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DecideEdit records that the author accepted or rejected a suggestion. Only the post's latest check can be decided on, and only while it is ready.
// It never changes the draft.
func (s *Service) DecideEdit(ctx context.Context, author, postID, editID, decision string) (*EditView, error) {
	if decision != "accepted" && decision != "rejected" {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var checkID, state string
	err = tx.QueryRowContext(ctx, `SELECT c.id, c.state FROM post_edits e JOIN post_checks c ON c.id = e.check_id AND c.author_id = e.author_id
		WHERE e.id = ? AND e.post_id = ? AND e.author_id = ?`, editID, postID, author).Scan(&checkID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if state != "ready" {
		return nil, ErrStale
	}
	var latestID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM post_checks WHERE post_id = ? AND author_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1`, postID, author).Scan(&latestID); err != nil {
		return nil, err
	}
	if latestID != checkID {
		return nil, ErrSuperseded
	}
	if _, err := tx.ExecContext(ctx, `UPDATE post_edits SET decision = ?, decided_at = ? WHERE id = ? AND author_id = ?`, decision, s.stamp(), editID, author); err != nil {
		return nil, err
	}
	var e EditView
	if err := tx.QueryRowContext(ctx, `SELECT id, kind, part, anchor, original, proposed, reason, decision FROM post_edits WHERE id = ? AND author_id = ?`, editID, author).
		Scan(&e.ID, &e.Kind, &e.Part, &e.Anchor, &e.Original, &e.Proposed, &e.Reason, &e.Decision); err != nil {
		return nil, err
	}
	return &e, tx.Commit()
}
