package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type zeroRows struct{}

func (zeroRows) LastInsertId() (int64, error) { return 0, nil }
func (zeroRows) RowsAffected() (int64, error) { return 0, nil }

// A primary-key collision on id is not a name conflict; only UNIQUE (user_id, name) is.
func TestTemplateConstraintsAreToldApart(t *testing.T) {
	db, _ := migrated(t)
	u := mkUser(t, db, "ua", "USER", "APPROVED", "READY")
	ctx := context.Background()
	first, err := CreateTemplate(ctx, db, u, "one", "body", t0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO prompt_templates (id, user_id, name, body, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		first.ID, u, "another-name", "x", "t", "t")
	if err == nil || isNameTaken(err) {
		t.Fatalf("a duplicate id must be an error that is not a name conflict, got %v (isNameTaken %v)", err, isNameTaken(err))
	}
	if _, err = CreateTemplate(ctx, db, u, "one", "again", t0); !errors.Is(err, ErrTemplateName) {
		t.Fatalf("a duplicate name = %v, want ErrTemplateName", err)
	}
	second, err := CreateTemplate(ctx, db, u, "two", "b", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateTemplate(ctx, db, u, second.ID, "one", "b", t0); !errors.Is(err, ErrTemplateName) {
		t.Fatalf("renaming onto an existing name = %v, want ErrTemplateName", err)
	}
}

// A compare-and-set that matches no row must fail loudly, not be reported as saved.
func TestDraftCASRequiresExactlyOneRow(t *testing.T) {
	if err := oneRow(zeroRows{}, nil); !errors.Is(err, errCASMiss) {
		t.Fatalf("0 rows affected = %v, want errCASMiss", err)
	}
	if err := oneRow(nil, sql.ErrConnDone); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("a driver error passes through, got %v", err)
	}
}
