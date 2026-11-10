package store

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

func TestWritingRulesMigrationUpgradesWithNullColumns(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	all := Migrations()
	before := fstest.MapFS{} // everything before 0029
	ents, err := fs.ReadDir(all, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() < "0029" {
			b, _ := fs.ReadFile(all, e.Name())
			before[e.Name()] = &fstest.MapFile{Data: b}
		}
	}
	if _, err := Migrate(ctx, db, before); err != nil {
		t.Fatal(err)
	}
	mkUser(t, db, "U", "USER", "APPROVED", "READY")
	mkBinding(t, db, "U", "u_p", "READY")
	if _, err := db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at)
		SELECT 'c1','U',id,'s','ACTIVE','t','t' FROM tenant_bindings WHERE user_id='U'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at) VALUES ('r1','U','c1','k','COMPLETED','t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	var wr *string
	if err := db.QueryRow(`SELECT writing_rules FROM runs WHERE id='r1'`).Scan(&wr); err != nil || wr != nil {
		t.Fatalf("existing run: %v %v", wr, err)
	}
	if on, err := WritingRulesEnabled(ctx, db); err != nil || !on {
		t.Fatalf("absent row must mean on: %v %v", on, err)
	}
}

func TestWritingRulesSwitchAuditsAndNeedsAdmin(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	mkUser(t, db, "admin", "ADMIN", "APPROVED", "READY")
	mkUser(t, db, "plain", "USER", "APPROVED", "READY")
	if err := SetWritingRules(ctx, db, "plain", false, "r", t0); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("a plain user changed the switch: %v", err)
	}
	if on, _ := WritingRulesEnabled(ctx, db); !on {
		t.Fatal("refused change still switched it off")
	}
	if err := SetWritingRules(ctx, db, "admin", false, "r1", t0); err != nil {
		t.Fatal(err)
	}
	if on, _ := WritingRulesEnabled(ctx, db); on {
		t.Fatal("not switched off")
	}
	if err := SetWritingRules(ctx, db, "admin", true, "r2", t0); err != nil {
		t.Fatal(err)
	}
	if on, _ := WritingRulesEnabled(ctx, db); !on {
		t.Fatal("not switched on")
	}
	var n int
	var detail string
	db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='writing_rules.set'`).Scan(&n)
	db.QueryRow(`SELECT detail FROM audit_log WHERE action='writing_rules.set' AND request_id='r1'`).Scan(&detail)
	if n != 2 || detail != "enabled=false" {
		t.Fatalf("audit rows %d, detail %q", n, detail)
	}
}

func TestWritingRulesCounts(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	mkUser(t, db, "U", "USER", "APPROVED", "READY")
	mkBinding(t, db, "U", "u_p", "READY")
	if _, err := db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at)
		SELECT 'c1','U',id,'s','ACTIVE','t','t' FROM tenant_bindings WHERE user_id='U'`); err != nil {
		t.Fatal(err)
	}
	var applied string
	if err := db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=29`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(tsLayout, applied)
	before, after := ts(at.Add(-time.Hour)), ts(at.Add(time.Hour))
	i := 0
	run := func(created string, wr any) {
		i++
		if _, err := db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at, writing_rules)
			VALUES (?,'U','c1',?,'COMPLETED',?,?,?)`, NewID(), string(rune('a'+i)), created, created, wr); err != nil {
			t.Fatal(err)
		}
	}
	run(after, "v1")
	run(after, "v1")
	run(after, "off")
	run(after, nil)  // expected, not attached: counts as without and not applied
	run(before, nil) // older than the migration: without, never blamed
	since := at.Add(-2 * time.Hour)
	c, err := WritingRulesCounts(ctx, db, since)
	if err != nil {
		t.Fatal(err)
	}
	if c.With != 2 || c.Without != 3 || c.NotApplied != 1 {
		t.Fatalf("counts %+v", c)
	}
}
