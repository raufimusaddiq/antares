package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Chat share links (#250). A share is a frozen copy of a chat's turns taken by its owner; the public read finds it
// by the hash of the link's token and nothing else, and only while the chat and its owner's account are in good
// standing.

// ErrNoShare is a share that does not exist, was replaced or stopped, or whose chat or owner may no longer be read.
var ErrNoShare = errors.New("store: no such share")

// ShareStatus is what the owner sees about their own chat's link. The link itself is never stored.
type ShareStatus struct {
	CreatedAt string
	Turns     int
}

// SharedChat is the whole of what the public read returns: no account, chat or profile identity.
type SharedChat struct {
	Title, Turns, CreatedAt string // Turns is the JSON array the owner's snapshot stored
}

// PutShare stores the owner's snapshot of their own chat under a new token hash, replacing any earlier link to it.
// The chat must be the owner's and not being deleted, otherwise ErrNoChat and nothing changes.
func PutShare(ctx context.Context, db *sql.DB, userID, chatID, tokenHash, title, turns string, n int, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_shares WHERE chat_id = ? AND user_id = ?`, chatID, userID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_shares (token_hash, user_id, chat_id, title, turns, turn_count, created_at)
		SELECT ?, user_id, id, ?, ?, ?, ? FROM chats WHERE id = ? AND user_id = ? AND status IN ('ACTIVE','ARCHIVED')`,
		tokenHash, title, turns, n, ts(now), chatID, userID)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNoChat
	}
	return tx.Commit()
}

// GetShareStatus reports the owner's link to their own chat, or ErrNoShare.
func GetShareStatus(ctx context.Context, db *sql.DB, userID, chatID string) (ShareStatus, error) {
	var s ShareStatus
	err := db.QueryRowContext(ctx, `SELECT created_at, turn_count FROM chat_shares WHERE chat_id = ? AND user_id = ?`, chatID, userID).Scan(&s.CreatedAt, &s.Turns)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNoShare
	}
	return s, err
}

// DeleteShare stops the owner's link to their own chat. No link is not an error.
func DeleteShare(ctx context.Context, db *sql.DB, userID, chatID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM chat_shares WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	return err
}

// ReadShare is the public read. A share whose chat is being deleted, or whose owner is no longer approved, reads
// as ErrNoShare: the link dies with the account's standing, without waiting for anyone to stop it.
func ReadShare(ctx context.Context, db *sql.DB, tokenHash string) (SharedChat, error) {
	var s SharedChat
	err := db.QueryRowContext(ctx, `SELECT s.title, s.turns, s.created_at FROM chat_shares s
		JOIN chats c ON c.id = s.chat_id AND c.user_id = s.user_id AND c.status IN ('ACTIVE','ARCHIVED')
		JOIN users u ON u.id = s.user_id AND u.approval_status = 'APPROVED'
		WHERE s.token_hash = ?`, tokenHash).Scan(&s.Title, &s.Turns, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNoShare
	}
	return s, err
}
