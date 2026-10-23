package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits that keep the derived index bounded (PRD §21.3).
const (
	MaxIndexedText     = 4000   // runes kept per turn
	MaxIndexedUserDocs = 100000 // turns per user, across all chats
	// MaxIndexedUserRunes bounds what one search scans (instr over every folded body). Measured in
	// TestMeasureSearchAtQuota; the figures are in docs/runbooks/search.md.
	MaxIndexedUserRunes = 25_000_000
)

// Search roles. RoleTitle is the chat title, indexed as turn -1.
const (
	RoleTitle = "title"
	TitleSeq  = -1
)

// Fold is the one case fold used for both sides of a search: the stored text and
// the query. SQLite's LOWER() folds ASCII only, so 'Ünal' and 'ÜNAL' would not
// meet; this folds every rune with Unicode simple lower-casing. It maps rune for
// rune, so a position in the folded text is the same position in the original.
func Fold(s string) string { return strings.Map(unicode.ToLower, s) }

// IndexDoc is one searchable turn.
type IndexDoc struct {
	Seq       int
	Role      string // "user" | "assistant"
	Text      string
	CreatedAt string
}

func clip(s string) (string, bool) {
	if utf8.RuneCountInString(s) <= MaxIndexedText {
		return s, false
	}
	return string([]rune(s)[:MaxIndexedText]), true
}

// SetTitleDoc (re)writes the chat title's search row inside tx. The chat must be the user's.
func setTitleDoc(ctx context.Context, tx *sql.Tx, userID, chatID, title string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_docs WHERE chat_id = ? AND user_id = ? AND seq = ?`, chatID, userID, TitleSeq); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	title, _ = clip(title)
	_, err := tx.ExecContext(ctx, `INSERT INTO search_docs (user_id, chat_id, seq, role, body, folded, created_at) VALUES (?,?,?,?,?,?,NULL)`,
		userID, chatID, TitleSeq, RoleTitle, title, Fold(title))
	return err
}

// ReplaceChatIndex replaces the turns of the user's own chat with docs, read from
// the transcript at startedAt. Compaction, rewind and regenerate are handled by
// replacement, not patching. It returns written == false (and changes nothing)
// when a newer index of the chat already exists. A chat that is not the user's
// is ErrNoChat. A size or quota cut-off is recorded as truncated.
func ReplaceChatIndex(ctx context.Context, db *sql.DB, userID, chatID string, docs []IndexDoc, startedAt time.Time) (written bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var title sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT title FROM chats WHERE id = ? AND user_id = ? AND status = 'ACTIVE'`, chatID, userID).Scan(&title); errors.Is(err, sql.ErrNoRows) {
		return false, ErrNoChat
	} else if err != nil {
		return false, err
	}
	at := ts(startedAt)
	var prev, inval string
	switch err := tx.QueryRowContext(ctx, `SELECT indexed_at, invalidated_at FROM search_state WHERE chat_id = ? AND user_id = ?`, chatID, userID).Scan(&prev, &inval); {
	case err == nil:
		if prev > at {
			return false, nil
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return false, err
	}
	var others int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_docs WHERE user_id = ? AND chat_id <> ? AND seq >= 0`, userID, chatID).Scan(&others); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_docs WHERE chat_id = ? AND user_id = ? AND seq >= 0`, chatID, userID); err != nil {
		return false, err
	}
	truncated := false
	var usedRunes int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(runes), 0) FROM search_state WHERE user_id = ? AND chat_id <> ?`, userID, chatID).Scan(&usedRunes); err != nil {
		return false, err
	}
	room := MaxIndexedUserDocs - others // quota counts turns, not title rows
	runes := 0
	for _, d := range docs {
		if (d.Role != "user" && d.Role != "assistant") || d.Seq < 0 || strings.TrimSpace(d.Text) == "" {
			continue
		}
		if room <= 0 {
			truncated = true
			break
		}
		text, cut := clip(d.Text)
		truncated = truncated || cut
		n := utf8.RuneCountInString(text)
		if usedRunes+runes+n > MaxIndexedUserRunes {
			truncated = true
			break
		}
		runes += n
		if _, err := tx.ExecContext(ctx, `INSERT INTO search_docs (user_id, chat_id, seq, role, body, folded, created_at) VALUES (?,?,?,?,?,?,?)`,
			userID, chatID, d.Seq, d.Role, text, Fold(text), nullIfEmpty(d.CreatedAt)); err != nil {
			return false, err
		}
		room--
	}
	if err := setTitleDoc(ctx, tx, userID, chatID, title.String); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO search_state (chat_id, user_id, indexed_at, invalidated_at, truncated, runes) VALUES (?,?,?,?,?,?)
		ON CONFLICT(chat_id) DO UPDATE SET indexed_at = excluded.indexed_at, truncated = excluded.truncated, failed_at = '', runes = excluded.runes`,
		chatID, userID, at, inval, boolInt(truncated), runes); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InvalidateChatIndex marks the chat's index stale (a turn was added). The next
// search re-reads the transcript. A chat that is not the user's is untouched.
func InvalidateChatIndex(ctx context.Context, db *sql.DB, userID, chatID string, now time.Time) error {
	t := ts(now)
	_, err := db.ExecContext(ctx, `INSERT INTO search_state (chat_id, user_id, indexed_at, invalidated_at)
		SELECT id, user_id, '', ? FROM chats WHERE id = ? AND user_id = ?
		ON CONFLICT(chat_id) DO UPDATE SET invalidated_at = excluded.invalidated_at`, t, chatID, userID)
	return err
}

// PurgeChatIndex removes everything searchable about the user's chat.
func PurgeChatIndex(ctx context.Context, db *sql.DB, userID, chatID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM search_docs WHERE chat_id = ? AND user_id = ?`, `DELETE FROM search_state WHERE chat_id = ? AND user_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, chatID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StaleChats returns up to limit of the user's ACTIVE chats whose index is
// missing or stale, most recently active first. A chat whose last read failed
// is skipped until retryBefore has passed; the zero time disables the skip.
func StaleChats(ctx context.Context, db *sql.DB, userID, onlyChat string, limit int, retryBefore time.Time) ([]Chat, error) {
	rb := ""
	if !retryBefore.IsZero() {
		rb = ts(retryBefore)
	}
	q := `SELECT ` + chatCols + ` FROM chats c LEFT JOIN search_state s ON s.chat_id = c.id AND s.user_id = c.user_id
		WHERE c.user_id = ? AND c.status = 'ACTIVE' AND (s.chat_id IS NULL OR s.indexed_at < s.invalidated_at OR s.indexed_at = '')
		AND (? = '' OR s.chat_id IS NULL OR s.failed_at = '' OR s.failed_at < ?)`
	args := []any{userID, rb, rb}
	if onlyChat != "" {
		q += ` AND c.id = ?`
		args = append(args, onlyChat)
	}
	q += ` ORDER BY c.updated_at DESC, c.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkIndexFailed records that the user's chat could not be read for indexing.
func MarkIndexFailed(ctx context.Context, db *sql.DB, userID, chatID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO search_state (chat_id, user_id, indexed_at, invalidated_at, failed_at)
		SELECT id, user_id, '', '', ? FROM chats WHERE id = ? AND user_id = ?
		ON CONFLICT(chat_id) DO UPDATE SET failed_at = excluded.failed_at`, ts(now), chatID, userID)
	return err
}

// IndexStatus counts the user's ACTIVE chats (in scope: one chat or all) and, of
// them, how many are pending (not indexed or stale; a later search will read
// them), failed (stale and the last read failed: retried after a backoff) and
// partial (cut short by a size or quota limit: waiting never fixes that).
func IndexStatus(ctx context.Context, db *sql.DB, userID, onlyChat string) (total, pending, failed, partial int, err error) {
	const stale = `(s.chat_id IS NULL OR s.indexed_at = '' OR s.indexed_at < s.invalidated_at)`
	q := `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN ` + stale + ` AND COALESCE(s.failed_at,'') = '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN ` + stale + ` AND COALESCE(s.failed_at,'') <> '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN NOT ` + stale + ` AND s.truncated = 1 THEN 1 ELSE 0 END), 0)
		FROM chats c LEFT JOIN search_state s ON s.chat_id = c.id AND s.user_id = c.user_id
		WHERE c.user_id = ? AND c.status = 'ACTIVE'`
	args := []any{userID}
	if onlyChat != "" {
		q += ` AND c.id = ?`
		args = append(args, onlyChat)
	}
	err = db.QueryRowContext(ctx, q, args...).Scan(&total, &pending, &failed, &partial)
	return
}

// SearchQuery is a search over one user's own index. Terms are already folded
// (Fold) and all must occur in a turn.
type SearchQuery struct {
	UserID string
	Terms  []string
	ChatID string // optional: only this chat (must still be the user's)
	Role   string // optional: "user" | "assistant" | "title"
	Limit  int
	After  *SearchCursor
}

// SearchCursor is the keyset position after the last hit of a page.
type SearchCursor struct {
	CreatedAt, ChatID string
	Seq               int
}

type SearchHit struct {
	ChatID        string
	ChatTitle     string
	ChatCreatedAt string
	Seq           int
	Role          string
	Body          string
	CreatedAt     string
}

// SearchChats returns hits from the user's own ACTIVE chats, newest-created chat
// first and, inside a chat, newest turn first. It asks for one extra row; that
// row is returned last and is not part of the page. Ownership is in the query:
// the chat row and the index row must both carry userID, so no row of another
// user can be reached however the terms are written. Terms are bound
// parameters to instr(), not patterns, so there is nothing to escape.
// Order is recency of creation (chat, then turn), not relevance: no score is claimed. The chat key is created_at because
// it never changes; updated_at moves when a run starts, which would move a chat across a live cursor. CROSS JOIN pins the
// chats index as the outer loop (left to right); left free, the planner drives
// from search_docs and sorts every match the user has. The plan then orders the
// chats for free and sorts only the turns inside one chat (EXPLAIN QUERY PLAN
// "USE TEMP B-TREE FOR LAST TERM OF ORDER BY"), bounded by one chat's size.
func SearchChats(ctx context.Context, db *sql.DB, q SearchQuery) (hits []SearchHit, more bool, err error) {
	if q.UserID == "" || len(q.Terms) == 0 {
		return nil, false, errors.New("store: search needs a user and at least one term")
	}
	sqlq := `SELECT c.id, COALESCE(c.title,''), c.created_at, d.seq, d.role, d.body, COALESCE(d.created_at,'')
		FROM chats c CROSS JOIN search_docs d ON d.chat_id = c.id AND d.user_id = c.user_id
		WHERE c.user_id = ? AND c.status = 'ACTIVE'`
	args := []any{q.UserID}
	if q.ChatID != "" {
		sqlq += ` AND c.id = ?`
		args = append(args, q.ChatID)
	}
	if q.Role != "" {
		sqlq += ` AND d.role = ?`
		args = append(args, q.Role)
	}
	for _, t := range q.Terms {
		sqlq += ` AND instr(d.folded, ?) > 0`
		args = append(args, t)
	}
	if q.After != nil {
		sqlq += ` AND (c.created_at, c.id, d.seq) < (?, ?, ?)`
		args = append(args, q.After.CreatedAt, q.After.ChatID, q.After.Seq)
	}
	sqlq += ` ORDER BY c.created_at DESC, c.id DESC, d.seq DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ChatID, &h.ChatTitle, &h.ChatCreatedAt, &h.Seq, &h.Role, &h.Body, &h.CreatedAt); err != nil {
			return nil, false, err
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(hits) > q.Limit {
		return hits[:q.Limit], true, nil
	}
	return hits, false, nil
}
