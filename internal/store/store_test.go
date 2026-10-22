package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
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
	if err := SchemaCurrent(ctx, db, set(t)); err != nil {
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
		VALUES ('chatA','A','bA','s1','ACTIVE','t','t')`)
	// B cannot hang a chat on A's binding, nor a run on A's chat.
	if _, err := db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at)
		VALUES ('chatX','B','bA','s2','ACTIVE','t','t')`); err == nil {
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
	if err := SchemaCurrent(ctx, db, set(t)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := SchemaCurrent(ctx, db, set(t)); err == nil {
		t.Fatal("readiness check must fail with the database closed")
	}
}

func TestSchemaCurrentFailsOnUnmigratedDB(t *testing.T) {
	db, _ := openTest(t)
	db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT, checksum TEXT, applied_at TEXT)`)
	if err := SchemaCurrent(context.Background(), db, set(t)); err == nil {
		t.Fatal("unmigrated database must not be ready")
	}
}

func TestOpenTightensExistingFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil { // defeat umask
		t.Fatal(err)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("existing database kept mode %v, want 0600", st.Mode().Perm())
	}
}

func TestOpenRejectsPathsTheDriverWouldRewrite(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a%20b.db", "a?b.db", "a#b.db"} {
		p := filepath.Join(dir, name)
		if db, err := Open(p); err == nil {
			db.Close()
			t.Errorf("%q should be rejected", name)
		}
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%q: a file was created before rejection", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("rejected paths left files behind: %v", entries)
	}
}

func TestOpenedFileIsTheSecuredFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "with space.db")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	if file != p {
		t.Fatalf("driver opened %q, pre-flight secured %q", file, p)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-wal") || strings.HasSuffix(e.Name(), "-shm") {
			continue
		}
		st, _ := e.Info()
		if e.Name() != "with space.db" || st.Mode().Perm() != 0o600 {
			t.Errorf("unexpected or loose file %s %v", e.Name(), st.Mode().Perm())
		}
	}
}

func TestSchemaCurrentFailsOnGap(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	m := fstest.MapFS{}
	for i, n := range []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"} {
		m[n] = &fstest.MapFile{Data: []byte(fmt.Sprintf("-- %d", i))}
	}
	db.Exec(`DELETE FROM schema_migrations`)
	for _, v := range []int{1, 3} { // 2 missing
		db.Exec(`INSERT INTO schema_migrations VALUES (?,?,?,?)`, v, "x", "y", "t")
	}
	if err := SchemaCurrent(ctx, db, setOf(t, m)); err == nil {
		t.Fatal("a gap below the maximum must not report ready")
	}
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestOpenSecuresPreExistingSidecars(t *testing.T) {
	// Simulate a restore of a crashed/hot-copied database: while a first handle
	// is live (so its -wal and -shm hold committed rows), copy all three files
	// to a fresh directory with loose modes, then Open the copy.
	src, sp := migrated(t)
	if _, err := src.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES ('u','i','s','plaintext-probe@example.test','USER','PENDING','UNPROVISIONED','t')`); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "antares.db")
	for _, suf := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(sp + suf)
		if err != nil {
			t.Fatalf("source %s missing: %v", suf, err)
		}
		if err := os.WriteFile(p+suf, b, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p+suf, 0o644)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var email string
	if err := db.QueryRow(`SELECT email FROM users WHERE id = 'u'`).Scan(&email); err != nil {
		t.Fatalf("committed row must survive Open (no WAL deletion): %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET display_name = 'x' WHERE id = 'u'`); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		if m := mode(t, p+suf); m != 0o600 {
			t.Errorf("antares.db%s has mode %v after Open and a write, want 0600", suf, m)
		}
	}
}

func TestOpenTightensExistingDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	os.Mkdir(dir, 0o755)
	os.Chmod(dir, 0o755)
	db, err := Open(filepath.Join(dir, "antares.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if m := mode(t, dir); m != 0o700 {
		t.Fatalf("data dir mode %v, want 0700", m)
	}
}

func TestOpenThroughSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.Mkdir(real, 0o700)
	linkDir := filepath.Join(root, "linkdir")
	os.Symlink(real, linkDir)

	// symlinked parent directory
	db, err := Open(filepath.Join(linkDir, "a.db"))
	if err != nil {
		t.Fatalf("symlinked data dir must start: %v", err)
	}
	db.Close()

	// symlinked db file
	tgt := filepath.Join(root, "dedicated")
	os.Mkdir(tgt, 0o700)
	os.WriteFile(filepath.Join(tgt, "b.db"), nil, 0o644)
	os.Chmod(filepath.Join(tgt, "b.db"), 0o644)
	home := filepath.Join(root, "linkhome")
	os.Mkdir(home, 0o700)
	os.Symlink(filepath.Join(tgt, "b.db"), filepath.Join(home, "b-link.db"))
	db, err = Open(filepath.Join(home, "b-link.db"))
	if err != nil {
		t.Fatalf("symlinked db file must start: %v", err)
	}
	db.Close()
	if m := mode(t, filepath.Join(tgt, "b.db")); m != 0o600 {
		t.Fatalf("symlink target mode %v, want 0600", m)
	}
}

func TestChatStatusVocabularyIsConstrained(t *testing.T) {
	db, _ := migrated(t)
	db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at) VALUES ('A','i','s','a@example.test','USER','APPROVED','READY','t')`)
	db.Exec(`INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id, credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at) VALUES ('bA','A','u_pa','MUX_PREFIX','ep',x'00',1,'fp','mem','READY','t')`)
	ins := func(status string) error {
		_, err := db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at) VALUES (?,?,?,?,?,'t','t')`,
			"c"+status, "A", "bA", "s"+status, status)
		return err
	}
	if err := ins("ACTIVE"); err != nil {
		t.Fatal(err)
	}
	if err := ins("whatever"); err == nil {
		t.Fatal("chats.status accepted an arbitrary string")
	}
}

func TestOpenRejectsRelativePathBeforeAnyChmod(t *testing.T) {
	cwd := t.TempDir()
	if err := os.Chmod(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	db, err := Open("antares.db")
	if err == nil {
		db.Close()
		t.Fatal("relative path must be refused")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error must name the real cause, got: %v", err)
	}
	if m := mode(t, cwd); m != 0o755 {
		t.Fatalf("cwd mode changed to %v: Open chmod-ed a directory it does not own", m)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("files created before refusal: %v", entries)
	}
}

func TestOpenRefusesFilesystemRoot(t *testing.T) {
	before := mode(t, "/")
	if db, err := Open("/antares-test.db"); err == nil {
		db.Close()
		os.Remove("/antares-test.db")
		t.Fatal("a database directly under / must be refused")
	}
	if mode(t, "/") != before {
		t.Fatal("root directory mode changed")
	}
	if _, err := os.Stat("/antares-test.db"); err == nil {
		t.Fatal("file created under /")
	}
}

func setOf(t *testing.T, f fs.FS) Set {
	t.Helper()
	s, err := LoadMigrations(f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func set(t *testing.T) Set { return setOf(t, Migrations()) }

func TestOpenRefusesSharedDirectoryWithoutTouchingIt(t *testing.T) {
	dir := t.TempDir() // stands in for /var/lib: not ours alone
	other := filepath.Join(dir, "other-service.conf")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(other, 0o644)
	os.Chmod(dir, 0o755)
	db, err := Open(filepath.Join(dir, "antares.db"))
	if err == nil {
		db.Close()
		t.Fatal("a directory holding another service's file must be refused")
	}
	if !strings.Contains(err.Error(), "dedicated") {
		t.Errorf("error should say the directory must be dedicated: %v", err)
	}
	if m := mode(t, dir); m != 0o755 {
		t.Errorf("shared directory mode changed to %v", m)
	}
	if m := mode(t, other); m != 0o644 {
		t.Errorf("other service's file mode changed to %v", m)
	}
	if b, _ := os.ReadFile(other); string(b) != "keep" {
		t.Error("other service's file was modified")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("Open left files in a directory it refused: %v", entries)
	}
}

func TestOpenAcceptsExistingDirHoldingOnlyItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	p := filepath.Join(dir, "antares.db")
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.WriteFile(p+suf, nil, 0o644)
	}
	db, err := Open(p)
	if err != nil {
		t.Fatalf("restore directory with only db files must be accepted: %v", err)
	}
	db.Close()
	if m := mode(t, dir); m != 0o700 {
		t.Fatalf("dir mode %v", m)
	}
}

func TestTimestampsSortChronologically(t *testing.T) {
	a := time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)
	b := a.Add(500 * time.Millisecond)
	c := a.Add(time.Second)
	if !(ts(a) < ts(b) && ts(b) < ts(c)) {
		t.Fatalf("string order differs from time order: %s %s %s", ts(a), ts(b), ts(c))
	}
}

func TestIdempotencyKeyScopedPerUser(t *testing.T) {
	db, _ := migrated(t)
	for _, u := range []string{"A", "B"} {
		db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at) VALUES (?,'i',?,?,'USER','APPROVED','READY','t')`, u, "s"+u, u+"@example.test")
		db.Exec(`INSERT INTO tenant_bindings (id, user_id, profile_name, routing_mode, upstream_endpoint_id, credential_ciphertext, credential_key_version, credential_fingerprint, memory_scope_key, status, created_at) VALUES (?,?,?,'MUX_PREFIX','ep',x'00',1,?,?,'READY','t')`, "b"+u, u, "u_"+u, "fp"+u, "m"+u)
		db.Exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at) VALUES (?,?,?,?,'ACTIVE','t','t')`, "c"+u, u, "b"+u, "s"+u)
	}
	run := func(id, user, chat string) error {
		_, err := db.Exec(`INSERT INTO runs (id, user_id, chat_id, idempotency_key, status, created_at, updated_at) VALUES (?,?,?,'same-key','PENDING','t','t')`, id, user, chat)
		return err
	}
	if err := run("r1", "A", "cA"); err != nil {
		t.Fatal(err)
	}
	if err := run("r2", "B", "cB"); err != nil {
		t.Fatalf("B reusing A's key must not collide (cross-user existence oracle): %v", err)
	}
	if err := run("r3", "A", "cA"); err == nil {
		t.Fatal("same user, same key must be refused")
	}
}

func TestOpenSymlinkedDBIntoSharedDirIsRefused(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	os.Mkdir(shared, 0o777)
	os.Chmod(shared, 0o777)
	other := filepath.Join(shared, "other-service.conf")
	os.WriteFile(other, []byte("keep"), 0o644)
	home := filepath.Join(root, "home")
	os.Mkdir(home, 0o700)
	os.Symlink(filepath.Join(shared, "antares.db"), filepath.Join(home, "antares.db"))
	db, err := Open(filepath.Join(home, "antares.db"))
	if err == nil {
		db.Close()
		t.Fatal("a symlink into a shared directory must be refused")
	}
	if m := mode(t, shared); m != 0o777 {
		t.Errorf("shared dir mode changed to %v", m)
	}
	if entries, _ := os.ReadDir(shared); len(entries) != 1 {
		t.Errorf("database created inside the shared directory: %v", entries)
	}
}

func TestOpenRefusesSymlinkedSidecar(t *testing.T) {
	for _, suf := range []string{"-wal", "-shm"} {
		dir := t.TempDir()
		victim := filepath.Join(dir, "victim.txt")
		os.WriteFile(victim, []byte("v"), 0o644)
		os.Chmod(victim, 0o644)
		p := filepath.Join(dir, "antares.db")
		os.WriteFile(p, nil, 0o600)
		os.Symlink(victim, p+suf)
		// victim.txt makes the dir non-dedicated, so exercise the sidecar guard
		// directly as well as through Open.
		if err := secureFiles(p); err == nil {
			t.Errorf("%s: secureFiles followed a symlinked sidecar", suf)
		}
		if m := mode(t, victim); m != 0o644 {
			t.Errorf("%s: victim mode changed to %v", suf, m)
		}
	}
	// Through Open, victim placed outside the data dir.
	root := t.TempDir()
	victim := filepath.Join(root, "victim.txt")
	os.WriteFile(victim, []byte("v"), 0o644)
	os.Chmod(victim, 0o644)
	dir := filepath.Join(root, "data")
	os.Mkdir(dir, 0o700)
	os.Symlink(victim, filepath.Join(dir, "antares.db-wal"))
	if db, err := Open(filepath.Join(dir, "antares.db")); err == nil {
		db.Close()
		t.Fatal("Open accepted a symlinked -wal")
	}
	if m := mode(t, victim); m != 0o644 {
		t.Fatalf("victim mode changed to %v", m)
	}
}

func TestOpenRejectsTrailingSeparator(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "sub") + "/", filepath.Join(dir, "sub") + "/."} {
		if db, err := Open(p); err == nil {
			db.Close()
			t.Errorf("%q should be rejected", p)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files created: %v", entries)
	}
}

func TestMigrateRefusesUnknownVersionBeforeTouchingAnything(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	m1 := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}
	if _, err := Migrate(ctx, db, m1); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO schema_migrations VALUES (5,'future','x','t')`)
	m12 := fstest.MapFS{"0001_a.sql": m1["0001_a.sql"], "0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)}}
	applied, err := Migrate(ctx, db, m12)
	if err == nil {
		t.Fatal("foreign version must be refused")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n)
	if n != 0 || len(applied) != 0 {
		t.Fatalf("DDL committed into a foreign-lineage database: table b=%d applied=%v", n, applied)
	}
	db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&n)
	if n != 0 {
		t.Fatal("version 2 recorded")
	}
}

func TestSecureFilesCoversRollbackJournal(t *testing.T) {
	// SQLite removes a rollback journal when it opens a WAL database, so the
	// exposure is the window before that; secureFiles runs first and must
	// cover every suffix requireDedicated allowlists.
	dir := t.TempDir()
	p := filepath.Join(dir, "antares.db")
	for _, suf := range []string{"", "-journal"} {
		os.WriteFile(p+suf, nil, 0o644)
		os.Chmod(p+suf, 0o644)
	}
	if err := secureFiles(p); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p+"-journal"); m != 0o600 {
		t.Fatalf("restored -journal kept mode %v, want 0600", m)
	}
	os.Remove(p + "-journal")
	os.Symlink(p, p+"-journal")
	if err := secureFiles(p); err == nil {
		t.Fatal("a symlinked -journal must be refused")
	}
}

func TestMigrateDetectsRenamedMigration(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, db, fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}); err != nil {
		t.Fatal(err)
	}
	renamed := fstest.MapFS{"0001_other.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}
	if _, err := Migrate(ctx, db, renamed); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("renamed migration must be refused: %v", err)
	}
}

func TestMigrationsMustUseFourDigitVersions(t *testing.T) {
	db, _ := openTest(t)
	for _, names := range [][]string{
		{"1_a.sql", "10_b.sql"}, {"001_a.sql"}, {"00001_a.sql"}, {"+001_a.sql"}, {"0001a.sql"},
	} {
		m := fstest.MapFS{}
		for _, n := range names {
			m[n] = &fstest.MapFile{Data: []byte(`CREATE TABLE t` + n[:1] + ` (id INTEGER);`)}
		}
		applied, err := Migrate(context.Background(), db, m)
		if err == nil || len(applied) != 0 {
			t.Errorf("%v: want refusal before applying anything, got applied=%v err=%v", names, applied, err)
		}
	}
	// Name order is version order for well-formed sets.
	ok := fstest.MapFS{
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)},
		"0010_c.sql": {Data: []byte(`CREATE TABLE c (id INTEGER);`)},
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)},
	}
	applied, err := Migrate(context.Background(), db, ok)
	if err != nil || fmt.Sprint(applied) != "[1 2 10]" {
		t.Fatalf("applied=%v err=%v, want [1 2 10]", applied, err)
	}
}

func TestSchemaCurrentFailsOnEditedMigration(t *testing.T) {
	db, _ := openTest(t)
	ctx := context.Background()
	orig := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)}}
	if _, err := Migrate(ctx, db, orig); err != nil {
		t.Fatal(err)
	}
	if err := SchemaCurrent(ctx, db, setOf(t, orig)); err != nil {
		t.Fatal(err)
	}
	edited := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER, x TEXT);`)}}
	if err := SchemaCurrent(ctx, db, setOf(t, edited)); err == nil {
		t.Fatal("an edited migration must not report ready")
	}
}
