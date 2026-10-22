package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func openTest(t *testing.T) (*sql.DB, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data", "antares.db")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, p
}

func migrated(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, p := openTest(t)
	if _, err := Migrate(context.Background(), db, Migrations()); err != nil {
		t.Fatal(err)
	}
	return db, p
}

func TestOpenPragmasAndPoolBound(t *testing.T) {
	db, _ := openTest(t)
	var mode string
	var fk, busy int
	db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	if mode != "wal" || fk != 1 || busy != int(busyTimeout.Milliseconds()) {
		t.Fatalf("pragmas: %s %d %d", mode, fk, busy)
	}
	if got := db.Stats().MaxOpenConnections; got != maxConns {
		t.Fatalf("pool not bounded: %d", got)
	}
}

func TestMigrateFromEmptyThenIdempotent(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	first, err := Migrate(ctx, db, Migrations())
	if err != nil || len(first) == 0 {
		t.Fatalf("first run: %v %v", first, err)
	}
	t.Logf("first run applied: %v", first)
	second, err := Migrate(ctx, db, Migrations())
	if err != nil || len(second) != 0 {
		t.Fatalf("second run must apply nothing: %v %v", second, err)
	}
	t.Logf("second run applied: %v", second)
	if err := SchemaCurrent(ctx, db, Migrations()); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"users", "approvals", "tenant_bindings", "app_sessions", "chats", "runs", "messages", "audit_log"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Errorf("table %s: %v", tbl, err)
		}
	}
}

func TestFailedMigrationLeavesNoHalfAppliedSchema(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	good := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}
	if _, err := Migrate(ctx, db, good); err != nil {
		t.Fatal(err)
	}
	bad := fstest.MapFS{
		"0001_a.sql": good["0001_a.sql"],
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER); CREATE TABLE c (id INTEGER); INSERT INTO nope VALUES (1);`)},
	}
	if _, err := Migrate(ctx, db, bad); err == nil {
		t.Fatal("expected failure")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('b','c')`).Scan(&n)
	if n != 0 {
		t.Fatalf("half-applied schema: %d tables survived", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&n)
	if n != 0 {
		t.Fatal("failed migration was recorded")
	}
	// A fixed migration 2 applies cleanly afterwards.
	fixed := fstest.MapFS{"0001_a.sql": good["0001_a.sql"], "0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)}}
	if got, err := Migrate(ctx, db, fixed); err != nil || len(got) != 1 {
		t.Fatalf("retry: %v %v", got, err)
	}
}

func TestMigrateDetectsEditedAndUnknownVersions(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	m1 := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}
	if _, err := Migrate(ctx, db, m1); err != nil {
		t.Fatal(err)
	}
	edited := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER, extra TEXT);`)}}
	if _, err := Migrate(ctx, db, edited); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("edited migration must be refused: %v", err)
	}
	if _, err := Migrate(ctx, db, fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("db newer than binary must be refused: %v", err)
	}
}

func TestConcurrentMigrateFromTwoProcesses(t *testing.T) {
	_, p := openTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(p)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			_, err = Migrate(context.Background(), db, Migrations())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentAdminBootstrapYieldsOneAdmin(t *testing.T) {
	_, p := migrated(t)
	const n = 8
	var created int32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { // each goroutine has its own pool, like a separate process
			defer wg.Done()
			db, err := Open(p)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			<-start
			ok, err := BootstrapAdmin(context.Background(), db, "https://issuer.example.test", "sub-admin-1", "admin@example.test", time.Now())
			if ok {
				atomic.AddInt32(&created, 1)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	db, _ := Open(p)
	defer db.Close()
	var admins, latches, audits int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'ADMIN'`).Scan(&admins)
	db.QueryRow(`SELECT COUNT(*) FROM admin_bootstrap`).Scan(&latches)
	db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'admin.bootstrap'`).Scan(&audits)
	if admins != 1 || latches != 1 || audits != 1 || created != 1 {
		t.Fatalf("admins=%d latches=%d audits=%d winners=%d, want all 1", admins, latches, audits, created)
	}
}

func TestBootstrapIsOneShotEvenWithDifferentIdentity(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	if ok, err := BootstrapAdmin(ctx, db, "iss", "sub-1", "a@example.test", time.Now()); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	if ok, err := BootstrapAdmin(ctx, db, "iss", "sub-2", "b@example.test", time.Now()); ok || err != nil {
		t.Fatalf("second identity must not become admin: %v %v", ok, err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n != 1 {
		t.Fatalf("users=%d", n)
	}
}

func TestBootstrapPromotesExistingPendingUser(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES ('u1','iss','sub-1','a@example.test','USER','PENDING','UNPROVISIONED','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := BootstrapAdmin(ctx, db, "iss", "sub-1", "a@example.test", time.Now()); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	var role, st string
	db.QueryRow(`SELECT role, approval_status FROM users WHERE id='u1'`).Scan(&role, &st)
	if role != "ADMIN" || st != "APPROVED" {
		t.Fatalf("%s %s", role, st)
	}
}

func TestSchemaRefusesCrossUserOwnership(t *testing.T) {
	db, _ := migrated(t)
	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, u := range []string{"A", "B"} {
		mustExec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
			VALUES (?,'iss',?,?,'USER','APPROVED','READY','t')`, u, "sub"+u, u+"@example.test")
		mustExec(`INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id,
			credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at)
			VALUES (?,?,?,'MUX_PREFIX','ep',x'00',1,?,?,'READY','t')`, "b"+u, u, "u_profile"+u, "fp"+u, "mem"+u)
	}
	mustExec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at)
		VALUES ('chatA','A','bA','s1','open','t','t')`)
	// B cannot hang a chat on A's binding, nor a run on A's chat.
	if _, err := db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at)
		VALUES ('chatX','B','bA','s2','open','t','t')`); err == nil {
		t.Fatal("chat of B on A's binding was accepted")
	}
	if _, err := db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at)
		VALUES ('r1','B','chatA','k1','PENDING','t','t')`); err == nil {
		t.Fatal("run of B on A's chat was accepted")
	}
	// A key reused for another user is refused.
	if _, err := db.Exec(`INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id,
		credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at)
		VALUES ('bX','B','u_profileX','MUX_PREFIX','ep',x'00',1,'fpA','memX','READY','t')`); err == nil {
		t.Fatal("duplicate credential fingerprint was accepted")
	}
	// Status values stay three separate, constrained fields.
	if _, err := db.Exec(`UPDATE users SET approval_status = 'READY' WHERE id = 'A'`); err == nil {
		t.Fatal("approval_status accepted a tenant value")
	}
}

func TestSchemaCurrentFailsWhenDBUnreachable(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	if err := SchemaCurrent(ctx, db, Migrations()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := SchemaCurrent(ctx, db, Migrations()); err == nil {
		t.Fatal("readiness check must fail with the database closed")
	}
}

func TestSchemaCurrentFailsOnUnmigratedDB(t *testing.T) {
	db, _ := openTest(t)
	db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT, checksum TEXT, applied_at TEXT)`)
	if err := SchemaCurrent(context.Background(), db, Migrations()); err == nil {
		t.Fatal("unmigrated database must not be ready")
	}
}
