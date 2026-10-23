package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Errors the chat layer translates into API answers.
var (
	// ErrNoChat is returned for a chat or run that does not exist OR belongs to
	// someone else. The two are deliberately indistinguishable (PRD §9.1: 404
	// for foreign resources).
	ErrNoChat = errors.New("store: no such chat or run")
	// ErrRunActive means the chat already has a run that is not terminal.
	ErrRunActive = errors.New("store: the chat already has an active run")
	// ErrGate means the account is not (or no longer) eligible for Hermes calls.
	ErrGate = errors.New("store: account is not eligible for a Hermes request")
)

// Run statuses (the CHECK constraint in 0001_init.sql is the source of truth).
const (
	RunPending      = "PENDING"
	RunRunning      = "RUNNING"
	RunCompleted    = "COMPLETED"
	RunFailed       = "FAILED"
	RunCancelled    = "CANCELLED"
	RunDisconnected = "DISCONNECTED"
	RunTimedOut     = "TIMED_OUT"
)

// RunTerminal reports a status that can never change again.
func RunTerminal(s string) bool {
	switch s {
	case RunCompleted, RunFailed, RunCancelled, RunTimedOut:
		return true
	}
	return false
}

// nonTerminal is the SQL spelling of "not RunTerminal"; keep the two in step.
const nonTerminal = `('PENDING','RUNNING','DISCONNECTED')`

// ActiveBinding is the database half of the default-deny gate
// (docs/runbooks/security-invariants.md §1, after the session check): the
// account is APPROVED, its tenant is READY, and its binding is READY and has
// been verified. It returns the sealed credential, or ErrGate. There is no
// other way to obtain a credential for a Hermes call.
func ActiveBinding(ctx context.Context, db *sql.DB, userID string) (SealedCredential, error) {
	var s SealedCredential
	err := db.QueryRowContext(ctx, `SELECT b.id, b.profile_name, b.credential_ciphertext, b.credential_key_version, 1
		FROM users u JOIN tenant_bindings b ON b.user_id = u.id
		WHERE u.id = ? AND u.approval_status = 'APPROVED' AND u.tenant_status = 'READY'
		  AND b.status = 'READY' AND b.verified_at IS NOT NULL`, userID).
		Scan(&s.BindingID, &s.Profile, &s.Ciphertext, &s.KeyVersion, &s.Verified)
	if errors.Is(err, sql.ErrNoRows) {
		return SealedCredential{}, ErrGate
	}
	return s, err
}

// Chat is an owned conversation. Content lives in Hermes (PRD §10.2).
type Chat struct {
	ID              string
	UserID          string
	BindingID       string
	HermesSessionID string
	Title           string
	Status          string
	CreatedAt       string
	UpdatedAt       string
	ActiveRunID     string // the chat's non-terminal run, if any
}

const chatCols = `c.id, c.user_id, c.hermes_binding_id, c.hermes_session_id, COALESCE(c.title,''), c.status, c.created_at, c.updated_at,
	COALESCE((SELECT r.id FROM runs r WHERE r.chat_id = c.id AND r.user_id = c.user_id AND r.status IN ` + nonTerminal + ` LIMIT 1),'')`

func scanChat(row interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	err := row.Scan(&c.ID, &c.UserID, &c.BindingID, &c.HermesSessionID, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.ActiveRunID)
	return c, err
}

// CreateChat records a chat for the user on the binding they currently hold.
// The binding id must belong to the same user (the composite foreign key
// enforces it too).
func CreateChat(ctx context.Context, db *sql.DB, userID, bindingID, hermesSessionID, title string, now time.Time) (Chat, error) {
	id := NewID()
	t := ts(now)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, title, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'ACTIVE', ?, ?)`, id, userID, bindingID, hermesSessionID, nullIfEmpty(title), t, t); err != nil {
		return Chat{}, err
	}
	// The title is searchable from the moment the chat exists (UXP-003).
	if err := setTitleDoc(ctx, tx, userID, id, title); err != nil {
		return Chat{}, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return Chat{ID: id, UserID: userID, BindingID: bindingID, HermesSessionID: hermesSessionID, Title: title, Status: "ACTIVE", CreatedAt: t, UpdatedAt: t}, nil
}

// GetChat returns the user's own chat, or ErrNoChat. Ownership is the WHERE
// clause, and the chat must sit on the user's current binding.
func GetChat(ctx context.Context, db *sql.DB, userID, chatID string) (Chat, error) {
	c, err := scanChat(db.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats c
		WHERE c.id = ? AND c.user_id = ? AND c.status = 'ACTIVE'
		  AND EXISTS (SELECT 1 FROM tenant_bindings b WHERE b.id = c.hermes_binding_id AND b.user_id = c.user_id)`, chatID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, ErrNoChat
	}
	return c, err
}

// ChatCursor is the keyset position after the last chat of a page.
type ChatCursor struct{ UpdatedAt, ID string }

// ListChats returns up to limit of the user's chats, newest activity first,
// starting after the cursor (nil = the first page). It asks for one extra row
// so the caller knows whether another page exists; that row is returned last
// and is not part of the page. Ordering is (updated_at DESC, id), so a page
// boundary is a position, not a count: a chat created meanwhile cannot shift
// later pages the way an offset would.
func ListChats(ctx context.Context, db *sql.DB, userID string, limit int, after *ChatCursor) (page []Chat, more bool, err error) {
	q := `SELECT ` + chatCols + ` FROM chats c WHERE c.user_id = ? AND c.status = 'ACTIVE'`
	args := []any{userID}
	if after != nil {
		q += ` AND (c.updated_at < ? OR (c.updated_at = ? AND c.id > ?))`
		args = append(args, after.UpdatedAt, after.UpdatedAt, after.ID)
	}
	q += ` ORDER BY c.updated_at DESC, c.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, false, err
		}
		page = append(page, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

// Run is one admitted turn.
type Run struct {
	ID            string
	UserID        string
	ChatID        string
	HermesRunID   string
	Status        string
	ErrorCode     string
	CreatedAt     string
	UpdatedAt     string
	HermesSession string // the owning chat's upstream session
	BindingID     string
}

const runCols = `r.id, r.user_id, r.chat_id, COALESCE(r.hermes_run_id,''), r.status, COALESCE(r.error_code,''), r.created_at, r.updated_at,
	c.hermes_session_id, c.hermes_binding_id`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.UserID, &r.ChatID, &r.HermesRunID, &r.Status, &r.ErrorCode, &r.CreatedAt, &r.UpdatedAt, &r.HermesSession, &r.BindingID)
	return r, err
}

// GetRun returns the user's own run, or ErrNoChat.
func GetRun(ctx context.Context, db *sql.DB, userID, runID string) (Run, error) {
	r, err := scanRun(db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs r
		JOIN chats c ON c.id = r.chat_id AND c.user_id = r.user_id
		WHERE r.id = ? AND r.user_id = ?`, runID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNoChat
	}
	return r, err
}

// AdmitRun inserts a PENDING run for the user's own chat. A repeat of the same
// idempotency key returns the original run (created == false) and never
// inserts a second one. A chat that already has a non-terminal run yields
// ErrRunActive, decided by the partial unique index, not by a prior read.
func AdmitRun(ctx context.Context, db *sql.DB, userID, chatID, idemKey string, now time.Time) (run Run, created bool, err error) {
	return AdmitRunWithin(ctx, db, userID, chatID, idemKey, now, 0)
}

// ErrBudget means the chat has already used maxTokens, so no new run is admitted.
var ErrBudget = errors.New("store: the chat has used its token budget")

// AdmitRunWithin is AdmitRun with a token budget: when maxTokens > 0 and the
// chat's reported input+output tokens have reached it, a new run is refused with
// ErrBudget. A replay of an existing key is decided first and is never refused,
// so it still gets its original run back.
func AdmitRunWithin(ctx context.Context, db *sql.DB, userID, chatID, idemKey string, now time.Time, maxTokens int64) (run Run, created bool, err error) {
	if _, err := GetChat(ctx, db, userID, chatID); err != nil {
		return Run{}, false, err
	}
	// A replay is decided first: the same key on the same chat is the same run,
	// even while that run is still active.
	existing := func() (Run, error) {
		return scanRun(db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs r
			JOIN chats c ON c.id = r.chat_id AND c.user_id = r.user_id
			WHERE r.user_id = ? AND r.idempotency_key = ? AND r.chat_id = ?`, userID, idemKey, chatID))
	}
	if r, err := existing(); err == nil {
		return r, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, err
	}
	if maxTokens > 0 {
		var used int64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens + output_tokens), 0) FROM runs
			WHERE user_id = ? AND chat_id = ?`, userID, chatID).Scan(&used); err != nil {
			return Run{}, false, err
		}
		if used >= maxTokens {
			return Run{}, false, ErrBudget
		}
	}
	id := NewID()
	t := ts(now)
	_, err = db.ExecContext(ctx, `INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'PENDING', ?, ?)`, id, userID, chatID, idemKey, t, t)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "runs.idempotency_key"): // lost a race with the same key
			if r, gerr := existing(); gerr == nil {
				return r, false, nil
			}
			return Run{}, false, ErrRunActive // the key belongs to another chat: not a replay
		case strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "runs.chat_id"):
			return Run{}, false, ErrRunActive
		}
		return Run{}, false, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE chats SET updated_at = ? WHERE id = ? AND user_id = ?`, t, chatID, userID); err != nil {
		return Run{}, false, err
	}
	r, err := GetRun(ctx, db, userID, id)
	return r, err == nil, err
}

// MoveRun changes a run's status only while it is still in one of the from
// states, and returns whether it did. A terminal run is never moved: a
// cancelled run cannot later become completed, whatever a late upstream event
// says. hermesRunID and errCode are written only when non-empty.
func MoveRun(ctx context.Context, db *sql.DB, userID, runID string, from []string, to, hermesRunID, errCode string, now time.Time) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("store: MoveRun needs at least one source status")
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := []any{to, hermesRunID, errCode, ts(now), runID, userID}
	for _, f := range from {
		args = append(args, f)
	}
	res, err := db.ExecContext(ctx, `UPDATE runs SET status = ?, hermes_run_id = COALESCE(NULLIF(?, ''), hermes_run_id),
		error_code = COALESCE(NULLIF(?, ''), error_code), updated_at = ?
		WHERE id = ? AND user_id = ? AND status IN (`+marks+`)`, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RecoverRuns runs once at start-up, before any request is served, when no
// worker can be attached to anything. A RUNNING run becomes DISCONNECTED (it
// may still be running upstream; reconnecting reconciles it). A PENDING run
// never reached Hermes as far as we recorded, so it FAILS rather than being
// replayed: no automatic re-execution of a possibly side-effecting turn.
func RecoverRuns(ctx context.Context, db *sql.DB, now time.Time) (disconnected, failed int64, err error) {
	t := ts(now)
	res, err := db.ExecContext(ctx, `UPDATE runs SET status = 'DISCONNECTED', updated_at = ? WHERE status = 'RUNNING'`, t)
	if err != nil {
		return 0, 0, err
	}
	disconnected, _ = res.RowsAffected()
	res, err = db.ExecContext(ctx, `UPDATE runs SET status = 'FAILED', error_code = 'HERMES_UNAVAILABLE', updated_at = ? WHERE status = 'PENDING'`, t)
	if err != nil {
		return 0, 0, err
	}
	failed, _ = res.RowsAffected()
	return disconnected, failed, nil
}

// ActiveRunCount is the number of non-terminal runs the user holds, for the
// per-user concurrency limit.
func ActiveRunCount(ctx context.Context, db *sql.DB, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE user_id = ? AND status IN `+nonTerminal, userID).Scan(&n)
	return n, err
}
