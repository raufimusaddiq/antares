package smoke

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func wantStatus(t *testing.T, rep Report, id string, want Status) StepResult {
	t.Helper()
	got := rep.step(id)
	if got.Status != want {
		t.Fatalf("step %s: got %s (%q), want %s\nfull report:\n%s", id, got.Status, got.Detail, want, dump(rep))
	}
	return got
}

func dump(rep Report) string {
	var b strings.Builder
	for _, s := range rep.Steps {
		b.WriteString("  " + string(s.Status) + "  " + s.ID + "  " + s.Detail + "\n")
	}
	b.WriteString(rep.Summary())
	return b.String()
}

func TestHealthyStackPassesEveryStep(t *testing.T) {
	s := newStack(t, false, nil)
	cfg := s.config()
	rep := s.run(cfg)
	t.Log("\n" + dump(rep))
	if rep.Verdict != VerdictPass || rep.ExitCode() != 0 {
		t.Fatalf("verdict %s, want PASS:\n%s", rep.Verdict, dump(rep))
	}
	for _, id := range []string{"reach.health", "login.admin", "login.a", "login.b", "login.pending", "admin.active", "negative.unapproved",
		"account.approve", "tenant.ready.a", "tenant.verify.a", "chat.a.reply", "tenant.ready.b", "tenant.verify.b", "chat.b.reply",
		"negative.cross_user", "negative.revoked", "cleanup.restore"} {
		wantStatus(t, rep, id, Pass)
	}
	// The reply the user got is the one Hermes produced, in the transcript.
	if cfg.A.reply == "" || !strings.Contains(cfg.A.reply, "fake Hermes") {
		t.Fatalf("A's reply = %q", cfg.A.reply)
	}
	// Nothing in the machine-readable report is a credential.
	raw, _ := rep.JSON()
	for sub, c := range s.cookies {
		if strings.Contains(string(raw), c) {
			t.Fatalf("report contains %s's session cookie", sub)
		}
	}
	for _, k := range s.hermes.keys {
		if strings.Contains(string(raw), k) {
			t.Fatal("report contains a tenant key")
		}
	}
}

// The first live failure: the host helper was stale, so agent.disabled_toolsets was
// never written and boundary-crossing toolsets stayed on. /healthz, /readyz and the
// service being active all passed. The smoke test must fail at the tenant step and
// say why, in Antares' own words.
func TestStaleProvisionerFailsAtTheTenantStepNamingToolsetPolicy(t *testing.T) {
	s := newStack(t, false, nil)
	s.waitAdminReady()      // the admin was provisioned before the helper went stale...
	s.hermes.setStale(true) // ...and every profile provisioned after that has terminal enabled
	rep := s.run(s.config())
	t.Log("\n" + dump(rep))
	if rep.Verdict != VerdictFail || rep.ExitCode() != 1 {
		t.Fatalf("verdict %s, want FAIL", rep.Verdict)
	}
	if rep.FirstFailure != "tenant.ready.a" {
		t.Fatalf("first failure = %s, want tenant.ready.a", rep.FirstFailure)
	}
	d := wantStatus(t, rep, "tenant.ready.a", Fail).Detail
	if !strings.Contains(d, "toolset_policy") || !strings.Contains(d, "terminal") {
		t.Fatalf("detail does not name the toolset policy failure: %q", d)
	}
	// Reaching the service was fine, and so was the unapproved check; the failure is not "everything".
	wantStatus(t, rep, "reach.health", Pass)
	wantStatus(t, rep, "negative.unapproved", Pass)
	// What depends on a working tenant did not run, and is reported as skipped, not passed.
	for _, id := range []string{"tenant.verify.a", "chat.a.reply", "negative.cross_user", "negative.revoked"} {
		wantStatus(t, rep, id, Skip)
	}
}

// The same fault from the first provisioning: not even the admin's workspace comes up, and
// the gate fails at the first step that needs one, instead of reporting ready.
func TestStaleProvisionerFromTheStartFailsAtAdminActive(t *testing.T) {
	s := newStack(t, true, nil)
	rep := s.run(s.config())
	wantStatus(t, rep, "admin.active", Fail)
	if rep.Verdict != VerdictFail {
		t.Fatalf("verdict %s", rep.Verdict)
	}
	wantStatus(t, rep, "chat.a.reply", Skip)
}

func TestRunThatFailsIsAFailure(t *testing.T) {
	s := newStack(t, false, nil)
	s.hermes.setMode("fail")
	rep := s.run(s.config())
	d := wantStatus(t, rep, "chat.a.reply", Fail).Detail
	if !strings.Contains(d, "FAILED") {
		t.Fatalf("detail = %q", d)
	}
	wantStatus(t, rep, "tenant.verify.a", Pass) // the tenant was fine; the chat was not
	wantStatus(t, rep, "negative.cross_user", Skip)
	if rep.ExitCode() != 1 {
		t.Fatalf("exit %d", rep.ExitCode())
	}
}

func TestCompletedRunWithoutAReplyIsAFailure(t *testing.T) {
	s := newStack(t, false, nil)
	s.hermes.setMode("empty")
	cfg := s.config()
	rep := s.run(cfg)
	d := wantStatus(t, rep, "chat.a.reply", Fail).Detail
	if !strings.Contains(d, "no non-empty assistant reply") {
		t.Fatalf("detail = %q", d)
	}
}

func TestRunThatNeverFinishesFailsWithinTheBound(t *testing.T) {
	s := newStack(t, false, nil)
	s.hermes.setMode("hang")
	cfg := s.config()
	cfg.RunTimeout = 1500e6 // 1.5s
	rep := s.run(cfg)
	d := wantStatus(t, rep, "chat.a.reply", Fail).Detail
	if !strings.Contains(d, "no terminal phase within") {
		t.Fatalf("detail = %q", d)
	}
}

// Skipped must never read as passed.
func TestMissingCredentialsAreSkippedAndTheRunIsIncomplete(t *testing.T) {
	s := newStack(t, false, nil)
	cfg := s.config()
	cfg.Pending = nil
	rep := s.run(cfg)
	wantStatus(t, rep, "login.pending", Skip)
	wantStatus(t, rep, "negative.unapproved", Skip)
	if rep.Verdict != VerdictIncomplete || rep.ExitCode() != 2 {
		t.Fatalf("verdict %s exit %d, want INCOMPLETE/2", rep.Verdict, rep.ExitCode())
	}
	if !strings.Contains(rep.Summary(), "not a pass") {
		t.Fatalf("summary hides the skip: %s", rep.Summary())
	}
}

func TestExpiredSessionFailsTheLoginStepAndBlocksTheRest(t *testing.T) {
	s := newStack(t, false, nil)
	cfg := s.config()
	cfg.A = &Actor{Name: "A", Session: &Session{Name: "A", Cookie: "expired-or-wrong-cookie-value-0000"}}
	s.red.Add(cfg.A.Session.Cookie)
	rep := s.run(cfg)
	d := wantStatus(t, rep, "login.a", Fail).Detail
	if !strings.Contains(d, "401") || strings.Contains(d, "expired-or-wrong") {
		t.Fatalf("detail = %q", d)
	}
	wantStatus(t, rep, "account.approve", Skip)
	wantStatus(t, rep, "chat.a.reply", Skip)
}

// ---- the negative checks must fail when the defect they guard against is present ------

// swapCookie rewrites the session cookie of requests matching match from one
// account to another, as a broken ownership or revocation check would behave:
// the server then sees the other account.
func swapCookie(s *stack, h http.Handler, from, to string, match func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil && s.cookie(from) != "" && c.Value == s.cookie(from) && match(r) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: s.cookie(to)})
		}
		h.ServeHTTP(w, r)
	})
}

func TestCrossUserReadIsCaught(t *testing.T) {
	s := newStack(t, false, func(s *stack, h http.Handler) http.Handler {
		// B asking for something A owns is served as A: the ownership check does not exist.
		return swapCookie(s, h, "sub-b", "sub-a", func(r *http.Request) bool { return s.ownerSub(r.URL.Path) == "sub-a" })
	})
	rep := s.run(s.config())
	d := wantStatus(t, rep, "negative.cross_user", Fail).Detail
	if !strings.Contains(d, "ISOLATION BREACH") {
		t.Fatalf("detail = %q", d)
	}
	if rep.ExitCode() != 1 {
		t.Fatal("a leak must fail the gate")
	}
}

func TestRevokedAccountThatStillWorksIsCaught(t *testing.T) {
	var suspended atomic.Bool
	s := newStack(t, false, func(s *stack, h http.Handler) http.Handler {
		inner := swapCookie(s, h, "sub-b", "sub-a", func(r *http.Request) bool { return suspended.Load() && strings.HasPrefix(r.URL.Path, "/api/chats") })
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/suspend") {
				suspended.Store(true) // a stale positive cache: B keeps its old standing
			}
			inner.ServeHTTP(w, r)
		})
	})
	rep := s.run(s.config())
	d := wantStatus(t, rep, "negative.revoked", Fail).Detail
	if !strings.Contains(d, "ALLOWED") {
		t.Fatalf("detail = %q", d)
	}
}

func TestUnapprovedAccountThatCanChatIsCaught(t *testing.T) {
	s := newStack(t, false, func(s *stack, h http.Handler) http.Handler {
		return swapCookie(s, h, "sub-pending", "sub-admin", func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/chats") })
	})
	rep := s.run(s.config())
	d := wantStatus(t, rep, "negative.unapproved", Fail).Detail
	if !strings.Contains(d, "ALLOWED") {
		t.Fatalf("detail = %q", d)
	}
}
