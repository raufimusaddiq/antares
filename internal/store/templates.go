package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Personal slash templates (PRD §21.6, UXP-006, issue #107). A template is stored text
// and nothing else: the table has no command, tool or skill column, and the server
// never interprets the body. Names are unique per owner.

const (
	MaxTemplates    = 100
	MaxTemplateBody = 8 << 10
	MaxTemplateName = 32
	maxTemplateScan = MaxTemplates + 1
)

var (
	// ErrNoTemplate is a template that does not exist OR belongs to someone else.
	ErrNoTemplate = errors.New("store: no such template")
	// ErrTemplateName means the owner already has a template with that name.
	ErrTemplateName = errors.New("store: template name already used")
	// ErrTooManyTemplates means the owner is at MaxTemplates.
	ErrTooManyTemplates = errors.New("store: too many templates")
)

type Template struct {
	ID, UserID, Name, Body, CreatedAt, UpdatedAt string
}

const templateCols = `id, user_id, name, body, created_at, updated_at`

func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Body, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "UNIQUE") }

// ListTemplates returns the owner's templates by name.
func ListTemplates(ctx context.Context, db *sql.DB, userID string) ([]Template, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+templateCols+` FROM prompt_templates WHERE user_id = ? ORDER BY name LIMIT ?`, userID, maxTemplateScan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTemplate adds a template for the owner. The count check and the insert share one
// transaction, so concurrent creates cannot pass MaxTemplates.
func CreateTemplate(ctx context.Context, db *sql.DB, userID, name, body string, now time.Time) (Template, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_templates WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return Template{}, err
	}
	if n >= MaxTemplates {
		return Template{}, ErrTooManyTemplates
	}
	t := Template{ID: NewID(), UserID: userID, Name: name, Body: body, CreatedAt: ts(now), UpdatedAt: ts(now)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_templates (`+templateCols+`) VALUES (?,?,?,?,?,?)`,
		t.ID, t.UserID, t.Name, t.Body, t.CreatedAt, t.UpdatedAt); err != nil {
		if isUnique(err) {
			return Template{}, ErrTemplateName
		}
		return Template{}, err
	}
	return t, tx.Commit()
}

// UpdateTemplate changes the owner's template; ownership is in the WHERE clause.
func UpdateTemplate(ctx context.Context, db *sql.DB, userID, id, name, body string, now time.Time) (Template, error) {
	res, err := db.ExecContext(ctx, `UPDATE prompt_templates SET name = ?, body = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		name, body, ts(now), id, userID)
	if isUnique(err) {
		return Template{}, ErrTemplateName
	}
	if err != nil {
		return Template{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Template{}, ErrNoTemplate
	}
	t, err := scanTemplate(db.QueryRowContext(ctx, `SELECT `+templateCols+` FROM prompt_templates WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNoTemplate
	}
	return t, err
}

// DeleteTemplate removes the owner's template.
func DeleteTemplate(ctx context.Context, db *sql.DB, userID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM prompt_templates WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoTemplate
	}
	return nil
}
