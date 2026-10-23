package tenant

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/store"
)

// All values are fabricated.
const (
	keyOne = "fabricated-tenant-key-one-0123456789abcdef"
	keyTwo = "fabricated-tenant-key-two-0123456789abcdef"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeHelper struct {
	mu        sync.Mutex
	provision func(slug string, call int) (hermes.Credential, error)
	rotate    func(slug string) (hermes.Credential, error)
	provCalls int
	rotCalls  int
	slugs     []string
}

func (f *fakeHelper) Provision(ctx context.Context, slug string) (hermes.Credential, error) {
	f.mu.Lock()
	f.provCalls++
	n := f.provCalls
	f.slugs = append(f.slugs, slug)
	fn := f.provision
	f.mu.Unlock()
	return fn(slug, n)
}

func (f *fakeHelper) Rotate(ctx context.Context, slug string) (hermes.Credential, error) {
	f.mu.Lock()
	f.rotCalls++
	fn := f.rotate
	f.mu.Unlock()
	if fn == nil {
		return hermes.Credential{}, errors.New("unexpected rotate")
	}
	return fn(slug)
}

func (f *fakeHelper) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.provCalls, f.rotCalls
}

func issues(key string) func(string, int) (hermes.Credential, error) {
	return func(slug string, _ int) (hermes.Credential, error) {
		return hermes.Credential{Profile: slug, Key: key}, nil
	}
}

type fakeVerifier struct {
	mu      sync.Mutex
	report  func(hermes.Target) hermes.Report
	targets []hermes.Target
}

func (v *fakeVerifier) Verify(ctx context.Context, t hermes.Target) hermes.Report {
	v.mu.Lock()
	v.targets = append(v.targets, t)
	fn := v.report
	v.mu.Unlock()
	return fn(t)
}

func pass(hermes.Target) hermes.Report {
	return hermes.Report{{Check: hermes.CheckEndpoint, Passed: true}, {Check: hermes.CheckToolsets, Passed: true}}
}

func failing(check, reason string) func(hermes.Target) hermes.Report {
	return func(hermes.Target) hermes.Report {
		return hermes.Report{{Check: hermes.CheckEndpoint, Passed: true}, {Check: check, Passed: false, Reason: reason}}
	}
}

type rig struct {
	t      *testing.T
	db     *sql.DB
	path   string
	box    *secrets.Box
	h      *fakeHelper
	v      *fakeVerifier
	clk    *clock
	svc    *Service
	admin  string
	nUsers int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "antares.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(context.Background(), db, store.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: db, path: path, box: box, clk: &clock{t: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)},
		h: &fakeHelper{provision: issues(keyOne), rotate: func(slug string) (hermes.Credential, error) {
			return hermes.Credential{Profile: slug, Key: keyTwo}, nil
		}}, v: &fakeVerifier{report: pass}}
	r.svc, err = New(Options{DB: db, Box: box, Helper: r.h, Verifier: r.v, Now: r.clk.Now, MaxAttempts: 3, BaseBackoff: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.svc.Close)
	r.admin = r.user("admin", "ADMIN", "APPROVED", "READY")
	return r
}

func (r *rig) user(id, role, approval, tenant string) string {
	r.t.Helper()
	_, err := r.db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, ?, ?, ?, ?)`, id, "sub-"+id, id+"@example.test", role, approval, tenant, r.clk.Now().Format("2006-01-02T15:04:05.000000000Z"))
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) approved(id string) string { return r.user(id, "USER", "APPROVED", "UNPROVISIONED") }

func (r *rig) view(id string) store.AdminUser {
	r.t.Helper()
	u, err := store.GetAdminUser(context.Background(), r.db, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

func (r *rig) count(q string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(q, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

var slugShape = regexp.MustCompile(`^u_[a-z0-9]{24}$`)

func TestApprovalProvisionsToReadyEndToEnd(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.TenantStatus != "READY" || got.BindingStatus != "READY" || got.BindingVerifiedAt == "" || got.TenantDetail != "" {
		t.Fatalf("got %+v", got)
	}
	if p, ro := r.h.counts(); p != 1 || ro != 0 || !slugShape.MatchString(r.h.slugs[0]) {
		t.Fatalf("helper calls = %d/%d slugs=%v", p, ro, r.h.slugs)
	}
	if strings.Contains(r.h.slugs[0], "u1") || strings.Contains(r.h.slugs[0], "example") {
		t.Fatalf("slug derives from user data: %s", r.h.slugs[0])
	}
	tg := r.v.targets[0]
	if tg.Profile != r.h.slugs[0] || tg.Key != keyOne || !tg.Fresh || len(tg.Siblings) != 0 {
		t.Fatalf("verifier target = %+v", tg)
	}
	// The key is sealed against its binding and opens only with its own id.
	cred, err := store.BindingCredential(context.Background(), r.db, u)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cred.Ciphertext, []byte(keyOne)) {
		t.Fatal("stored ciphertext contains the key")
	}
	if k, err := r.box.Open(cred.Ciphertext, cred.BindingID); err != nil || k != keyOne {
		t.Fatalf("open: %q %v", k, err)
	}
	if _, err := r.box.Open(cred.Ciphertext, "another-binding"); err == nil {
		t.Fatal("ciphertext opens under a different binding id")
	}
	for _, a := range []string{"tenant.provision", "tenant.bind", "tenant.ready"} {
		if r.count(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND target_id = ?`, a, u) != 1 {
			t.Errorf("missing audit row %s", a)
		}
	}
	// A second run finds nothing to do.
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.h.counts(); p != 1 {
		t.Fatal("a READY user was provisioned again")
	}
}

// No table and no file on disk ever holds the plaintext key or the helper token.
func TestPlaintextKeyNeverReachesTheDatabaseFiles(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	// A failed verification writes a detail and audit row too; none may hold the key.
	r.db.Exec(`UPDATE users SET tenant_status = 'CONFIGURING' WHERE id = ?`, u)
	r.db.Exec(`UPDATE tenant_bindings SET status = 'CONFIGURING' WHERE user_id = ?`, u)
	r.v.report = failing(hermes.CheckToolsets, "boundary-crossing toolset terminal is enabled")
	r.svc.Process(context.Background(), u)
	rows, err := r.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	for _, tb := range tables {
		rs, err := r.db.Query(`SELECT * FROM ` + tb)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rs.Scan(ptrs...)
			if strings.Contains(fmt.Sprint(vals...), keyOne) || bytes.Contains([]byte(fmt.Sprintf("%s", vals)), []byte(keyOne)) {
				t.Errorf("table %s holds the plaintext key", tb)
			}
		}
		rs.Close()
	}
	r.db.Exec(`PRAGMA wal_checkpoint(FULL)`)
	for _, suf := range []string{"", "-wal"} {
		if b, err := os.ReadFile(r.path + suf); err == nil && bytes.Contains(b, []byte(keyOne)) {
			t.Errorf("database file%s holds the plaintext key", suf)
		}
	}
}

func TestHelperOutageLeavesFailedWithReasonThenRecoversWithBackoff(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.h.provision = func(string, int) (hermes.Credential, error) {
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrRetryable, Detail: "provisioning helper unreachable"}
	}
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.TenantStatus != "FAILED" || got.TenantDetail != "provisioning helper unreachable" || got.TenantAttempts != 1 || got.BindingStatus != "" {
		t.Fatalf("got %+v", got)
	}
	// Not stuck CONFIGURING, and not retried inside the backoff window.
	if ids, _ := store.DueTenants(context.Background(), r.db, r.clk.Now(), 3, 10); len(ids) != 0 {
		t.Fatalf("due inside the backoff: %v", ids)
	}
	r.svc.Process(context.Background(), u)
	if p, _ := r.h.counts(); p != 1 {
		t.Fatalf("helper hit %d times inside the backoff", p)
	}
	// Helper recovers; after the backoff the sweep finds and completes it, with the SAME slug.
	r.h.provision = issues(keyOne)
	r.clk.Add(16 * time.Second)
	r.svc.sweep(context.Background())
	r.svc.wg.Wait()
	got = r.view(u)
	if got.TenantStatus != "READY" || got.TenantAttempts != 0 || got.TenantDetail != "" {
		t.Fatalf("after recovery: %+v", got)
	}
	if len(r.h.slugs) != 2 || r.h.slugs[0] != r.h.slugs[1] {
		t.Fatalf("slug changed across a retry: %v", r.h.slugs)
	}
}

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	r := newRig(t)
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := r.svc.backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestRetriesStopAfterTheBudgetAndAnAdminRequeueRestartsThem(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.h.provision = func(string, int) (hermes.Credential, error) {
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrRetryable, Detail: "provisioning helper failed"}
	}
	for i := 0; i < 3; i++ {
		r.clk.Add(11 * time.Minute)
		if err := r.svc.Process(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.view(u); got.TenantStatus != "FAILED" || got.TenantAttempts != 3 ||
		!strings.Contains(got.TenantDetail, "operator attention required: retry budget exhausted:") {
		t.Fatalf("got %+v", got)
	}
	if r.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'tenant.attention' AND target_id = ? AND outcome LIKE '%retry budget exhausted%'`, u) != 1 {
		t.Fatal("exhausted budget lacks operator signal")
	}
	before, _ := r.h.counts()
	r.clk.Add(24 * time.Hour)
	r.svc.sweep(context.Background())
	r.svc.wg.Wait()
	if after, _ := r.h.counts(); after != before {
		t.Fatalf("kept retrying after the budget: %d -> %d", before, after)
	}
	// The admin requeues; the helper is healthy now.
	r.h.provision = issues(keyOne)
	if _, err := store.RequeueTenant(context.Background(), r.db, r.admin, u, store.RequeueProvision, "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "READY" {
		t.Fatalf("after requeue: %+v", got)
	}
}

func TestPermanentHelperFailureNeedsAnOperatorAndExposesOnlyTheFixedReason(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.h.provision = func(string, int) (hermes.Credential, error) {
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrPermanent, Detail: "provisioning helper refused our token"}
	}
	r.svc.Process(context.Background(), u)
	got := r.view(u)
	if got.TenantStatus != "FAILED" || got.TenantDetail != "operator attention required: deterministic failure: provisioning helper refused our token" || got.TenantAttempts != 1 {
		t.Fatalf("got %+v", got)
	}
	if ids, _ := store.DueTenants(context.Background(), r.db, r.clk.Now().Add(100*time.Hour), 3, 10); len(ids) != 0 {
		t.Fatal("a permanent failure is retried automatically")
	}
	if r.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'tenant.attention' AND target_id = ?`, u) != 1 {
		t.Fatal("permanent helper failure lacks operator signal")
	}
	// A raw, unclassified error never reaches the stored reason.
	u2 := r.approved("u2")
	r.h.provision = func(string, int) (hermes.Credential, error) {
		return hermes.Credential{}, errors.New("docker exec failed: API_SERVER_KEY=" + keyTwo)
	}
	r.svc.Process(context.Background(), u2)
	if d := r.view(u2).TenantDetail; strings.Contains(d, keyTwo) || d != "provisioning helper failed" {
		t.Fatalf("detail = %q", d)
	}
}

func TestVerificationFailureNeverReachesReadyAndReusesTheStoredBinding(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.v.report = failing(hermes.CheckIsolation, "tenant key is accepted by another tenant's profile")
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.TenantStatus != "FAILED" || got.BindingStatus != "FAILED" || got.BindingVerifiedAt != "" ||
		!strings.Contains(got.TenantDetail, "credential_isolation") {
		t.Fatalf("got %+v", got)
	}
	if r.count(`SELECT COUNT(*) FROM tenant_bindings WHERE status = 'READY'`) != 0 {
		t.Fatal("a binding is READY after a failed verification")
	}
	// Rotation reapplies policy while preserving the binding identity.
	r.v.report = pass
	r.clk.Add(time.Minute)
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if p, ro := r.h.counts(); p != 1 || ro != 1 {
		t.Fatalf("helper called %d/%d times; retry must rotate the stored binding", p, ro)
	}
	if got := r.view(u); got.TenantStatus != "READY" || got.BindingStatus != "READY" {
		t.Fatalf("after retry: %+v", got)
	}
}

func TestEachVerificationCheckFailureBlocksReady(t *testing.T) {
	for _, check := range []string{hermes.CheckEndpoint, hermes.CheckIdentity, hermes.CheckIsolation, hermes.CheckSessions, hermes.CheckToolsets, hermes.CheckMemory} {
		r := newRig(t)
		u := r.approved("u1")
		r.v.report = failing(check, "x")
		r.svc.Process(context.Background(), u)
		got := r.view(u)
		if got.TenantStatus == "READY" || !strings.Contains(got.TenantDetail, check) {
			t.Errorf("%s: %+v", check, got)
		}
	}
	// An empty report (a verifier that checked nothing) is a failure too.
	r := newRig(t)
	u := r.approved("u1")
	r.v.report = func(hermes.Target) hermes.Report { return nil }
	r.svc.Process(context.Background(), u)
	if got := r.view(u); got.TenantStatus != "FAILED" || got.TenantDetail == "" {
		t.Fatalf("empty report: %+v", got)
	}
}

// The helper answered but the response never reached us (or the commit never
// happened): the profile has a key we do not hold. Recovery is a rotation.
func TestLostResponseRecoversByRotatingTheKey(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.h.provision = func(string, int) (hermes.Credential, error) { return hermes.Credential{}, hermes.ErrKeyExists }
	r.h.rotate = func(slug string) (hermes.Credential, error) {
		return hermes.Credential{Profile: slug, Key: keyTwo}, nil
	}
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
	if r.v.targets[0].Key != keyTwo {
		t.Fatal("verified with a key other than the rotated one")
	}
	if p, ro := r.h.counts(); p != 1 || ro != 1 {
		t.Fatalf("calls %d/%d", p, ro)
	}
	// If the rotation fails too, the user is FAILED, not READY and not stuck.
	r2 := newRig(t)
	u2 := r2.approved("u1")
	r2.h.provision = func(string, int) (hermes.Credential, error) { return hermes.Credential{}, hermes.ErrKeyExists }
	r2.h.rotate = func(string) (hermes.Credential, error) {
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrRetryable, Detail: "provisioning helper failed"}
	}
	r2.svc.Process(context.Background(), u2)
	if got := r2.view(u2); got.TenantStatus != "FAILED" {
		t.Fatalf("got %+v", got)
	}
}

func TestDuplicateKicksProvisionOnce(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.h.provision = func(slug string, _ int) (hermes.Credential, error) {
		once.Do(func() { close(started) })
		<-release
		return hermes.Credential{Profile: slug, Key: keyOne}, nil
	}
	for i := 0; i < 6; i++ {
		r.svc.Kick(u)
	}
	<-started
	for i := 0; i < 6; i++ {
		r.svc.Kick(u) // absorbed by the run in flight
	}
	close(release)
	r.svc.wg.Wait()
	if p, _ := r.h.counts(); p != 1 {
		t.Fatalf("helper called %d times for one user", p)
	}
	if got := r.view(u); got.TenantStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
	r.svc.Kick(u)
	r.svc.wg.Wait()
	if p, _ := r.h.counts(); p != 1 {
		t.Fatal("kicking a READY user provisioned again")
	}
}

func TestConcurrentProcessCallsNeverProvisionTwice(t *testing.T) {
	// Bypass Kick's in-process dedupe: the database claim alone must hold.
	r := newRig(t)
	u := r.approved("u1")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.svc.Process(context.Background(), u) }()
	}
	wg.Wait()
	if p, _ := r.h.counts(); p != 1 {
		t.Fatalf("helper called %d times", p)
	}
	if got := r.view(u); got.TenantStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
}

func TestSuspensionWhileTheHelperRunsLeavesTheTenantUnusable(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	in, release := make(chan struct{}), make(chan struct{})
	r.h.provision = func(slug string, _ int) (hermes.Credential, error) {
		close(in)
		<-release
		return hermes.Credential{Profile: slug, Key: keyOne}, nil
	}
	done := make(chan error, 1)
	go func() { done <- r.svc.Process(context.Background(), u) }()
	<-in
	if _, err := store.Decide(context.Background(), r.db, r.admin, u, store.ActionSuspend, "abuse", "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.ApprovalStatus != "SUSPENDED" || got.TenantStatus != "DISABLED" || got.BindingStatus != "DISABLED" {
		t.Fatalf("got %+v", got)
	}
	if len(r.v.targets) != 0 {
		t.Fatal("a suspended user's binding was verified")
	}
	if r.count(`SELECT COUNT(*) FROM tenant_bindings WHERE status = 'READY'`) != 0 {
		t.Fatal("a READY binding exists for a suspended user")
	}
}

func TestSuspensionWhileVerifyingCannotUnlock(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	in, release := make(chan struct{}), make(chan struct{})
	r.v.report = func(hermes.Target) hermes.Report { close(in); <-release; return pass(hermes.Target{}) }
	done := make(chan error, 1)
	go func() { done <- r.svc.Process(context.Background(), u) }()
	<-in
	if _, err := store.Decide(context.Background(), r.db, r.admin, u, store.ActionSuspend, "abuse", "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.TenantStatus != "DISABLED" || got.BindingStatus != "DISABLED" {
		t.Fatalf("got %+v", got)
	}
}

func TestReactivationRotatesAndReverifiesTheExistingBinding(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.svc.Process(context.Background(), u)
	store.Decide(context.Background(), r.db, r.admin, u, store.ActionSuspend, "abuse", "r", r.clk.Now())
	if _, err := store.Decide(context.Background(), r.db, r.admin, u, store.ActionReactivate, "", "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "CONFIGURING" {
		t.Fatalf("reactivated user must wait for verification: %+v", got)
	}
	if err := r.svc.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got := r.view(u)
	if got.TenantStatus != "READY" || got.BindingStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
	if p, ro := r.h.counts(); p != 1 || ro != 1 {
		t.Fatalf("reactivation helper calls = %d/%d", p, ro)
	}
	if r.v.targets[1].Fresh {
		t.Fatal("a re-verification was treated as a first one")
	}
}

func TestSecondTenantIsVerifiedAgainstTheFirstAsASibling(t *testing.T) {
	r := newRig(t)
	u1, u2 := r.approved("u1"), r.approved("u2")
	r.svc.Process(context.Background(), u1)
	r.h.provision = issues(keyTwo)
	r.svc.Process(context.Background(), u2)
	if len(r.v.targets) != 2 {
		t.Fatalf("targets %d", len(r.v.targets))
	}
	first, second := r.v.targets[0], r.v.targets[1]
	if len(second.Siblings) != 1 || second.Siblings[0] != first.Profile {
		t.Fatalf("siblings = %v, want [%s]", second.Siblings, first.Profile)
	}
	if first.Profile == second.Profile || first.Key == second.Key {
		t.Fatal("two tenants share a profile or key")
	}
}

func TestRotationRepairsAnUnreadableStoredCredential(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.v.report = failing(hermes.CheckEndpoint, "x")
	r.svc.Process(context.Background(), u)
	other, _ := secrets.New(bytes.Repeat([]byte{9}, 32)) // the master key changed
	svc, _ := New(Options{DB: r.db, Box: other, Helper: r.h, Verifier: r.v, Now: r.clk.Now, MaxAttempts: 3})
	defer svc.Close()
	r.clk.Add(time.Hour)
	r.v.report = pass
	svc.Process(context.Background(), u)
	got := r.view(u)
	if got.TenantStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
}

func TestOnlyApprovedUsersAreEverProvisioned(t *testing.T) {
	r := newRig(t)
	for i, ap := range []string{"PENDING", "REJECTED", "SUSPENDED"} {
		u := r.user(fmt.Sprintf("x%d", i), "USER", ap, "UNPROVISIONED")
		r.svc.Process(context.Background(), u)
		r.svc.Kick(u)
	}
	r.svc.sweep(context.Background())
	r.svc.wg.Wait()
	if p, _ := r.h.counts(); p != 0 {
		t.Fatalf("helper called %d times for users who are not approved", p)
	}
}

func TestSweepProvisionsTheBootstrapAdminWithoutAnAdminAPI(t *testing.T) {
	// The admin guard needs a READY admin; the first admin is APPROVED +
	// UNPROVISIONED at bootstrap, so the sweep is what makes the admin surface reachable.
	r := newRig(t)
	boot := r.user("boot", "ADMIN", "APPROVED", "UNPROVISIONED")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.svc.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for r.view(boot).TenantStatus != "READY" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got := r.view(boot); got.TenantStatus != "READY" {
		t.Fatalf("got %+v", got)
	}
}

func TestShutdownWaitsForRunsAndAnInterruptedRunResumesAfterItsLease(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	in := make(chan struct{})
	var inOnce sync.Once
	r.h.provision = func(slug string, _ int) (hermes.Credential, error) {
		inOnce.Do(func() { close(in) })
		time.Sleep(20 * time.Millisecond)
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrRetryable, Detail: "provisioning helper unreachable"}
	}
	r.svc.Kick(u)
	<-in
	r.svc.Close() // cancels and waits: must not return while the run is mid-flight
	r.svc.Kick(u) // refused after Close
	// Whatever state it ended in, it is not stuck: past the lease and backoff it is due again.
	got := r.view(u)
	if got.TenantStatus != "FAILED" && got.TenantStatus != "CONFIGURING" {
		t.Fatalf("got %+v", got)
	}
	if ids, _ := store.DueTenants(context.Background(), r.db, r.clk.Now().Add(time.Hour), 3, 10); len(ids) != 1 {
		t.Fatalf("an interrupted run is never resumed: %v (%+v)", ids, got)
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("accepted empty options")
	}
}

// A run is a helper call plus a verification; its budget must cover both,
// derived from the probe count and timeouts in internal/hermes. It covers one
// helper call, not the Provision-then-Rotate pair at full timeout: the 409 that
// triggers Rotate is a fast path, and a run cut at runBudget is retried via the
// lease (TestLeaseOutlastsRunBudget).
func TestRunBudgetCoversHelperAndProbes(t *testing.T) {
	need := hermes.HelperTimeout + hermes.MaxVerifyProbes*hermes.ProbeTimeout
	if runBudget <= need {
		t.Fatalf("runBudget %v must exceed helper %v + %d probes x %v = %v",
			runBudget, hermes.HelperTimeout, hermes.MaxVerifyProbes, hermes.ProbeTimeout, need)
	}
}

// A claimed run is bounded by runBudget, so a lease longer than that can never
// expire under a run that is still working.
func TestLeaseOutlastsRunBudget(t *testing.T) {
	if lease <= runBudget {
		t.Fatalf("lease %v must exceed runBudget %v", lease, runBudget)
	}
}

func TestRequeueRepairsToolsetPolicy(t *testing.T) {
	for _, mode := range []string{store.RequeueProvision, store.RequeueVerify} {
		t.Run(mode, func(t *testing.T) {
			r := newRig(t)
			u := r.approved("u1")
			policyApplied := false
			r.v.report = func(target hermes.Target) hermes.Report {
				if !policyApplied {
					return hermes.Report{{Check: hermes.CheckToolsets, Reason: "boundary-crossing toolset browser is enabled", Remediation: hermes.ReapplyToolsetPolicy}}
				}
				if target.Key != keyTwo {
					t.Fatal("verification used the old key")
				}
				return pass(target)
			}
			if err := r.svc.Process(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			r.h.rotate = func(slug string) (hermes.Credential, error) {
				policyApplied = true
				return hermes.Credential{Profile: slug, Key: keyTwo}, nil
			}
			if _, err := store.RequeueTenant(context.Background(), r.db, r.admin, u, mode, "r", r.clk.Now()); err != nil {
				t.Fatal(err)
			}
			if err := r.svc.Process(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if got := r.view(u); got.TenantStatus != "READY" || !policyApplied {
				t.Fatalf("after requeue: %+v, policy applied=%v", got, policyApplied)
			}
		})
	}
}

func TestDeterministicPolicyFailureStopsWithAttentionWithoutSpendingBudget(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	r.v.report = func(hermes.Target) hermes.Report {
		return hermes.Report{{Check: hermes.CheckToolsets, Reason: "boundary-crossing toolset browser is enabled", Remediation: hermes.ReapplyToolsetPolicy}}
	}
	for _, mode := range []string{"", store.RequeueVerify} {
		if mode != "" {
			if _, err := store.RequeueTenant(context.Background(), r.db, r.admin, u, mode, "r", r.clk.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.svc.Process(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		got := r.view(u)
		if got.TenantStatus != "FAILED" || got.BindingStatus != "FAILED" || got.TenantAttempts != 1 ||
			!strings.Contains(got.TenantDetail, "operator attention required: deterministic failure:") {
			t.Fatalf("got %+v", got)
		}
		if n := r.count(`SELECT COUNT(*) FROM users WHERE id = ? AND tenant_next_attempt_at IS NULL`, u); n != 1 {
			t.Fatal("deterministic failure scheduled a retry")
		}
		r.clk.Add(24 * time.Hour)
		if ids, err := store.DueTenants(context.Background(), r.db, r.clk.Now(), 3, 10); err != nil || len(ids) != 0 {
			t.Fatalf("due=%v err=%v", ids, err)
		}
		if err := r.svc.Process(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if p, ro := r.h.counts(); p != 1 || ro != 1 {
		t.Fatalf("unexpected automatic retries: %d/%d", p, ro)
	}
	if r.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'tenant.attention' AND target_id = ?`, u) != 2 {
		t.Fatal("deterministic stops lack audit signals")
	}
}

func TestLostRotationResponseRecoversOnRetry(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	ctx := context.Background()
	if err := r.svc.Process(ctx, u); err != nil {
		t.Fatal(err)
	}
	before, err := store.BindingCredential(ctx, r.db, u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueTenant(ctx, r.db, r.admin, u, store.RequeueVerify, "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.h.rotate = func(string) (hermes.Credential, error) {
		return hermes.Credential{}, &hermes.Failure{Class: hermes.ErrRetryable, Detail: "provisioning helper unreachable"}
	}
	if err := r.svc.Process(ctx, u); err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "FAILED" || got.TenantAttempts != 1 {
		t.Fatalf("lost response: %+v", got)
	}
	r.h.rotate = func(slug string) (hermes.Credential, error) {
		return hermes.Credential{Profile: slug, Key: keyTwo}, nil
	}
	r.clk.Add(time.Minute)
	if err := r.svc.Process(ctx, u); err != nil {
		t.Fatal(err)
	}
	after, err := store.BindingCredential(ctx, r.db, u)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "READY" || after.BindingID != before.BindingID || after.Profile != before.Profile {
		t.Fatalf("recovery: %+v", got)
	}
	if key, err := r.box.Open(after.Ciphertext, after.BindingID); err != nil || key != keyTwo {
		t.Fatal("rotated key was not sealed against the existing binding")
	}
}

func TestSuspensionDuringPolicyRepairCannotUnlock(t *testing.T) {
	r := newRig(t)
	u := r.approved("u1")
	ctx := context.Background()
	if err := r.svc.Process(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueTenant(ctx, r.db, r.admin, u, store.RequeueVerify, "r", r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.h.rotate = func(slug string) (hermes.Credential, error) {
		if _, err := store.Decide(ctx, r.db, r.admin, u, store.ActionSuspend, "test", "r", r.clk.Now()); err != nil {
			t.Fatal(err)
		}
		return hermes.Credential{Profile: slug, Key: keyTwo}, nil
	}
	if err := r.svc.Process(ctx, u); err != nil {
		t.Fatal(err)
	}
	if got := r.view(u); got.TenantStatus != "DISABLED" || got.BindingStatus != "DISABLED" {
		t.Fatalf("got %+v", got)
	}
	if len(r.v.targets) != 1 {
		t.Fatal("repair verified a suspended account")
	}
}
