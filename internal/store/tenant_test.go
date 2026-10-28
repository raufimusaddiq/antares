package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func mkUser(t *testing.T, db *sql.DB, id, role, approval, tenant string) string {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, ?, ?, ?, ?)`, id, "sub-"+id, id+"@example.test", role, approval, tenant, ts(t0)); err != nil {
		t.Fatal(err)
	}
	return id
}

func mkBinding(t *testing.T, db *sql.DB, user, profile, status string) string {
	t.Helper()
	id := NewID()
	if _, err := db.Exec(`INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,
		credential_key_version,credential_fingerprint,memory_scope_key,status,created_at)
		VALUES (?,?,?,'MUX_PREFIX','ep',?,1,?,?,?,?)`, id, user, profile, []byte("sealed-"+user), "fp-"+user, "mem-"+user, status, ts(t0)); err != nil {
		t.Fatal(err)
	}
	return id
}

func mkSession(t *testing.T, db *sql.DB, user, hash string) {
	t.Helper()
	if err := CreateSession(context.Background(), db, user, hash, t0, time.Hour); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func adminAndUser(t *testing.T, approval string) (db *sql.DB, admin, user string) {
	db, _ = migrated(t)
	admin = mkUser(t, db, "a1", "ADMIN", "APPROVED", "READY")
	user = mkUser(t, db, "u1", "USER", approval, "UNPROVISIONED")
	return
}

func status(t *testing.T, db *sql.DB, id string) (approval, tenant string) {
	t.Helper()
	if err := db.QueryRow(`SELECT approval_status, tenant_status FROM users WHERE id = ?`, id).Scan(&approval, &tenant); err != nil {
		t.Fatal(err)
	}
	return
}

func TestDecideApproveRecordsActorTimeAndAudit(t *testing.T) {
	db, admin, user := adminAndUser(t, "PENDING")
	got, err := Decide(context.Background(), db, admin, user, ActionApprove, "welcome", "req_1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalStatus != "APPROVED" || got.TenantStatus != "UNPROVISIONED" || got.ApprovedAt == "" {
		t.Fatalf("got %+v", got)
	}
	var a, from, to, reason, at string
	if err := db.QueryRow(`SELECT actor_user_id, from_status, to_status, reason, created_at FROM approvals WHERE target_user_id = ?`, user).
		Scan(&a, &from, &to, &reason, &at); err != nil {
		t.Fatal(err)
	}
	if a != admin || from != "PENDING" || to != "APPROVED" || reason != "welcome" || at != ts(t0) {
		t.Fatalf("approvals row = %s %s %s %q %s", a, from, to, reason, at)
	}
	var actor, action, outcome, req string
	if err := db.QueryRow(`SELECT actor_user_id, action, outcome, request_id FROM audit_log WHERE target_id = ?`, user).Scan(&actor, &action, &outcome, &req); err != nil {
		t.Fatal(err)
	}
	if actor != admin || action != "admin.approve" || outcome != "ok" || req != "req_1" {
		t.Fatalf("audit row = %s %s %s %s", actor, action, outcome, req)
	}
	var by string
	db.QueryRow(`SELECT approved_by FROM users WHERE id = ?`, user).Scan(&by)
	if by != admin {
		t.Fatalf("approved_by = %q", by)
	}
}

func TestDecideRejectAndSuspendNeedAReasonAndChangeNothingWithout(t *testing.T) {
	for _, tc := range []struct{ action, approval string }{{ActionReject, "PENDING"}, {ActionSuspend, "APPROVED"}} {
		for _, reason := range []string{"", "   \t "} {
			db, admin, user := adminAndUser(t, tc.approval)
			if _, err := Decide(context.Background(), db, admin, user, tc.action, reason, "r", t0); !errors.Is(err, ErrReason) {
				t.Fatalf("%s with reason %q: %v", tc.action, reason, err)
			}
			if a, _ := status(t, db, user); a != tc.approval {
				t.Fatalf("status changed to %s without a reason", a)
			}
			if count(t, db, `SELECT COUNT(*) FROM approvals`)+count(t, db, `SELECT COUNT(*) FROM audit_log`) != 0 {
				t.Fatal("a refused action left history rows")
			}
		}
	}
	db, admin, user := adminAndUser(t, "PENDING")
	for _, bad := range []string{strings.Repeat("x", 501), "line\nbreak", "nul\x00byte"} {
		if _, err := Decide(context.Background(), db, admin, user, ActionReject, bad, "r", t0); !errors.Is(err, ErrReasonInvalid) {
			t.Fatalf("reason %q: %v", bad, err)
		}
	}
}

func TestDecideTransitionTable(t *testing.T) {
	type tc struct {
		from, action, to string
		ok               bool
	}
	cases := []tc{
		{"PENDING", ActionApprove, "APPROVED", true}, {"REJECTED", ActionApprove, "APPROVED", true},
		{"PENDING", ActionReject, "REJECTED", true}, {"APPROVED", ActionSuspend, "SUSPENDED", true},
		{"SUSPENDED", ActionReactivate, "APPROVED", true},
		{"APPROVED", ActionApprove, "", false}, {"SUSPENDED", ActionApprove, "", false},
		{"APPROVED", ActionReject, "", false}, {"REJECTED", ActionReject, "", false}, {"SUSPENDED", ActionReject, "", false},
		{"PENDING", ActionSuspend, "", false}, {"REJECTED", ActionSuspend, "", false}, {"SUSPENDED", ActionSuspend, "", false},
		{"PENDING", ActionReactivate, "", false}, {"APPROVED", ActionReactivate, "", false}, {"REJECTED", ActionReactivate, "", false},
		{"APPROVED", "promote", "", false},
	}
	for _, c := range cases {
		db, admin, user := adminAndUser(t, c.from)
		_, err := Decide(context.Background(), db, admin, user, c.action, "because", "r", t0)
		got, _ := status(t, db, user)
		if c.ok {
			if err != nil || got != c.to {
				t.Errorf("%s --%s--> %s: err=%v got=%s", c.from, c.action, c.to, err, got)
			}
			continue
		}
		if !errors.Is(err, ErrConflict) || got != c.from {
			t.Errorf("%s --%s-->: err=%v status=%s (want conflict, unchanged)", c.from, c.action, err, got)
		}
		if count(t, db, `SELECT COUNT(*) FROM approvals`) != 0 {
			t.Errorf("%s --%s--> wrote history on a refusal", c.from, c.action)
		}
	}
}

func TestDecideAuthorizationIsEnforcedInTheDataLayer(t *testing.T) {
	db, _, user := adminAndUser(t, "PENDING")
	plain := mkUser(t, db, "u2", "USER", "APPROVED", "READY")
	suspendedAdmin := mkUser(t, db, "a2", "ADMIN", "SUSPENDED", "DISABLED")
	for name, actor := range map[string]string{"plain user": plain, "suspended admin": suspendedAdmin, "unknown actor": "nobody"} {
		if _, err := Decide(context.Background(), db, actor, user, ActionApprove, "", "r", t0); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if a, _ := status(t, db, user); a != "PENDING" {
		t.Fatal("an unauthorised actor changed a status")
	}
	if _, err := Decide(context.Background(), db, "a1", "a1", ActionSuspend, "x", "r", t0); !errors.Is(err, ErrSelf) {
		t.Fatalf("self suspend: %v", err)
	}
	if _, err := Decide(context.Background(), db, "a1", "ghost", ActionApprove, "", "r", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestSuspendRevokesSessionsAndDisablesTheBinding(t *testing.T) {
	db, admin, user := adminAndUser(t, "APPROVED")
	db.Exec(`UPDATE users SET tenant_status = 'READY' WHERE id = ?`, user)
	mkBinding(t, db, user, "u_aaaaaaaaaaaa", "READY")
	mkSession(t, db, user, "h1")
	mkSession(t, db, user, "h2")
	mkSession(t, db, admin, "adm")
	if _, err := Decide(context.Background(), db, admin, user, ActionSuspend, "abuse", "r", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionUser(context.Background(), db, "h1", t0); !errors.Is(err, ErrNoSession) {
		t.Fatalf("suspended user's session still valid: %v", err)
	}
	if _, err := SessionUser(context.Background(), db, "adm", t0); err != nil {
		t.Fatalf("the admin's own session was revoked: %v", err)
	}
	if _, tenant := status(t, db, user); tenant != "DISABLED" {
		t.Fatalf("tenant = %s", tenant)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tenant_bindings WHERE user_id = ? AND status = 'DISABLED'`, user); n != 1 {
		t.Fatal("binding not disabled")
	}
	u, _ := SessionUser(context.Background(), db, "adm", t0)
	_ = u
	// The admin route clears the pending job shutdown once the jobs are paused; this test has none to pause.
	if err := ClearJobShutdownPending(context.Background(), db, user); err != nil {
		t.Fatal(err)
	}
	// Reactivation keeps the credential but demands re-verification.
	got, err := Decide(context.Background(), db, admin, user, ActionReactivate, "", "r", t0)
	if err != nil || got.TenantStatus != "CONFIGURING" {
		t.Fatalf("reactivate: %+v %v", got, err)
	}
	// The binding is not usable until verified again: CONFIGURING, never READY.
	if n := count(t, db, `SELECT COUNT(*) FROM tenant_bindings WHERE user_id = ? AND status = 'READY'`, user); n != 0 {
		t.Fatal("reactivation made the binding READY without verification")
	}
}

func TestRejectRevokesSessionsAndReapprovalProvisions(t *testing.T) {
	db, admin, user := adminAndUser(t, "PENDING")
	mkSession(t, db, user, "h1")
	if _, err := Decide(context.Background(), db, admin, user, ActionReject, "not a member", "r", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionUser(context.Background(), db, "h1", t0); !errors.Is(err, ErrNoSession) {
		t.Fatal("rejected user's session still valid")
	}
	got, err := Decide(context.Background(), db, admin, user, ActionApprove, "", "r", t0)
	if err != nil || got.TenantStatus != "UNPROVISIONED" {
		t.Fatalf("re-approve: %+v %v", got, err)
	}
}

// race runs two Decide calls at the same instant and returns their errors.
func race(db *sql.DB, user string, x, y struct{ actor, action string }) [2]error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	var out [2]error
	for i, c := range []struct{ actor, action string }{x, y} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, out[i] = Decide(context.Background(), db, c.actor, user, c.action, "because", "r", t0)
		}()
	}
	close(start)
	wg.Wait()
	return out
}

type act = struct{ actor, action string }

// Two admins making the same decision: exactly one wins, the other is told the
// state moved, and history holds the winner's row only (PRD §3.5).
func TestTwoAdminsDecidingTheSameThingYieldOneWinner(t *testing.T) {
	for _, action := range []string{ActionApprove, ActionReject} {
		for i := 0; i < 15; i++ {
			db, a1, user := adminAndUser(t, "PENDING")
			a2 := mkUser(t, db, "a2", "ADMIN", "APPROVED", "READY")
			res := race(db, user, act{a1, action}, act{a2, action})
			wins := 0
			for _, e := range res {
				switch {
				case e == nil:
					wins++
				case !errors.Is(e, ErrConflict):
					t.Fatalf("%s: loser got %v, want ErrConflict", action, e)
				}
			}
			if wins != 1 {
				t.Fatalf("%s: %d winners: %v", action, wins, res)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM approvals WHERE target_user_id = ?`, user); n != 1 {
				t.Fatalf("%s: %d history rows, want the winner's only", action, n)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM audit_log WHERE target_id = ? AND action LIKE 'admin.%'`, user); n != 1 {
				t.Fatalf("%s: %d audit rows", action, n)
			}
		}
	}
}

// Approve and reject racing may serialise either way (a rejected user can be
// re-approved), but the history must always be a valid chain from PENDING that
// ends at the stored status: no lost update, no history that disagrees with state.
func TestRacingApproveAndRejectLeavesAConsistentChain(t *testing.T) {
	for i := 0; i < 20; i++ {
		db, a1, user := adminAndUser(t, "PENDING")
		a2 := mkUser(t, db, "a2", "ADMIN", "APPROVED", "READY")
		race(db, user, act{a1, ActionApprove}, act{a2, ActionReject})
		rows, err := db.Query(`SELECT from_status, to_status FROM approvals WHERE target_user_id = ? ORDER BY created_at, rowid`, user)
		if err != nil {
			t.Fatal(err)
		}
		cur := "PENDING"
		n := 0
		for rows.Next() {
			var f, to string
			rows.Scan(&f, &to)
			if f != cur {
				t.Fatalf("broken chain: at %s but a row says from %s", cur, f)
			}
			cur = to
			n++
		}
		rows.Close()
		if got, _ := status(t, db, user); got != cur || n == 0 {
			t.Fatalf("stored status %s, history ends at %s after %d rows", got, cur, n)
		}
	}
}

func TestClaimLeaseAndStableSlug(t *testing.T) {
	db, _, user := adminAndUser(t, "APPROVED")
	ctx := context.Background()
	c, err := ClaimTenant(ctx, db, user, "u_firstslug00001", t0, time.Minute, 5)
	if err != nil || c.Profile != "u_firstslug00001" || c.BindingID != "" {
		t.Fatalf("claim: %+v %v", c, err)
	}
	if _, err := ClaimTenant(ctx, db, user, "u_otherslug00002", t0.Add(30*time.Second), time.Minute, 5); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("a leased run was claimed twice: %v", err)
	}
	c2, err := ClaimTenant(ctx, db, user, "u_otherslug00002", t0.Add(2*time.Minute), time.Minute, 5)
	if err != nil || c2.Profile != "u_firstslug00001" {
		t.Fatalf("expired lease: %+v %v (the slug must not change)", c2, err)
	}
	if ids, _ := DueTenants(ctx, db, t0.Add(2*time.Minute), 5, 10); len(ids) != 0 {
		t.Fatalf("a leased user is due: %v", ids)
	}
	if ids, _ := DueTenants(ctx, db, t0.Add(10*time.Minute), 5, 10); len(ids) != 1 {
		t.Fatalf("an expired lease is not due: %v", ids)
	}
}

func TestOnlyApprovedUsersAreClaimable(t *testing.T) {
	for _, ap := range []string{"PENDING", "REJECTED", "SUSPENDED"} {
		db, _, user := adminAndUser(t, ap)
		if _, err := ClaimTenant(context.Background(), db, user, "u_slug000000001", t0, time.Minute, 5); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("%s user claimable: %v", ap, err)
		}
		if ids, _ := DueTenants(context.Background(), db, t0, 5, 10); len(ids) != 0 {
			t.Errorf("%s user is due: %v", ap, ids)
		}
	}
}

func TestFailedBacksOffAndStopsAfterTheBudget(t *testing.T) {
	db, _, user := adminAndUser(t, "APPROVED")
	ctx := context.Background()
	c, _ := ClaimTenant(ctx, db, user, "u_slug000000001", t0, time.Minute, 3)
	if err := MarkFailed(ctx, db, c, "provisioning helper unreachable", 1, t0.Add(15*time.Second), t0); err != nil {
		t.Fatal(err)
	}
	got, _ := GetAdminUser(ctx, db, user)
	if got.TenantStatus != "FAILED" || got.TenantDetail != "provisioning helper unreachable" || got.TenantAttempts != 1 {
		t.Fatalf("got %+v", got)
	}
	if ids, _ := DueTenants(ctx, db, t0.Add(5*time.Second), 3, 10); len(ids) != 0 {
		t.Fatal("retried before the backoff elapsed")
	}
	if _, err := ClaimTenant(ctx, db, user, "x", t0.Add(5*time.Second), time.Minute, 3); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("claimed before backoff: %v", err)
	}
	if ids, _ := DueTenants(ctx, db, t0.Add(20*time.Second), 3, 10); len(ids) != 1 {
		t.Fatal("not retried after the backoff")
	}
	c, err := ClaimTenant(ctx, db, user, "x", t0.Add(20*time.Second), time.Minute, 3)
	if err != nil || c.Attempts != 1 {
		t.Fatalf("reclaim: %+v %v", c, err)
	}
	MarkFailed(ctx, db, c, "again", 3, t0.Add(time.Hour), t0.Add(20*time.Second))
	if ids, _ := DueTenants(ctx, db, t0.Add(48*time.Hour), 3, 10); len(ids) != 0 {
		t.Fatal("kept retrying past the attempt budget")
	}
	if _, err := ClaimTenant(ctx, db, user, "x", t0.Add(48*time.Hour), time.Minute, 3); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("claimed past the budget: %v", err)
	}
	// An admin requeue resets the budget.
	admin := "a1"
	if _, err := RequeueTenant(ctx, db, admin, user, RequeueProvision, "r", t0); err != nil {
		t.Fatal(err)
	}
	if ids, _ := DueTenants(ctx, db, t0, 3, 10); len(ids) != 1 {
		t.Fatal("requeue did not make the user due")
	}
}

func TestSuspensionDuringARunSupersedesIt(t *testing.T) {
	ctx := context.Background()
	db, admin, user := adminAndUser(t, "APPROVED")
	c, _ := ClaimTenant(ctx, db, user, "u_slug000000001", t0, time.Minute, 5)
	if _, err := Decide(ctx, db, admin, user, ActionSuspend, "abuse", "r", t0); err != nil {
		t.Fatal(err)
	}
	// The helper answered after the suspension: the credential is kept, disabled.
	b := NewBinding{ID: NewID(), Endpoint: "ep", Ciphertext: []byte("x"), KeyVersion: 1, Fingerprint: "fp1", MemoryScopeKey: "m1"}
	if err := AttachBinding(ctx, db, c, b, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("attach: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tenant_bindings WHERE user_id = ? AND status = 'DISABLED'`, user); n != 1 {
		t.Fatal("late credential was not stored DISABLED")
	}
	if err := MarkReady(ctx, db, c, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("MarkReady after suspend: %v", err)
	}
	if err := MarkFailed(ctx, db, c, "x", 1, t0, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("MarkFailed after suspend: %v", err)
	}
	if _, tenant := status(t, db, user); tenant != "DISABLED" {
		t.Fatalf("tenant = %s", tenant)
	}
}

func TestMarkReadyIsTheOnlyPathToReadyAndNeedsTheBinding(t *testing.T) {
	ctx := context.Background()
	db, _, user := adminAndUser(t, "APPROVED")
	c, _ := ClaimTenant(ctx, db, user, "u_slug000000001", t0, time.Minute, 5)
	// No binding row yet: READY must be refused and nothing may change.
	if err := MarkReady(ctx, db, c, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("MarkReady with no binding: %v", err)
	}
	if _, tenant := status(t, db, user); tenant != "CONFIGURING" {
		t.Fatalf("tenant = %s after a refused MarkReady", tenant)
	}
	b := NewBinding{ID: NewID(), Endpoint: "ep", Ciphertext: []byte("x"), KeyVersion: 1, Fingerprint: "fp1", MemoryScopeKey: "m1"}
	if err := AttachBinding(ctx, db, c, b, t0); err != nil {
		t.Fatal(err)
	}
	if err := MarkReady(ctx, db, c, t0); err != nil {
		t.Fatal(err)
	}
	got, _ := GetAdminUser(ctx, db, user)
	if got.TenantStatus != "READY" || got.BindingStatus != "READY" || got.BindingVerifiedAt == "" {
		t.Fatalf("got %+v", got)
	}
	// The same run cannot mark READY twice.
	if err := MarkReady(ctx, db, c, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("second MarkReady: %v", err)
	}
}

// A policy saved while a run is in flight skips that (CONFIGURING) account, so MarkReady itself must refuse a run
// that applied an older policy: atomically, in the READY transaction, whenever the save lands before it.
func TestMarkReadyRefusesARunThatAppliedAnOlderToolPolicy(t *testing.T) {
	ctx := context.Background()
	db, admin, user := adminAndUser(t, "APPROVED")
	if _, err := SetTenantToolPolicy(ctx, db, admin, []string{"web"}, "r", t0); err != nil {
		t.Fatal(err)
	}
	_, applied, err := TenantToolPolicy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := ClaimTenant(ctx, db, user, "u_slug000000001", t0, time.Minute, 5)
	c.ToolPolicy = applied
	b := NewBinding{ID: NewID(), Endpoint: "ep", Ciphertext: []byte("x"), KeyVersion: 1, Fingerprint: "fp1", MemoryScopeKey: "m1"}
	if err := AttachBinding(ctx, db, c, b, t0); err != nil {
		t.Fatal(err)
	}
	// The admin saves after the run's verification, before its MarkReady. The account is CONFIGURING, so not requeued.
	ids, err := SetTenantToolPolicy(ctx, db, admin, []string{"web", "tts"}, "r", t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == user {
			t.Fatalf("an in-flight account was requeued: %v", ids)
		}
	}
	if err := MarkReady(ctx, db, c, t0); !errors.Is(err, ErrPolicyChanged) {
		t.Fatalf("MarkReady on the old policy: %v", err)
	}
	got, _ := GetAdminUser(ctx, db, user)
	if got.TenantStatus != "CONFIGURING" || got.BindingStatus != "CONFIGURING" {
		t.Fatalf("a refused MarkReady changed the account: %+v", got)
	}
	_, current, _ := TenantToolPolicy(ctx, db)
	c.ToolPolicy = current
	if err := MarkReady(ctx, db, c, t0); err != nil {
		t.Fatalf("MarkReady on the current policy: %v", err)
	}
}

func TestDuplicateCredentialAcrossUsersIsRefused(t *testing.T) {
	ctx := context.Background()
	db, _, u1 := adminAndUser(t, "APPROVED")
	u2 := mkUser(t, db, "u2", "USER", "APPROVED", "UNPROVISIONED")
	c1, _ := ClaimTenant(ctx, db, u1, "u_slug000000001", t0, time.Minute, 5)
	c2, _ := ClaimTenant(ctx, db, u2, "u_slug000000002", t0, time.Minute, 5)
	mk := func() NewBinding {
		return NewBinding{ID: NewID(), Endpoint: "ep", Ciphertext: []byte("x"), KeyVersion: 1, Fingerprint: "same-key", MemoryScopeKey: NewID()}
	}
	if err := AttachBinding(ctx, db, c1, mk(), t0); err != nil {
		t.Fatal(err)
	}
	if err := AttachBinding(ctx, db, c2, mk(), t0); err == nil || errors.Is(err, ErrSuperseded) {
		t.Fatalf("the same key was bound to two users: %v", err)
	}
}

func TestRequeueRules(t *testing.T) {
	ctx := context.Background()
	db, admin, user := adminAndUser(t, "APPROVED")
	if _, err := RequeueTenant(ctx, db, admin, user, RequeueVerify, "r", t0); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("verify without binding: %v", err)
	}
	mkBinding(t, db, user, "u_aaaaaaaaaaaa", "READY")
	db.Exec(`UPDATE users SET tenant_status = 'READY' WHERE id = ?`, user)
	got, err := RequeueTenant(ctx, db, admin, user, RequeueVerify, "r", t0)
	if err != nil || got.TenantStatus != "CONFIGURING" || got.BindingStatus != "CONFIGURING" {
		t.Fatalf("verify: %+v %v", got, err)
	}
	for _, ap := range []string{"PENDING", "REJECTED", "SUSPENDED"} {
		db.Exec(`UPDATE users SET approval_status = ? WHERE id = ?`, ap, user)
		if _, err := RequeueTenant(ctx, db, admin, user, RequeueProvision, "r", t0); !errors.Is(err, ErrConflict) {
			t.Errorf("requeue of a %s user: %v", ap, err)
		}
	}
	db.Exec(`UPDATE users SET approval_status = 'APPROVED', tenant_status = 'DISABLED' WHERE id = ?`, user)
	if _, err := RequeueTenant(ctx, db, admin, user, RequeueProvision, "r", t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("requeue of a DISABLED tenant: %v", err)
	}
	plain := mkUser(t, db, "u9", "USER", "APPROVED", "READY")
	if _, err := RequeueTenant(ctx, db, plain, user, RequeueProvision, "r", t0); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin requeue: %v", err)
	}
}

func TestOtherReadyProfilesExcludesTheCallerAndUnreadyBindings(t *testing.T) {
	ctx := context.Background()
	db, _, u1 := adminAndUser(t, "APPROVED")
	u2 := mkUser(t, db, "u2", "USER", "APPROVED", "READY")
	u3 := mkUser(t, db, "u3", "USER", "APPROVED", "CONFIGURING")
	mkBinding(t, db, u1, "u_own000000001", "READY")
	mkBinding(t, db, u2, "u_peer00000002", "READY")
	mkBinding(t, db, u3, "u_peer00000003", "CONFIGURING")
	got, err := OtherReadyProfiles(ctx, db, u1, 3)
	if err != nil || !reflect.DeepEqual(got, []string{"u_peer00000002"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

// The admin read model has no field that could carry a profile, credential or
// content: the guard is structural, so a later edit that adds one fails here.
func TestAdminUserHasNoSecretOrContentFields(t *testing.T) {
	typ := reflect.TypeOf(AdminUser{})
	for i := 0; i < typ.NumField(); i++ {
		n := strings.ToLower(typ.Field(i).Name)
		for _, bad := range []string{"profile", "credential", "key", "secret", "token", "cipher", "memory", "chat", "message", "content", "slug"} {
			if strings.Contains(n, bad) {
				t.Errorf("AdminUser.%s looks like a secret or content field", typ.Field(i).Name)
			}
		}
	}
}

func TestListAdminUsersFiltersAndPages(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	for i, ap := range []string{"PENDING", "APPROVED", "PENDING", "REJECTED"} {
		id := mkUser(t, db, "u"+string(rune('a'+i)), "USER", ap, "UNPROVISIONED")
		db.Exec(`UPDATE users SET created_at = ? WHERE id = ?`, ts(t0.Add(time.Duration(i)*time.Minute)), id)
	}
	pend, err := ListAdminUsers(ctx, db, "PENDING", "", 50, 0)
	if err != nil || len(pend) != 2 || pend[0].ID != "ua" || pend[1].ID != "uc" {
		t.Fatalf("pending = %+v %v", pend, err)
	}
	all, _ := ListAdminUsers(ctx, db, "", "", 2, 1)
	if len(all) != 2 || all[0].ID != "ub" {
		t.Fatalf("page = %+v", all)
	}
	if _, err := GetAdminUser(ctx, db, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestListAdminUsersSearchMatchesIdentityOnlyAndEscapesWildcards(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	a := mkUser(t, db, "ua", "USER", "PENDING", "UNPROVISIONED")
	b := mkUser(t, db, "ub", "USER", "APPROVED", "UNPROVISIONED")
	db.Exec(`UPDATE users SET email = 'Sari.Wulan@example.test', display_name = 'Sari' WHERE id = ?`, a)
	db.Exec(`UPDATE users SET email = 'budi_100%@example.test', display_name = '' WHERE id = ?`, b)
	got := func(approval, q string) []string {
		us, err := ListAdminUsers(ctx, db, approval, q, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, u := range us {
			ids = append(ids, u.ID)
		}
		return ids
	}
	if g := got("", "SARI"); !reflect.DeepEqual(g, []string{"ua"}) {
		t.Fatalf("case-insensitive name/email match = %v", g)
	}
	if g := got("", "_100%"); !reflect.DeepEqual(g, []string{"ub"}) {
		t.Fatalf("wildcards must be literal, got %v", g)
	}
	if g := got("", "_"); !reflect.DeepEqual(g, []string{"ub"}) {
		t.Fatalf("a lone _ is a literal underscore, got %v", g)
	}
	// Non-ASCII names: SQLite's LOWER() folds ASCII only, so the fold must be the same on both sides.
	c := mkUser(t, db, "uc", "USER", "APPROVED", "UNPROVISIONED")
	db.Exec(`UPDATE users SET email = 'unal@example.test', display_name = 'Ünal Öztürk' WHERE id = ?`, c)
	for _, q := range []string{"Ünal", "ünal", "ÜNAL", "Öztürk", "öztürk", "ÖZTÜRK"} {
		if g := got("", q); !reflect.DeepEqual(g, []string{"uc"}) {
			t.Fatalf("q=%q = %v, want the account displayed as %q", q, g, "Ünal Öztürk")
		}
	}
	if g := got("PENDING", "budi"); len(g) != 0 {
		t.Fatalf("search must combine with the approval filter, got %v", g)
	}
}

// The feed is written to while an admin reads it. Offset paging over a
// newest-first list repeats and skips rows when that happens; keyset paging
// must not.
func TestListAuditKeysetPagingSurvivesNewRows(t *testing.T) {
	ctx := context.Background()
	db, _ := migrated(t)
	put := func(id string, at time.Time) {
		if _, err := db.Exec(`INSERT INTO audit_log (id, action, target_kind, target_id, outcome, created_at) VALUES (?, 'admin.approve', 'user', 'x', 'ok', ?)`, id, ts(at)); err != nil {
			t.Fatal(err)
		}
	}
	// Five rows, two sharing a timestamp: the id breaks the tie.
	put("e1", t0)
	put("e2", t0.Add(time.Minute))
	put("e3", t0.Add(2*time.Minute))
	put("e4", t0.Add(2*time.Minute))
	put("e5", t0.Add(3*time.Minute))

	p1, more, err := ListAudit(ctx, db, 2, "", "")
	if err != nil || !more || len(p1) != 2 || p1[0].ID != "e5" || p1[1].ID != "e4" {
		t.Fatalf("page 1 = %+v more=%v %v", p1, more, err)
	}
	// A new entry lands after page 1 was read.
	put("e6", t0.Add(4*time.Minute))
	p2, more, err := ListAudit(ctx, db, 2, p1[1].CreatedAt, p1[1].ID)
	if err != nil || !more || len(p2) != 2 || p2[0].ID != "e3" || p2[1].ID != "e2" {
		t.Fatalf("page 2 = %+v more=%v %v (an offset would have repeated e4)", p2, more, err)
	}
	p3, more, err := ListAudit(ctx, db, 2, p2[1].CreatedAt, p2[1].ID)
	if err != nil || more || len(p3) != 1 || p3[0].ID != "e1" {
		t.Fatalf("page 3 = %+v more=%v %v", p3, more, err)
	}
	if fresh, _, _ := ListAudit(ctx, db, 2, "", ""); fresh[0].ID != "e6" {
		t.Fatalf("a fresh first page must show the new entry, got %+v", fresh)
	}
}

// Both the newest page and a cursor page must be served in index order: no in-memory sort.
func TestListAuditPagesUseTheKeysetIndex(t *testing.T) {
	db, _ := migrated(t)
	for name, q := range map[string]string{
		"first page":  `SELECT id FROM audit_log ORDER BY created_at DESC, id DESC LIMIT 51`,
		"cursor page": `SELECT id FROM audit_log WHERE (created_at < 'x' OR (created_at = 'x' AND id < 'y')) ORDER BY created_at DESC, id DESC LIMIT 51`,
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, d)
		}
		rows.Close()
		if joined := strings.Join(plan, "; "); strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "audit_log_keyset") {
			t.Errorf("%s plan = %s: want the keyset index and no temp b-tree", name, joined)
		}
	}
}

func TestRotatedCredentialIsOwnerScopedAndCannotReplaceReadyOrSuspendedBinding(t *testing.T) {
	ctx := context.Background()
	db, admin, u1 := adminAndUser(t, "APPROVED")
	u2 := mkUser(t, db, "u2", "USER", "APPROVED", "UNPROVISIONED")
	c1, err := ClaimTenant(ctx, db, u1, "u_slug000000001", t0, time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := ClaimTenant(ctx, db, u2, "u_slug000000002", t0, time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	c1.BindingID = mkBinding(t, db, u1, c1.Profile, "CONFIGURING")
	c2.BindingID = mkBinding(t, db, u2, c2.Profile, "CONFIGURING")
	b := NewBinding{ID: c1.BindingID, Ciphertext: []byte("rotated"), KeyVersion: 1, Fingerprint: "rotated-fp"}
	wrong := c1
	wrong.BindingID = c2.BindingID
	other := b
	other.ID = c2.BindingID
	if err := ReplaceBindingCredential(ctx, db, wrong, other, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("cross-owner replacement: %v", err)
	}
	cred, err := BindingCredential(ctx, db, u2)
	if err != nil || string(cred.Ciphertext) != "sealed-"+u2 {
		t.Fatalf("other tenant's credential changed: %v", err)
	}
	for _, id := range []string{"", c2.BindingID} {
		mismatched := b
		mismatched.ID = id
		if err := ReplaceBindingCredential(ctx, db, c1, mismatched, t0); !errors.Is(err, ErrConflict) {
			t.Fatalf("mismatched sealing AAD accepted: %v", err)
		}
	}
	if count(t, db, `SELECT COUNT(*) FROM audit_log WHERE action = 'tenant.rotate' AND target_id = ?`, u1) != 0 {
		t.Fatal("refused replacement recorded a successful rotation")
	}
	if err := ReplaceBindingCredential(ctx, db, c1, b, t0); err != nil {
		t.Fatal(err)
	}
	if count(t, db, `SELECT COUNT(*) FROM audit_log WHERE action = 'tenant.rotate' AND target_id = ?`, u1) != 1 {
		t.Fatal("missing rotation audit")
	}
	if err := MarkReady(ctx, db, c1, t0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBindingCredential(ctx, db, c1, b, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("READY replacement: %v", err)
	}
	if _, err := Decide(ctx, db, admin, u2, ActionSuspend, "test", "r", t0); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBindingCredential(ctx, db, c2, other, t0); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("suspended replacement: %v", err)
	}
}

// #169: the wire capture records only profiles of synthetic smoke accounts, never a real user's.
func TestIsSyntheticProfile(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	real := mkUser(t, db, "ureal", "USER", "APPROVED", "READY")
	mkBinding(t, db, real, "u_realrealre", "READY")
	syn := mkUser(t, db, "usyn", "USER", "APPROVED", "READY")
	mkBinding(t, db, syn, "u_synthetics", "READY")
	if _, err := db.Exec(`UPDATE users SET google_issuer = ?, tenant_profile = 'u_synthetics' WHERE id = ?`, SyntheticIssuer, syn); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET tenant_profile = 'u_realrealre' WHERE id = ?`, real); err != nil {
		t.Fatal(err)
	}
	for profile, want := range map[string]bool{"u_synthetics": true, "u_realrealre": false, "u_unknown123": false, "": false} {
		if got, err := IsSyntheticProfile(ctx, db, profile); err != nil || got != want {
			t.Errorf("%q: %v %v, want %v", profile, got, err, want)
		}
	}
}
