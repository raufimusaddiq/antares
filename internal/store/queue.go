package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Per-chat message queue (PRD §27.4, issue #110). The server is authoritative: the browser only
// shows what is stored here. Every function takes the owner and the chat, and a chat that is not
// the caller's is ErrNoChat, exactly like one that does not exist.

// MaxQueuedPerChat bounds one chat's queue.
const MaxQueuedPerChat = 20

var (
	// ErrNoItem is a queue item that does not exist, is not the caller's, or is no longer QUEUED.
	ErrNoItem = errors.New("store: no such queued message")
	// ErrQueueFull is an enqueue past MaxQueuedPerChat.
	ErrQueueFull = errors.New("store: the queue is full")
	// ErrQueueBusy is a claim while another dispatch of the same chat is still in flight.
	ErrQueueBusy = errors.New("store: a queued message is already being sent")
)

// QueueItem is a stored queued message. Ordinal is its 1-based place in the chat's queue.
type QueueItem struct {
	ID        string
	ChatID    string
	Ordinal   int
	Input     string
	Status    string
	CreatedAt string
	UpdatedAt string
}

// Queue is a chat's queue in order, and why it is paused ("" when it sends on its own).
type Queue struct {
	Items  []QueueItem
	Paused string
}

// IdempotencyKey is the run key a queued message is always sent under, so it can become at most one run.
func (q QueueItem) IdempotencyKey() string { return "queue-" + q.ID }

// GetQueue returns the owner's queue for a chat.
func GetQueue(ctx context.Context, db *sql.DB, userID, chatID string) (Queue, error) {
	var paused sql.NullString
	err := db.QueryRowContext(ctx, `SELECT queue_paused FROM chats WHERE id = ? AND user_id = ? AND status IN `+liveChat, chatID, userID).Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return Queue{}, ErrNoChat
	} else if err != nil {
		return Queue{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, chat_id, input, status, created_at, updated_at FROM queue_items
		WHERE user_id = ? AND chat_id = ? ORDER BY position, created_at, id`, userID, chatID)
	if err != nil {
		return Queue{}, err
	}
	defer rows.Close()
	q := Queue{Items: []QueueItem{}, Paused: paused.String}
	for rows.Next() {
		var it QueueItem
		if err := rows.Scan(&it.ID, &it.ChatID, &it.Input, &it.Status, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return Queue{}, err
		}
		it.Ordinal = len(q.Items) + 1
		q.Items = append(q.Items, it)
	}
	return q, rows.Err()
}

// Enqueue appends a message to the end of the owner's queue for a chat.
func Enqueue(ctx context.Context, db *sql.DB, userID, chatID, input string, now time.Time) (QueueItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return QueueItem{}, err
	}
	defer tx.Rollback()
	var n int
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), MAX(position) FROM queue_items WHERE user_id = ? AND chat_id = ?`, userID, chatID).Scan(&n, &last); err != nil {
		return QueueItem{}, err
	}
	if n >= MaxQueuedPerChat {
		return QueueItem{}, ErrQueueFull
	}
	it := QueueItem{ID: NewID(), ChatID: chatID, Ordinal: n + 1, Input: input, Status: "QUEUED", CreatedAt: ts(now), UpdatedAt: ts(now)}
	res, err := tx.ExecContext(ctx, `INSERT INTO queue_items (id, user_id, chat_id, position, input, status, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, 'QUEUED', ?, ? WHERE EXISTS (SELECT 1 FROM chats WHERE id = ? AND user_id = ? AND status IN `+liveChat+`)`,
		it.ID, userID, chatID, last.Int64+1, input, it.CreatedAt, it.UpdatedAt, chatID, userID)
	if err != nil {
		return QueueItem{}, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return QueueItem{}, ErrNoChat
	}
	return it, tx.Commit()
}

// EditQueued replaces the text of one of the owner's QUEUED messages. A message already being sent cannot change.
func EditQueued(ctx context.Context, db *sql.DB, userID, chatID, itemID, input string, now time.Time) error {
	return queueRow(db.ExecContext(ctx, `UPDATE queue_items SET input = ?, updated_at = ? WHERE id = ? AND user_id = ? AND chat_id = ? AND status = 'QUEUED'`,
		input, ts(now), itemID, userID, chatID))
}

// RemoveQueued deletes one of the owner's QUEUED messages. A message already being sent is not removed.
func RemoveQueued(ctx context.Context, db *sql.DB, userID, chatID, itemID string) error {
	return queueRow(db.ExecContext(ctx, `DELETE FROM queue_items WHERE id = ? AND user_id = ? AND chat_id = ? AND status = 'QUEUED'`, itemID, userID, chatID))
}

// SetQueuePaused records why the owner's queue for a chat is paused; "" turns automatic dispatch back on.
func SetQueuePaused(ctx context.Context, db *sql.DB, userID, chatID, reason string) error {
	err := queueRow(db.ExecContext(ctx, `UPDATE chats SET queue_paused = NULLIF(?, '') WHERE id = ? AND user_id = ? AND status IN `+liveChat, reason, chatID, userID))
	if errors.Is(err, ErrNoItem) {
		return ErrNoChat
	}
	return err
}

// PauseQueueIfWaiting pauses the queue for reason only when it has messages waiting and is not paused already,
// so the first reason is the one the owner sees.
func PauseQueueIfWaiting(ctx context.Context, db *sql.DB, userID, chatID, reason string) error {
	_, err := db.ExecContext(ctx, `UPDATE chats SET queue_paused = ? WHERE id = ? AND user_id = ? AND queue_paused IS NULL
		AND EXISTS (SELECT 1 FROM queue_items WHERE user_id = ? AND chat_id = ? AND status = 'QUEUED')`, reason, chatID, userID, userID, chatID)
	return err
}

// ClaimNext moves the head of the owner's queue from QUEUED to SUBMITTING with a compare-and-set, so of any
// number of concurrent dispatchers (two tabs, a tab and the run-end hook) exactly one gets it. Unless
// explicit, a paused queue yields ErrNoItem: only the owner's own "send next" takes from a paused queue.
func ClaimNext(ctx context.Context, db *sql.DB, userID, chatID string, explicit bool, now time.Time) (QueueItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return QueueItem{}, err
	}
	defer tx.Rollback()
	var paused sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT queue_paused FROM chats WHERE id = ? AND user_id = ? AND status IN `+liveChat, chatID, userID).Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return QueueItem{}, ErrNoChat
	} else if err != nil {
		return QueueItem{}, err
	}
	if paused.Valid && !explicit {
		return QueueItem{}, ErrNoItem
	}
	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items WHERE user_id = ? AND chat_id = ? AND status = 'SUBMITTING'`, userID, chatID).Scan(&busy); err != nil {
		return QueueItem{}, err
	}
	if busy > 0 {
		return QueueItem{}, ErrQueueBusy
	}
	var it QueueItem
	err = tx.QueryRowContext(ctx, `SELECT id, chat_id, input, created_at FROM queue_items WHERE user_id = ? AND chat_id = ? AND status = 'QUEUED'
		ORDER BY position, created_at, id LIMIT 1`, userID, chatID).Scan(&it.ID, &it.ChatID, &it.Input, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return QueueItem{}, ErrNoItem
	} else if err != nil {
		return QueueItem{}, err
	}
	if err := queueRow(tx.ExecContext(ctx, `UPDATE queue_items SET status = 'SUBMITTING', updated_at = ? WHERE id = ? AND user_id = ? AND status = 'QUEUED'`,
		ts(now), it.ID, userID)); err != nil {
		return QueueItem{}, err
	}
	it.Ordinal, it.Status, it.UpdatedAt = 1, "SUBMITTING", ts(now)
	return it, tx.Commit()
}

// SettleClaim ends a claim: sent (the message became a run, so the row goes) or released (no run was
// admitted, so it is QUEUED again at the head).
func SettleClaim(ctx context.Context, db *sql.DB, userID, itemID string, sent bool, now time.Time) error {
	if sent {
		return queueRow(db.ExecContext(ctx, `DELETE FROM queue_items WHERE id = ? AND user_id = ? AND status = 'SUBMITTING'`, itemID, userID))
	}
	return queueRow(db.ExecContext(ctx, `UPDATE queue_items SET status = 'QUEUED', updated_at = ? WHERE id = ? AND user_id = ? AND status = 'SUBMITTING'`,
		ts(now), itemID, userID))
}

// queueRow is ErrNoItem unless the statement changed a row.
func queueRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoItem
	}
	return nil
}
