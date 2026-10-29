package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Chat management (issue #148). Every statement names the owner in its own WHERE clause: a chat id
// that is unknown and one that belongs to someone else both match no row, so both are ErrNoChat.

// RenameChat sets the title of the user's own chat and rewrites its title search row in the same
// transaction, so the list and the search can never disagree about the name. The title is already
// cleaned by the caller; an empty one clears it (the UI shows "New chat"). Renaming does not count
// as activity: it does not move the chat in the list.
func RenameChat(ctx context.Context, db *sql.DB, userID, chatID, title string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE chats SET title = ? WHERE id = ? AND user_id = ? AND status IN `+liveChat,
		nullIfEmpty(title), chatID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return missOrState(ctx, tx, userID, chatID)
	}
	if err := setTitleDoc(ctx, tx, userID, chatID, title); err != nil {
		return err
	}
	return tx.Commit()
}

// SetAutoTitle gives the user's own chat a title only while it still has none, so a rename (by the user, at any
// time) always wins over the automatic title. It reports whether the title was set; an untitled chat that is gone,
// archived or someone else's is left alone.
func SetAutoTitle(ctx context.Context, db *sql.DB, userID, chatID, title string) (bool, error) {
	if strings.TrimSpace(title) == "" {
		return false, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE chats SET title = ? WHERE id = ? AND user_id = ? AND (title IS NULL OR title = '') AND status IN `+liveChat,
		title, chatID, userID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if err := setTitleDoc(ctx, tx, userID, chatID, title); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SetChatPinned pins or unpins the user's own chat. Only a chat in use can be pinned: an archived
// chat is out of the way by definition (ErrChatState); unpinning is always allowed and is idempotent.
// A pin keeps its first timestamp when repeated.
func SetChatPinned(ctx context.Context, db *sql.DB, userID, chatID string, pinned bool, now time.Time) error {
	var res sql.Result
	var err error
	if pinned {
		res, err = db.ExecContext(ctx, `UPDATE chats SET pinned_at = COALESCE(pinned_at, ?)
			WHERE id = ? AND user_id = ? AND status = 'ACTIVE'`, ts(now), chatID, userID)
	} else {
		res, err = db.ExecContext(ctx, `UPDATE chats SET pinned_at = NULL WHERE id = ? AND user_id = ? AND status IN `+liveChat, chatID, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	return missOrState(ctx, db, userID, chatID)
}

// SetChatVisualize turns the user's own chat's Visualize toggle on or off (#153). Repeating it is not an error.
func SetChatVisualize(ctx context.Context, db *sql.DB, userID, chatID string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := db.ExecContext(ctx, `UPDATE chats SET visualize = ? WHERE id = ? AND user_id = ? AND status IN `+liveChat, v, chatID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	return missOrState(ctx, db, userID, chatID)
}

// SetChatArchived moves the user's own chat into or out of the archive. Archiving also unpins it.
// Repeating the request is not an error. A chat whose delete is under way or has failed is not
// archived (ErrChatState): it has one thing to settle first.
func SetChatArchived(ctx context.Context, db *sql.DB, userID, chatID string, archived bool) error {
	var res sql.Result
	var err error
	if archived {
		res, err = db.ExecContext(ctx, `UPDATE chats SET status = 'ARCHIVED', pinned_at = NULL
			WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED')`, chatID, userID)
	} else {
		res, err = db.ExecContext(ctx, `UPDATE chats SET status = 'ACTIVE'
			WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED')`, chatID, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	return missOrState(ctx, db, userID, chatID)
}

// missOrState explains an update that matched nothing: no such chat for this owner (ErrNoChat), a chat
// that is being deleted right now (ErrDeleteInFlight: not gone, the delete may still fail, so a 404 would
// be a lie, exactly as for the delete route itself), or a chat in use whose state refuses the change.
func missOrState(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, chatID string) error {
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM chats WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED','PENDING_DELETE')`, chatID, userID).Scan(&status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoChat
	case err != nil:
		return err
	case status == "PENDING_DELETE":
		return ErrDeleteInFlight
	}
	return ErrChatState
}

// BeginDeleteChat claims the user's own chat for deletion and returns it, so the caller can ask
// Hermes to delete the session it names. The claim is one statement: the chat moves to
// PENDING_DELETE only if it has no run that is not terminal, so a run cannot be admitted into a chat
// that is being deleted (AdmitRun checks the status in its insert) and a delete cannot start under
// a running turn (ErrRunActive). Only an ACTIVE or ARCHIVED chat can be claimed, by exactly one request:
// a chat already PENDING_DELETE is ErrDeleteInFlight, so two overlapping deletes never both call Hermes and a failure
// of one cannot restore a chat the other is still deleting. A chat flagged after a failed delete is ACTIVE or
// ARCHIVED again, so it can be claimed: that is the retry. A restart settles an interrupted claim (RecoverRuns).
func BeginDeleteChat(ctx context.Context, db *sql.DB, userID, chatID string, now time.Time) (Chat, error) {
	// The status it had is remembered, and the pin is left alone, so a failed delete puts the chat back as it was.
	res, err := db.ExecContext(ctx, `UPDATE chats SET pre_delete_status = CASE WHEN status IN ('ACTIVE','ARCHIVED') THEN status ELSE pre_delete_status END,
		status = 'PENDING_DELETE', delete_failed_at = NULL
		WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED')
		  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.chat_id = chats.id AND r.user_id = chats.user_id AND r.status IN `+nonTerminal+`)`,
		chatID, userID)
	if err != nil {
		return Chat{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		err := db.QueryRowContext(ctx, `SELECT status FROM chats WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED','PENDING_DELETE')`, chatID, userID).Scan(&status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return Chat{}, ErrNoChat
		case err != nil:
			return Chat{}, err
		case status == "PENDING_DELETE":
			return Chat{}, ErrDeleteInFlight
		}
		return Chat{}, ErrRunActive
	}
	return scanChat(db.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats c WHERE c.id = ? AND c.user_id = ?`, chatID, userID))
}

// MarkDeleteFailed records that the upstream delete did not happen: the chat goes back to the status it had
// (active or archived; its pin was never touched), flagged, and the owner can retry. It only moves a chat
// that is PENDING_DELETE.
func MarkDeleteFailed(ctx context.Context, db *sql.DB, userID, chatID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE chats SET status = COALESCE(pre_delete_status, 'ACTIVE'), pre_delete_status = NULL, delete_failed_at = ?
		WHERE id = ? AND user_id = ? AND status = 'PENDING_DELETE'`, ts(now), chatID, userID)
	return err
}

// DeleteCounts is what a finished delete removed, for the audit trail and for tests. Counts only.
type DeleteCounts struct {
	Runs, Assets int64
}

// FinishDeleteChat removes everything Antares holds about a chat whose upstream session is gone, in
// one transaction: the search rows, the draft, the run records and prompts, the message metadata,
// the staged images that were sent only with this chat, and the chat. The tokens its runs reported
// are kept as two per-user numbers so the owner's token budget is not reset by deleting chats.
// The chat must be PENDING_DELETE and the user's own, otherwise ErrNoChat and nothing changes.
func FinishDeleteChat(ctx context.Context, db *sql.DB, userID, chatID string) (DeleteCounts, error) {
	var out DeleteCounts
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM chats WHERE id = ? AND user_id = ? AND status = 'PENDING_DELETE'`, chatID, userID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNoChat
		}
		return out, err
	}
	steps := []struct {
		q    string
		args []any
		into *int64
	}{
		// Only runs that reported tokens count, and a chat that reported none writes no row: "usage unknown" must not
		// turn into "0 tokens" because a chat was deleted.
		{`INSERT INTO deleted_chat_usage (user_id, input_tokens, output_tokens)
			SELECT ?, COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0) FROM runs
			WHERE chat_id = ? AND user_id = ? AND input_tokens IS NOT NULL HAVING COUNT(*) > 0
			ON CONFLICT(user_id) DO UPDATE SET input_tokens = input_tokens + excluded.input_tokens, output_tokens = output_tokens + excluded.output_tokens`,
			[]any{userID, chatID, userID}, nil},
		// What the runs cost stays countable per day and model (#152): a deleted chat cannot reset a spending limit.
		{`INSERT INTO deleted_run_usage (user_id, day, model, runs, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
			SELECT ?, substr(created_at, 1, 10), COALESCE(usage_model, ''), COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0) FROM runs
			WHERE chat_id = ? AND user_id = ? AND input_tokens IS NOT NULL AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT')
			GROUP BY substr(created_at, 1, 10), COALESCE(usage_model, '')
			ON CONFLICT(user_id, day, model) DO UPDATE SET runs = runs + excluded.runs, input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens, cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens`,
			[]any{userID, chatID, userID}, nil},
		// Replies that reported nothing are counted per day (#151): unknown, but not vanished.
		{`INSERT INTO deleted_unreported_runs (user_id, day, runs)
			SELECT ?, substr(created_at, 1, 10), COUNT(*) FROM runs
			WHERE chat_id = ? AND user_id = ? AND input_tokens IS NULL AND status IN ('COMPLETED','FAILED','CANCELLED','TIMED_OUT')
			GROUP BY substr(created_at, 1, 10)
			ON CONFLICT(user_id, day) DO UPDATE SET runs = runs + excluded.runs`,
			[]any{userID, chatID, userID}, nil},
		// A staged image is the owner's; it goes only when no other chat's run sent it too.
		{`DELETE FROM assets WHERE user_id = ?
			AND id IN (SELECT ri.asset_id FROM run_images ri JOIN runs r ON r.id = ri.run_id AND r.user_id = ri.user_id WHERE r.chat_id = ? AND r.user_id = ?)
			AND id NOT IN (SELECT ri.asset_id FROM run_images ri JOIN runs r ON r.id = ri.run_id AND r.user_id = ri.user_id WHERE r.chat_id <> ? AND r.user_id = ?)`,
			[]any{userID, chatID, userID, chatID, userID}, &out.Assets},
		{`DELETE FROM run_images WHERE user_id = ? AND run_id IN (SELECT id FROM runs WHERE chat_id = ? AND user_id = ?)`, []any{userID, chatID, userID}, nil},
		{`DELETE FROM run_prompts WHERE user_id = ? AND run_id IN (SELECT id FROM runs WHERE chat_id = ? AND user_id = ?)`, []any{userID, chatID, userID}, nil},
		{`DELETE FROM messages WHERE chat_id = ? AND user_id = ?`, []any{chatID, userID}, nil},
		{`DELETE FROM runs WHERE chat_id = ? AND user_id = ?`, []any{chatID, userID}, &out.Runs},
		{`DELETE FROM search_docs WHERE chat_id = ? AND user_id = ?`, []any{chatID, userID}, nil},
		{`DELETE FROM search_state WHERE chat_id = ? AND user_id = ?`, []any{chatID, userID}, nil},
		{`DELETE FROM chat_drafts WHERE chat_id = ? AND user_id = ?`, []any{chatID, userID}, nil},
		{`DELETE FROM chats WHERE id = ? AND user_id = ? AND status = 'PENDING_DELETE'`, []any{chatID, userID}, nil},
	}
	for _, s := range steps {
		res, err := tx.ExecContext(ctx, s.q, s.args...)
		if err != nil {
			return DeleteCounts{}, err
		}
		if s.into != nil {
			*s.into, _ = res.RowsAffected()
		}
	}
	return out, tx.Commit()
}

// DeleteInFlight reports whether the user's own chat is being deleted right now. A foreign or unknown id is false,
// so the answer cannot reveal someone else's chat.
func DeleteInFlight(ctx context.Context, db *sql.DB, userID, chatID string) (bool, error) {
	err := db.QueryRowContext(ctx, `SELECT 1 FROM chats WHERE id = ? AND user_id = ? AND status = 'PENDING_DELETE'`, chatID, userID).Scan(new(int))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
