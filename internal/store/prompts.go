package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Prompt statuses (the CHECK constraint in 0013_run_prompts.sql is the source of truth).
const (
	PromptPending  = "PENDING"
	PromptSending  = "SENDING"
	PromptAnswered = "ANSWERED"
	PromptSettled  = "SETTLED"
)

const (
	PromptApproval = "approval"
	PromptClarify  = "clarify"
)

// ErrPromptNotPending is an owned prompt that is no longer waiting for an answer: it was
// answered, settled by Hermes, or is being answered by another request right now.
var ErrPromptNotPending = errors.New("store: the prompt is not waiting for an answer")

// Prompt is one decision Hermes is waiting for.
type Prompt struct {
	ID         string
	UserID     string
	RunID      string
	Kind       string
	HermesID   string // the exact upstream request id; only the server ever sends it back
	Generation *int64 // the upstream execution generation, when Hermes sent one
	Text       string
	Detail     string
	Choices    []string
	Status     string
	CreatedAt  string
}

const promptCols = `p.id, p.user_id, p.run_id, p.kind, p.hermes_request_id, p.execution_generation, p.text, p.detail, p.choices, p.status, p.created_at`

func scanPrompt(row interface{ Scan(...any) error }) (Prompt, error) {
	var p Prompt
	var gen sql.NullInt64
	var choices string
	if err := row.Scan(&p.ID, &p.UserID, &p.RunID, &p.Kind, &p.HermesID, &gen, &p.Text, &p.Detail, &choices, &p.Status, &p.CreatedAt); err != nil {
		return Prompt{}, err
	}
	if gen.Valid {
		p.Generation = &gen.Int64
	}
	_ = json.Unmarshal([]byte(choices), &p.Choices) // written by InsertPrompt; a bad value reads as no choices
	return p, nil
}

// InsertPrompt records a prompt Hermes raised on the user's own run. A repeat of the same upstream
// request (a replayed frame after a reattach) returns the existing row with created == false.
func InsertPrompt(ctx context.Context, db *sql.DB, p Prompt, now time.Time) (Prompt, bool, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	p.ID = hex.EncodeToString(b)
	choices, _ := json.Marshal(p.Choices)
	if p.Choices == nil {
		choices = []byte("[]")
	}
	var gen any
	if p.Generation != nil {
		gen = *p.Generation
	}
	res, err := db.ExecContext(ctx, `INSERT INTO run_prompts (id, user_id, run_id, kind, hermes_request_id, execution_generation, text, detail, choices, status, created_at)
		SELECT ?, r.user_id, r.id, ?, ?, ?, ?, ?, ?, 'PENDING', ? FROM runs r WHERE r.id = ? AND r.user_id = ?
		ON CONFLICT (run_id, hermes_request_id) DO NOTHING`,
		p.ID, p.Kind, p.HermesID, gen, p.Text, p.Detail, string(choices), ts(now), p.RunID, p.UserID)
	if err != nil {
		return Prompt{}, false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		p.Status, p.CreatedAt = PromptPending, ts(now)
		return p, true, nil
	}
	row := db.QueryRowContext(ctx, `SELECT `+promptCols+` FROM run_prompts p WHERE p.run_id = ? AND p.user_id = ? AND p.hermes_request_id = ?`, p.RunID, p.UserID, p.HermesID)
	existing, err := scanPrompt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Prompt{}, false, ErrNoChat // not the user's run
	}
	return existing, false, err
}

// OpenPrompts lists the prompts on the user's own run that still wait for an answer, oldest first.
// Ownership is in the query: another user's run id yields nothing.
func OpenPrompts(ctx context.Context, db *sql.DB, userID, runID string) ([]Prompt, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+promptCols+` FROM run_prompts p
		JOIN runs r ON r.id = p.run_id AND r.user_id = p.user_id
		WHERE p.user_id = ? AND p.run_id = ? AND p.status IN ('PENDING','SENDING')
		ORDER BY p.created_at, p.id`, userID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Prompt{}
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimPrompt moves the user's own PENDING prompt of the given kind to SENDING, so exactly one request
// sends the answer. A prompt that is not the caller's, not on that run or of another kind is ErrNoChat,
// the same as a missing one; an owned prompt that is no longer PENDING is ErrPromptNotPending.
func ClaimPrompt(ctx context.Context, db *sql.DB, userID, runID, promptID, kind string) (Prompt, error) {
	row := db.QueryRowContext(ctx, `UPDATE run_prompts SET status = 'SENDING'
		WHERE id = ? AND user_id = ? AND run_id = ? AND kind = ? AND status = 'PENDING'
		RETURNING `+strings.ReplaceAll(promptCols, "p.", ""), promptID, userID, runID, kind)
	p, err := scanPrompt(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Prompt{}, err
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM run_prompts p WHERE p.id = ? AND p.user_id = ? AND p.run_id = ? AND p.kind = ?`, promptID, userID, runID, kind).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Prompt{}, ErrNoChat
	}
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{}, ErrPromptNotPending
}

// FinishPrompt records the outcome of a claimed prompt: ANSWERED with the approval's choice (empty for a
// clarification, whose answer is not kept), SETTLED when Hermes no longer had it, or PENDING again when
// the answer could not be delivered and the user may retry.
func FinishPrompt(ctx context.Context, db *sql.DB, userID, promptID, to, decision string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE run_prompts SET status = ?, decision = ?, decided_at = ?
		WHERE id = ? AND user_id = ? AND status = 'SENDING'`, to, decision, ts(now), promptID, userID)
	return err
}

// SettlePrompts closes every open prompt of the user's run without an answer: the run reached an end, or
// Hermes said the decision is gone. It never reports a prompt as answered.
func SettlePrompts(ctx context.Context, db *sql.DB, userID, runID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE run_prompts SET status = 'SETTLED', decided_at = ?
		WHERE user_id = ? AND run_id = ? AND status IN ('PENDING','SENDING')`, ts(now), userID, runID)
	return err
}

// SettlePrompt closes one PENDING prompt by its upstream request id (Hermes' own "settled" frame). A
// prompt whose answer is already on its way (SENDING) is left to that request to finish.
func SettlePrompt(ctx context.Context, db *sql.DB, userID, runID, hermesID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE run_prompts SET status = 'SETTLED', decided_at = ?
		WHERE user_id = ? AND run_id = ? AND hermes_request_id = ? AND status = 'PENDING'`, ts(now), userID, runID, hermesID)
	return err
}
