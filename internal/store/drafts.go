package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Server-side composer drafts (PRD §21.6, UXP-007, issue #107). One row per {user, chat}
// and one per user for the composer of a chat that does not exist yet. The body is
// plain text, not encrypted: nothing here claims otherwise.
//
// Every write is a compare-and-set on the revision the writer last saw, so two devices
// cannot silently overwrite each other: the loser gets ErrRevisionConflict together
// with the winning draft and decides what to do with its own text. Revision 0 means
// "no draft yet" and is what a first write must present. Revisions only grow: deleting a draft
// leaves an empty tombstone at a higher revision, so no revision is ever reused.

// MaxDraftBytes bounds one draft body.
const MaxDraftBytes = 32 << 10

// ErrRevisionConflict means the draft is no longer at the revision the writer saw.
var ErrRevisionConflict = errors.New("store: draft revision conflict")

// Draft is a stored draft. The zero value (Revision 0) is "nothing saved".
type Draft struct {
	Body      string
	Revision  int64
	UpdatedAt string
}

// draftScope names which table a draft lives in and, for a chat, which chat. The table
// name is one of two constants, never caller input.
type draftScope struct {
	table, chatID string
}

func chatScope(chatID string) draftScope {
	return draftScope{table: "chat_drafts", chatID: chatID}
}

var newChatScope = draftScope{table: "new_chat_drafts"}

func (s draftScope) where() string {
	if s.table == "chat_drafts" {
		return "user_id = ? AND chat_id = ?"
	}
	return "user_id = ?"
}

func (s draftScope) args(userID string) []any {
	if s.table == "chat_drafts" {
		return []any{userID, s.chatID}
	}
	return []any{userID}
}

// errCASMiss means a compare-and-set UPDATE matched no row although the transaction had just read the
// expected revision. The write lock makes that impossible today; if it ever happens the write must fail
// loudly, never be reported as saved.
var errCASMiss = errors.New("store: draft compare-and-set matched no row")

// oneRow turns an UPDATE's result into an error unless exactly one row changed.
func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return errCASMiss
	}
	return nil
}

// requireChat fails with ErrNoChat unless the chat is the user's own live chat.
func (s draftScope) requireChat(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID string) error {
	if s.table != "chat_drafts" {
		return nil
	}
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM chats WHERE id = ? AND user_id = ? AND status IN `+liveChat, s.chatID, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoChat
	}
	return err
}

func (s draftScope) read(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID string) (Draft, error) {
	w := s.where()
	var d Draft
	err := q.QueryRowContext(ctx, `SELECT body, revision, updated_at FROM `+s.table+` WHERE `+w, s.args(userID)...).Scan(&d.Body, &d.Revision, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, nil
	}
	return d, err
}

func (s draftScope) get(ctx context.Context, db *sql.DB, userID string) (Draft, error) {
	if err := s.requireChat(ctx, db, userID); err != nil {
		return Draft{}, err
	}
	return s.read(ctx, db, userID)
}

func (s draftScope) put(ctx context.Context, db *sql.DB, userID, body string, ifRevision int64, now time.Time) (Draft, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	if err := s.requireChat(ctx, tx, userID); err != nil {
		return Draft{}, err
	}
	cur, err := s.read(ctx, tx, userID)
	if err != nil {
		return Draft{}, err
	}
	if cur.Revision != ifRevision {
		return cur, ErrRevisionConflict
	}
	next := Draft{Body: body, Revision: cur.Revision + 1, UpdatedAt: ts(now)}
	if cur.Revision == 0 {
		if s.table == "chat_drafts" {
			_, err = tx.ExecContext(ctx, `INSERT INTO chat_drafts (user_id, chat_id, body, revision, updated_at) VALUES (?,?,?,?,?)`,
				userID, s.chatID, body, next.Revision, next.UpdatedAt)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO new_chat_drafts (user_id, body, revision, updated_at) VALUES (?,?,?,?)`,
				userID, body, next.Revision, next.UpdatedAt)
		}
	} else {
		w := s.where()
		err = oneRow(tx.ExecContext(ctx, `UPDATE `+s.table+` SET body = ?, revision = ?, updated_at = ? WHERE `+w+` AND revision = ?`,
			append([]any{body, next.Revision, next.UpdatedAt}, append(s.args(userID), cur.Revision)...)...))
	}
	if err != nil {
		return Draft{}, err
	}
	return next, tx.Commit()
}

// del tombstones the draft instead of removing the row: the body is emptied and the revision
// still advances, so a revision number is never issued twice for a scope. A device holding an
// older revision therefore keeps getting ErrRevisionConflict after a delete and recreate (no ABA).
// The tombstone reads back as an empty draft at its revision; the next write must present it.
func (s draftScope) del(ctx context.Context, db *sql.DB, userID string, ifRevision int64, now time.Time) (Draft, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	if err := s.requireChat(ctx, tx, userID); err != nil {
		return Draft{}, err
	}
	cur, err := s.read(ctx, tx, userID)
	if err != nil {
		return Draft{}, err
	}
	if cur.Revision != ifRevision {
		return cur, ErrRevisionConflict
	}
	if cur.Revision == 0 || cur.Body == "" {
		return cur, tx.Commit() // nothing saved, or already a tombstone: the state asked for
	}
	next := Draft{Revision: cur.Revision + 1, UpdatedAt: ts(now)}
	w := s.where()
	if err := oneRow(tx.ExecContext(ctx, `UPDATE `+s.table+` SET body = '', revision = ?, updated_at = ? WHERE `+w+` AND revision = ?`,
		append([]any{next.Revision, next.UpdatedAt}, append(s.args(userID), cur.Revision)...)...)); err != nil {
		return Draft{}, err
	}
	return next, tx.Commit()
}

// GetChatDraft returns the owner's draft for a chat (Revision 0 when none), or ErrNoChat
// for a chat that is absent, deleted or someone else's.
func GetChatDraft(ctx context.Context, db *sql.DB, userID, chatID string) (Draft, error) {
	return chatScope(chatID).get(ctx, db, userID)
}

// PutChatDraft writes the draft only if it is still at ifRevision; otherwise it returns
// the current draft and ErrRevisionConflict.
func PutChatDraft(ctx context.Context, db *sql.DB, userID, chatID, body string, ifRevision int64, now time.Time) (Draft, error) {
	return chatScope(chatID).put(ctx, db, userID, body, ifRevision, now)
}

// DeleteChatDraft tombstones the draft only if it is still at ifRevision and returns the tombstone.
func DeleteChatDraft(ctx context.Context, db *sql.DB, userID, chatID string, ifRevision int64, now time.Time) (Draft, error) {
	return chatScope(chatID).del(ctx, db, userID, ifRevision, now)
}

// The new-chat draft is the user's composer before any chat exists; it is keyed by user alone.
func GetNewChatDraft(ctx context.Context, db *sql.DB, userID string) (Draft, error) {
	return newChatScope.get(ctx, db, userID)
}

func PutNewChatDraft(ctx context.Context, db *sql.DB, userID, body string, ifRevision int64, now time.Time) (Draft, error) {
	return newChatScope.put(ctx, db, userID, body, ifRevision, now)
}

func DeleteNewChatDraft(ctx context.Context, db *sql.DB, userID string, ifRevision int64, now time.Time) (Draft, error) {
	return newChatScope.del(ctx, db, userID, ifRevision, now)
}
