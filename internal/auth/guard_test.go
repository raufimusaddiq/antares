package auth

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func concrete(path string) string {
	return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "00000000000000000000000000000001")
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct{ Code, Message, RequestID string } `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil || e.Error.Code == "" {
		t.Fatalf("not an error envelope: %q", body)
	}
	return e.Error.Code
}

// Every chat-touching route refuses every account that is not fully eligible.
// This is enforced by the server, not hidden by the UI.
func TestEveryChatRouteRefusesIneligibleAccounts(t *testing.T) {
	h := newHarness(t)
	type who struct {
		name   string
		b      *browser
		status int
		code   string
	}
	people := []who{
		{"anonymous", h.browser(), 401, "UNAUTHENTICATED"},
		{"pending", h.userIn("s-pending", "PENDING", "UNPROVISIONED", "USER", false), 403, "ACCOUNT_PENDING"},
		{"rejected", h.userIn("s-rejected", "REJECTED", "UNPROVISIONED", "USER", false), 403, "ACCOUNT_REJECTED"},
		{"suspended", h.userIn("s-suspended", "SUSPENDED", "READY", "USER", true), 403, "ACCOUNT_SUSPENDED"},
		{"approved, unprovisioned", h.userIn("s-unprov", "APPROVED", "UNPROVISIONED", "USER", false), 409, "TENANT_NOT_READY"},
		{"approved, configuring", h.userIn("s-config", "APPROVED", "CONFIGURING", "USER", false), 409, "TENANT_NOT_READY"},
		{"approved, tenant failed", h.userIn("s-failed", "APPROVED", "FAILED", "USER", false), 409, "TENANT_NOT_READY"},
		{"approved, tenant disabled", h.userIn("s-disabled", "APPROVED", "DISABLED", "USER", true), 409, "TENANT_NOT_READY"},
		{"tenant READY but no binding row", h.userIn("s-nobind", "APPROVED", "READY", "USER", false), 409, "TENANT_NOT_READY"},
		{"approved admin, tenant not ready", h.userIn("s-admin", "APPROVED", "UNPROVISIONED", "ADMIN", false), 409, "TENANT_NOT_READY"},
	}
	for _, rt := range ChatRoutes {
		for _, p := range people {
			w := p.b.do(rt.Method, concrete(rt.Path), nil)
			if w.Code != p.status || errCode(t, w.Body.Bytes()) != p.code {
				t.Errorf("%s %s as %s: %d %s", rt.Method, rt.Path, p.name, w.Code, w.Body.String())
			}
		}
	}
	if len(ChatRoutes) < 25 {
		t.Fatalf("route table suspiciously small: %d", len(ChatRoutes))
	}
	// Nothing was reachable: the refusals above never fell through to a handler.
}

func TestEligibleUserPassesTheGuardAndAdminTreeIsAdminOnly(t *testing.T) {
	h := newHarness(t)
	active := h.userIn("s-active", "APPROVED", "READY", "USER", true)
	for _, rt := range ChatRoutes {
		if w := active.do(rt.Method, concrete(rt.Path), nil); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s as active user = %d (guard should pass to the placeholder)", rt.Method, rt.Path, w.Code)
		}
	}
	if w := active.get("/api/admin/users"); w.Code != 403 || errCode(t, w.Body.Bytes()) != "FORBIDDEN" {
		t.Errorf("active non-admin on admin tree: %d %s", w.Code, w.Body.String())
	}
	pending := h.userIn("s-p", "PENDING", "UNPROVISIONED", "USER", false)
	if w := pending.get("/api/admin/users"); w.Code != 403 || errCode(t, w.Body.Bytes()) != "ACCOUNT_PENDING" {
		t.Errorf("pending on admin tree: %d", w.Code)
	}
	if w := h.browser().get("/api/admin/users"); w.Code != 401 {
		t.Errorf("anonymous on admin tree: %d", w.Code)
	}
	admin := h.userIn("s-a", "APPROVED", "UNPROVISIONED", "ADMIN", false)
	if w := admin.get("/api/admin/users"); w.Code != http.StatusNotImplemented {
		t.Errorf("admin on admin tree: %d", w.Code)
	}
	// Role decides what is shown; the login path is the same one.
	var v AccountView
	json.Unmarshal(admin.get("/api/me").Body.Bytes(), &v)
	if !v.CanAdmin || v.CanChat || v.Role != "ADMIN" {
		t.Errorf("admin view = %+v", v)
	}
}

// The PRD's own API table is the checklist: a route added there without a
// guard here fails this test.
func TestPRDRoutesAreAllGuarded(t *testing.T) {
	raw, err := os.ReadFile("../../docs/PRD.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	section := func(from, to string) string {
		i, j := strings.Index(doc, from), strings.Index(doc, to)
		if i < 0 || j < i {
			t.Fatalf("PRD markers %q..%q not found", from, to)
		}
		return doc[i:j]
	}
	guarded := map[string]bool{}
	for _, rt := range ChatRoutes {
		guarded[rt.Method+" "+rt.Path] = true
	}
	table := regexp.MustCompile("(?m)^\\| `([A-Z]+)` \\| `(/api/[^`?]+)[^`]*` \\| ([^|]+) \\|")
	checked := 0
	for _, m := range table.FindAllStringSubmatch(section("## 9. Chat UI REST/SSE API", "### 9.1 Error"), -1) {
		method, path, who := m[1], m[2], strings.TrimSpace(m[3])
		if strings.HasPrefix(who, "Active") && !guarded[method+" "+path] {
			t.Errorf("PRD §9 lists %s %s for %q users but no guard covers it", method, path, who)
		}
		checked++
	}
	inline := regexp.MustCompile("(?m)^\\| `([A-Z]+) (/api/[^`?]+)[^`]*` \\|")
	for _, m := range inline.FindAllStringSubmatch(section("### 9A.7", "### 9A.8"), -1) {
		checked++
		if !guarded[m[1]+" "+m[2]] {
			t.Errorf("PRD §9A.7 lists %s %s but no guard covers it", m[1], m[2])
		}
	}
	if checked < 30 {
		t.Fatalf("parsed only %d PRD routes; the table format changed", checked)
	}
}

func TestStatusChangeTakesEffectOnNextRequest(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-1", "APPROVED", "READY", "USER", true)
	if w := b.get("/api/chats"); w.Code != http.StatusNotImplemented {
		t.Fatalf("setup: %d", w.Code)
	}
	h.exec(`UPDATE users SET approval_status='SUSPENDED' WHERE google_sub='s-1'`)
	if w := b.get("/api/chats"); w.Code != 403 {
		t.Fatalf("suspended user's live session still reaches chat: %d", w.Code)
	}
}

func TestCallerSuppliedProfileKeyOrSessionIsRejected(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-1", "APPROVED", "READY", "USER", true)
	for _, c := range []struct{ path, hdr string }{
		{"/api/chats?profile=other", ""}, {"/api/chats?Profile_Name=x", ""}, {"/api/chats?api_key=k", ""},
		{"/api/chats?session_id=abc", ""}, {"/api/chats?route_prefix=/p", ""},
		{"/api/chats", "X-Hermes-Profile"}, {"/api/chats", "Authorization"}, {"/api/chats", "X-Hermes-Session-Id"}, {"/api/chats", "X-Api-Key"},
	} {
		hdr := map[string]string{}
		if c.hdr != "" {
			hdr[c.hdr] = "x"
		}
		w := b.do("GET", c.path, hdr)
		if w.Code != http.StatusBadRequest || errCode(t, w.Body.Bytes()) != "INVALID_REQUEST" {
			t.Errorf("%s %s: %d %s", c.path, c.hdr, w.Code, w.Body.String())
		}
	}
}

func TestLogoutInvalidatesSessionServerSide(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-1", "APPROVED", "READY", "USER", true)
	old := b.cookies[sessionCookie]
	if w := b.get("/api/me"); w.Code != 200 {
		t.Fatalf("setup: %d", w.Code)
	}
	w := b.do("POST", "/api/auth/logout", map[string]string{"Origin": testOrigin})
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	if _, kept := b.cookies[sessionCookie]; kept {
		t.Error("cookie not cleared")
	}
	// A client that kept the old value (an attacker, or a script that copied it) is refused.
	thief := h.browser()
	thief.cookies[sessionCookie] = old
	for _, p := range []string{"/api/me", "/api/account/status", "/api/chats"} {
		if w := thief.get(p); w.Code != http.StatusUnauthorized {
			t.Errorf("old cookie on %s after logout = %d", p, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM app_sessions WHERE revoked_at IS NOT NULL`); n != 1 {
		t.Fatalf("revoked sessions = %d", n)
	}
}

func TestLogoutRefusesCrossOriginAndAnonymous(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-1", "APPROVED", "READY", "USER", true)
	for _, hdr := range []map[string]string{nil, {"Origin": "https://evil.example.test"}, {"Origin": "null"}, {"Sec-Fetch-Site": "cross-site"}} {
		if w := b.do("POST", "/api/auth/logout", hdr); w.Code != http.StatusForbidden {
			t.Errorf("headers %v: %d", hdr, w.Code)
		}
	}
	if w := b.get("/api/me"); w.Code != 200 {
		t.Fatal("a refused logout must leave the session alone")
	}
	if w := h.browser().do("POST", "/api/auth/logout", map[string]string{"Origin": testOrigin}); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous logout = %d", w.Code)
	}
	if w := b.do("GET", "/api/auth/logout", nil); w.Code == http.StatusNoContent {
		t.Error("logout must not be a GET")
	}
}

func TestSessionExpires(t *testing.T) {
	h := newHarness(t)
	b := h.userIn("s-1", "APPROVED", "READY", "USER", true)
	h.clk.Add(sessionTTL - time.Minute)
	if w := b.get("/api/me"); w.Code != 200 {
		t.Fatalf("session died early: %d", w.Code)
	}
	h.clk.Add(2 * time.Minute)
	if w := b.get("/api/me"); w.Code != 401 {
		t.Fatalf("session outlived its expiry: %d", w.Code)
	}
}

func TestMeAndStatusRequireASession(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/api/me", "/api/account/status"} {
		if w := h.browser().get(p); w.Code != 401 || errCode(t, w.Body.Bytes()) != "UNAUTHENTICATED" {
			t.Errorf("%s anonymous: %d", p, w.Code)
		}
	}
	pending := h.userIn("s-p", "PENDING", "UNPROVISIONED", "USER", false)
	var st StatusView
	w := pending.get("/api/account/status")
	json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.State != StatePending || st.CanChat || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("pending status: %d %+v", w.Code, st)
	}
}

func TestAuditRecordsLoginLogoutAndRejection(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.login("s-1", "a@example.test")
	id := h.userID("s-1")
	b.do("POST", "/api/auth/logout", map[string]string{"Origin": testOrigin})
	// a rejected attempt
	h.g.claims = func(n string) map[string]any {
		c := h.g.goodClaims("s-2", "b@example.test", n)
		c["aud"] = "wrong"
		return c
	}
	state, code := b.begin()
	b.callback(state, code)

	rows, err := h.db.Query(`SELECT COALESCE(actor_user_id,''), action, outcome, COALESCE(request_id,''), created_at FROM audit_log ORDER BY created_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var actor, action, outcome, req, at string
		rows.Scan(&actor, &action, &outcome, &req, &at)
		if at == "" || req == "" {
			t.Errorf("audit row missing time/request id: %s %s", action, outcome)
		}
		who := "-"
		if actor == id {
			who = "user"
		}
		got = append(got, who+" "+action+" "+outcome)
	}
	want := []string{"user auth.login ok", "user auth.logout ok", "- auth.login rejected:id_token"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("audit = %q, want %q", got, want)
	}
}
