package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/tenant"
)

type kickRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (k *kickRecorder) Kick(id string) { k.mu.Lock(); k.ids = append(k.ids, id); k.mu.Unlock() }
func (k *kickRecorder) got() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.ids...)
}

// send performs a request with a body, replaying and updating the cookie jar.
func (b *browser) send(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	b.h.t.Helper()
	r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
	for k, v := range b.cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	b.h.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return w
}

var sameOriginHdr = map[string]string{"Origin": testOrigin, "Content-Type": "application/json"}

func (b *browser) act(id, action, body string) *httptest.ResponseRecorder {
	return b.send("POST", "/api/admin/users/"+id+"/"+action, body, sameOriginHdr)
}

func (h *harness) tenantStatus(id string) (approval, tenantStatus string) {
	h.t.Helper()
	if err := h.db.QueryRow(`SELECT approval_status, tenant_status FROM users WHERE id = ?`, id).Scan(&approval, &tenantStatus); err != nil {
		h.t.Fatal(err)
	}
	return
}

func (h *harness) adminWithKicks() (*browser, string, *kickRecorder) {
	k := &kickRecorder{}
	h.a.tenants = k
	b := h.userIn("the-admin", "APPROVED", "READY", "ADMIN", true)
	return b, h.userID("the-admin"), k
}

var adminRoutes = []struct{ method, path string }{
	{"GET", "/api/admin/users"},
	{"GET", "/api/admin/users/TARGET"},
	{"POST", "/api/admin/users/TARGET/approve"},
	{"POST", "/api/admin/users/TARGET/reject"},
	{"POST", "/api/admin/users/TARGET/suspend"},
	{"POST", "/api/admin/users/TARGET/reactivate"},
	{"POST", "/api/admin/users/TARGET/revoke-sessions"},
	{"POST", "/api/admin/users/TARGET/tenant-binding"},
	{"POST", "/api/admin/users/TARGET/tenant-verify"},
	{"GET", "/api/admin/audit"},
}

// Every admin route refuses every caller who is not an active, mapped admin,
// whatever the target, and a refused call changes nothing and starts nothing.
func TestAdminRoutesRefuseEveryoneButAnActiveAdminAndChangeNothing(t *testing.T) {
	h := newHarness(t)
	k := &kickRecorder{}
	h.a.tenants = k
	h.userIn("victim", "PENDING", "UNPROVISIONED", "USER", false)
	victim := h.userID("victim")
	people := []struct {
		name   string
		b      *browser
		status int
		code   string
	}{
		{"anonymous", h.browser(), 401, "UNAUTHENTICATED"},
		{"pending", h.userIn("p", "PENDING", "UNPROVISIONED", "USER", false), 403, "ACCOUNT_PENDING"},
		{"rejected", h.userIn("r", "REJECTED", "UNPROVISIONED", "USER", false), 403, "ACCOUNT_REJECTED"},
		{"suspended", h.userIn("s", "SUSPENDED", "READY", "USER", true), 403, "ACCOUNT_SUSPENDED"},
		{"approved, setting up", h.userIn("u", "APPROVED", "CONFIGURING", "USER", false), 409, "TENANT_NOT_READY"},
		{"active ordinary user", h.userIn("a", "APPROVED", "READY", "USER", true), 403, "FORBIDDEN"},
		{"approved admin, tenant failed", h.userIn("af", "APPROVED", "FAILED", "ADMIN", false), 409, "TENANT_NOT_READY"},
	}
	body := `{"reason":"because"}`
	for _, p := range people {
		for _, rt := range adminRoutes {
			path := strings.ReplaceAll(rt.path, "TARGET", victim)
			var w *httptest.ResponseRecorder
			if rt.method == "GET" {
				w = p.b.do("GET", path, nil)
			} else {
				w = p.b.send("POST", path, body, sameOriginHdr)
			}
			if w.Code != p.status || errCode(t, w.Body.Bytes()) != p.code {
				t.Errorf("%s: %s %s = %d %s", p.name, rt.method, rt.path, w.Code, w.Body.String())
			}
		}
	}
	if a, _ := h.tenantStatus(victim); a != "PENDING" {
		t.Fatalf("a refused call changed the victim to %s", a)
	}
	if n := h.count(`SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Fatalf("%d approvals rows from refused calls", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'admin.%'`); n != 0 {
		t.Fatalf("%d admin audit rows from refused calls", n)
	}
	if len(k.got()) != 0 {
		t.Fatalf("a refused call started provisioning: %v", k.got())
	}
}

func TestAdminListsPendingUsersWithStatusOnly(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	h.userIn("p1", "PENDING", "UNPROVISIONED", "USER", false)
	h.userIn("ok", "APPROVED", "READY", "USER", true)
	w := admin.get("/api/admin/users?status=PENDING")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var out struct {
		Users []map[string]any `json:"users"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Users) != 1 || out.Users[0]["email"] != "p1@example.test" || out.Users[0]["approvalStatus"] != "PENDING" {
		t.Fatalf("users = %v", out.Users)
	}
	for k := range out.Users[0] {
		for _, bad := range []string{"profile", "credential", "key", "secret", "token", "cipher", "memory", "chat", "content"} {
			if strings.Contains(strings.ToLower(k), bad) {
				t.Errorf("admin list exposes field %q", k)
			}
		}
	}
	// The READY user's binding is shown by status only.
	w = admin.get("/api/admin/users?status=APPROVED")
	if !has(w.Body.String(), `"binding":{"status":"READY"`) {
		t.Fatalf("binding status missing: %s", w.Body.String())
	}
	for _, bad := range []string{"?status=bogus", "?limit=0", "?limit=500", "?offset=-1", "?limit=x"} {
		if w := admin.get("/api/admin/users" + bad); w.Code != 400 {
			t.Errorf("%s = %d", bad, w.Code)
		}
	}
}

func TestApproveRecordsAuditStartsProvisioningAndLeavesTheUserSettingUp(t *testing.T) {
	h := newHarness(t)
	admin, adminID, k := h.adminWithKicks()
	user := h.userIn("t1", "PENDING", "UNPROVISIONED", "USER", false)
	id := h.userID("t1")
	w := admin.act(id, "approve", ``)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if a, tn := h.tenantStatus(id); a != "APPROVED" || tn != "UNPROVISIONED" {
		t.Fatalf("status = %s/%s", a, tn)
	}
	if !reflect.DeepEqual(k.got(), []string{id}) {
		t.Fatalf("kicks = %v", k.got())
	}
	var actor, action string
	h.db.QueryRow(`SELECT actor_user_id, action FROM audit_log WHERE target_id = ? AND action LIKE 'admin.%'`, id).Scan(&actor, &action)
	if actor != adminID || action != "admin.approve" {
		t.Fatalf("audit = %s %s", actor, action)
	}
	// Approved but not READY: the user sees SETTING_UP and no chat route opens.
	var me AccountView
	json.Unmarshal(user.get("/api/me").Body.Bytes(), &me)
	if me.State != StateSetup || me.CanChat {
		t.Fatalf("me = %+v", me)
	}
	if w := user.get("/api/chats"); w.Code != 409 || errCode(t, w.Body.Bytes()) != "TENANT_NOT_READY" {
		t.Fatalf("chat route: %d %s", w.Code, w.Body.String())
	}
	if w := admin.act(id, "approve", ``); w.Code != 409 || errCode(t, w.Body.Bytes()) != "STATE_CONFLICT" {
		t.Fatalf("second approve: %d %s", w.Code, w.Body.String())
	}
	if len(k.got()) != 1 {
		t.Fatalf("a refused approve kicked provisioning: %v", k.got())
	}
}

func TestRejectAndSuspendNeedAReasonAndRevokeAccessImmediately(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	pending := h.userIn("pend", "PENDING", "UNPROVISIONED", "USER", false)
	pid := h.userID("pend")
	for _, body := range []string{``, `{}`, `{"reason":""}`, `{"reason":"   "}`} {
		if w := admin.act(pid, "reject", body); w.Code != 400 || errCode(t, w.Body.Bytes()) != "REASON_REQUIRED" {
			t.Errorf("reject %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	if a, _ := h.tenantStatus(pid); a != "PENDING" {
		t.Fatalf("status = %s after reason-less rejects", a)
	}
	if w := admin.act(pid, "reject", `{"reason":"not a member"}`); w.Code != 200 {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	// The rejected user's very next request is refused: the session is gone.
	if w := pending.get("/api/me"); w.Code != 401 {
		t.Fatalf("rejected user's session survived: %d", w.Code)
	}
	var reason string
	h.db.QueryRow(`SELECT reason FROM approvals WHERE target_user_id = ?`, pid).Scan(&reason)
	if reason != "not a member" {
		t.Fatalf("reason = %q", reason)
	}

	active := h.userIn("act", "APPROVED", "READY", "USER", true)
	aid := h.userID("act")
	if w := active.get("/api/chats"); w.Code != http.StatusNotImplemented {
		t.Fatalf("active user should pass the guard: %d", w.Code)
	}
	if w := admin.act(aid, "suspend", `{}`); w.Code != 400 {
		t.Fatalf("suspend without reason: %d", w.Code)
	}
	if w := admin.act(aid, "suspend", `{"reason":"abuse"}`); w.Code != 200 {
		t.Fatalf("suspend: %d %s", w.Code, w.Body.String())
	}
	// Loses access on the next request, without waiting for the session to expire.
	if w := active.get("/api/chats"); w.Code != 401 {
		t.Fatalf("suspended user's old session still works: %d", w.Code)
	}
	// Signing in again does not help: the account is blocked, and so is the binding.
	again := h.browser()
	again.login("act", "act@example.test")
	if w := again.get("/api/chats"); w.Code != 403 || errCode(t, w.Body.Bytes()) != "ACCOUNT_SUSPENDED" {
		t.Fatalf("re-login: %d %s", w.Code, w.Body.String())
	}
	if n := h.count(`SELECT COUNT(*) FROM tenant_bindings WHERE user_id = ? AND status = 'READY'`, aid); n != 0 {
		t.Fatal("a suspended user still has a READY binding")
	}
}

// #113: an admin can sign a user out everywhere. Every session ends at once, the account is untouched (signing in
// again works), and the audit row is metadata only. A body or an unknown user changes nothing.
func TestAdminRevokeSessionsSignsTheUserOutEverywhere(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	phone := h.userIn("act", "APPROVED", "READY", "USER", true)
	id := h.userID("act")
	laptop := h.browser()
	laptop.login("act", "act@example.test")
	for _, b := range []*browser{phone, laptop} {
		if w := b.get("/api/chats"); w.Code != http.StatusNotImplemented {
			t.Fatalf("before: %d", w.Code)
		}
	}
	if w := admin.act(id, "revoke-sessions", `{"reason":"x"}`); w.Code != 400 {
		t.Fatalf("with a body: %d %s", w.Code, w.Body.String())
	}
	if w := admin.act("no-such-user", "revoke-sessions", ``); w.Code != 404 {
		t.Fatalf("unknown user: %d %s", w.Code, w.Body.String())
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'admin.revoke_sessions'`); n != 0 {
		t.Fatalf("%d audit rows from refused calls", n)
	}
	if w := phone.get("/api/chats"); w.Code != http.StatusNotImplemented {
		t.Fatalf("a refused call ended the session: %d", w.Code)
	}
	w := admin.act(id, "revoke-sessions", ``)
	if w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	for _, b := range []*browser{phone, laptop} {
		if w := b.get("/api/chats"); w.Code != 401 {
			t.Fatalf("a session survived: %d", w.Code)
		}
	}
	if a, ts := h.tenantStatus(id); a != "APPROVED" || ts != "READY" {
		t.Fatalf("the account changed: %s %s", a, ts)
	}
	again := h.browser()
	again.login("act", "act@example.test")
	if w := again.get("/api/chats"); w.Code != http.StatusNotImplemented {
		t.Fatalf("signing in again: %d", w.Code)
	}
	var actor, kind, target, outcome string
	if err := h.db.QueryRow(`SELECT actor_user_id, target_kind, target_id, outcome FROM audit_log WHERE action = 'admin.revoke_sessions'`).Scan(&actor, &kind, &target, &outcome); err != nil {
		t.Fatal(err)
	}
	if kind != "user" || target != id || actor == id || outcome != "ok" {
		t.Fatalf("audit row: actor %q kind %q target %q outcome %q", actor, kind, target, outcome)
	}
}

func TestReactivationSendsTheUserBackThroughSetup(t *testing.T) {
	h := newHarness(t)
	admin, _, k := h.adminWithKicks()
	h.userIn("act", "APPROVED", "READY", "USER", true)
	id := h.userID("act")
	admin.act(id, "suspend", `{"reason":"abuse"}`)
	if w := admin.act(id, "reactivate", ``); w.Code != 200 {
		t.Fatalf("reactivate: %d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(k.got(), []string{id}) {
		t.Fatalf("kicks = %v", k.got())
	}
	b := h.browser()
	b.login("act", "act@example.test")
	var me AccountView
	json.Unmarshal(b.get("/api/me").Body.Bytes(), &me)
	if me.State != StateSetup || me.CanChat {
		t.Fatalf("me = %+v: a reactivated user must wait for verification", me)
	}
	for _, a := range []string{"admin.suspend", "admin.reactivate"} {
		if h.count(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND target_id = ?`, a, id) != 1 {
			t.Errorf("missing audit row %s", a)
		}
	}
}

func TestMutationsNeedSameOriginAndRefuseServerChosenFields(t *testing.T) {
	h := newHarness(t)
	admin, _, k := h.adminWithKicks()
	h.userIn("t1", "PENDING", "UNPROVISIONED", "USER", false)
	id := h.userID("t1")

	for name, hdr := range map[string]map[string]string{
		"no origin":    {"Content-Type": "application/json"},
		"wrong origin": {"Origin": "https://evil.example.test"},
	} {
		if w := admin.send("POST", "/api/admin/users/"+id+"/approve", ``, hdr); w.Code != 403 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	bodies := map[string]string{
		"profile name":        `{"profile":"u_aaaaaaaaaaaa"}`,
		"slug":                `{"slug":"u_aaaaaaaaaaaa"}`,
		"api key":             `{"key":"fabricated"}`,
		"route prefix":        `{"route_prefix":"/p/x"}`,
		"session id":          `{"session_id":"s"}`,
		"unknown beside real": `{"reason":"ok","role":"ADMIN"}`,
		"trailing data":       `{"reason":"ok"}{"profile":"x"}`,
		"not json":            `reason=ok`,
		"array":               `[]`,
	}
	for name, body := range bodies {
		for _, act := range []string{"approve", "reject", "suspend", "reactivate"} {
			if w := admin.act(id, act, body); w.Code != 400 {
				t.Errorf("%s on %s: %d %s", name, act, w.Code, w.Body.String())
			}
		}
		for _, act := range []string{"tenant-binding", "tenant-verify"} {
			if w := admin.act(id, act, body); w.Code != 400 {
				t.Errorf("%s on %s: %d", name, act, w.Code)
			}
		}
	}
	if w := admin.act(id, "tenant-binding", `{"reason":"x"}`); w.Code != 400 {
		t.Errorf("tenant-binding accepted a body field: %d", w.Code)
	}
	if w := admin.act(id, "approve", strings.Repeat(" ", 5000)+`{"reason":"x"}`); w.Code != 400 {
		t.Errorf("oversized body: %d", w.Code)
	}
	for _, q := range []string{"?profile=u_x", "?api_key=k", "?session_id=s", "?route_prefix=/p/x"} {
		if w := admin.send("POST", "/api/admin/users/"+id+"/approve"+q, ``, sameOriginHdr); w.Code != 400 {
			t.Errorf("query %s: %d", q, w.Code)
		}
	}
	for _, hn := range []string{"Authorization", "X-Api-Key", "X-Hermes-Profile"} {
		hdr := map[string]string{"Origin": testOrigin, hn: "x"}
		if w := admin.send("POST", "/api/admin/users/"+id+"/approve", ``, hdr); w.Code != 400 {
			t.Errorf("header %s: %d", hn, w.Code)
		}
	}
	if a, _ := h.tenantStatus(id); a != "PENDING" || len(k.got()) != 0 {
		t.Fatalf("a refused request changed state (%s) or kicked (%v)", a, k.got())
	}
	if w := admin.get("/api/admin/users?profile=u_x"); w.Code != 400 {
		t.Errorf("list with profile param: %d", w.Code)
	}
}

func TestTargetIdsAreValidatedAndUnknownOnesAre404(t *testing.T) {
	h := newHarness(t)
	admin, adminID, _ := h.adminWithKicks()
	for _, id := range []string{"nope", "..", ".", "%2e%2e", "%2e", "../..", "..%2f", ".%2e", strings.Repeat("A", 32), strings.Repeat("0", 31), strings.Repeat("0", 33), "u_aaaaaaaaaaaa"} {
		for _, rt := range []struct{ m, p string }{{"GET", "/api/admin/users/"}, {"POST", "/api/admin/users/"}} {
			path := rt.p + id
			var w *httptest.ResponseRecorder
			if rt.m == "GET" {
				w = admin.get(path)
			} else {
				w = admin.send("POST", path+"/approve", ``, sameOriginHdr)
			}
			// 404 for every id that cannot exist, dot segments included: never a
			// redirect, whose target a following client would silently adopt.
			if w.Code != 404 {
				t.Errorf("%s %s = %d (want 404)", rt.m, path, w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("%s %s redirected to %s", rt.m, path, loc)
			}
		}
	}
	if w := admin.act(strings.Repeat("0", 32), "approve", ``); w.Code != 404 || errCode(t, w.Body.Bytes()) != "NOT_FOUND" {
		t.Errorf("unknown id: %d %s", w.Code, w.Body.String())
	}
	if w := admin.act(adminID, "suspend", `{"reason":"x"}`); w.Code != 403 {
		t.Errorf("self-suspend: %d", w.Code)
	}
	if a, _ := h.tenantStatus(adminID); a != "APPROVED" {
		t.Fatal("an admin suspended themselves")
	}
}

// Two users A and B: neither can reach the admin tree, nor change the other, by
// any route or id. Their own views never carry the other's data.
func TestOrdinaryUsersCannotActOnEachOther(t *testing.T) {
	h := newHarness(t)
	h.adminWithKicks()
	a := h.userIn("alice", "APPROVED", "READY", "USER", true)
	b := h.userIn("bob", "APPROVED", "READY", "USER", true)
	aID, bID := h.userID("alice"), h.userID("bob")
	for _, act := range []string{"approve", "reject", "suspend", "reactivate", "tenant-binding", "tenant-verify"} {
		if w := a.act(bID, act, `{"reason":"x"}`); w.Code != 403 {
			t.Errorf("alice %s bob = %d", act, w.Code)
		}
		if w := b.act(aID, act, `{"reason":"x"}`); w.Code != 403 {
			t.Errorf("bob %s alice = %d", act, w.Code)
		}
	}
	for _, p := range []string{"/api/admin/users/" + bID, "/api/admin/users", "/api/admin/audit"} {
		if w := a.get(p); w.Code != 403 {
			t.Errorf("alice GET %s = %d", p, w.Code)
		}
	}
	for _, id := range []string{aID, bID} {
		if ap, tn := h.tenantStatus(id); ap != "APPROVED" || tn != "READY" {
			t.Fatalf("a user's status changed: %s/%s", ap, tn)
		}
	}
	var me AccountView
	json.Unmarshal(a.get("/api/me").Body.Bytes(), &me)
	if me.ID != aID || has(a.get("/api/me").Body.String(), "bob") {
		t.Fatal("alice's view carries bob's data")
	}
}

func TestTenantBindingAndVerifyRequeueWithoutAcceptingAnyInput(t *testing.T) {
	h := newHarness(t)
	admin, _, k := h.adminWithKicks()
	h.userIn("f", "APPROVED", "FAILED", "USER", false)
	fid := h.userID("f")
	if w := admin.act(fid, "tenant-verify", ``); w.Code != 409 || errCode(t, w.Body.Bytes()) != "NO_BINDING" {
		t.Fatalf("verify without binding: %d %s", w.Code, w.Body.String())
	}
	if w := admin.act(fid, "tenant-binding", ``); w.Code != http.StatusAccepted {
		t.Fatalf("tenant-binding: %d %s", w.Code, w.Body.String())
	}
	if _, tn := h.tenantStatus(fid); tn != "UNPROVISIONED" {
		t.Fatalf("tenant = %s", tn)
	}
	h.userIn("r", "APPROVED", "READY", "USER", true)
	rid := h.userID("r")
	if w := admin.act(rid, "tenant-verify", `{}`); w.Code != http.StatusAccepted {
		t.Fatalf("tenant-verify: %d %s", w.Code, w.Body.String())
	}
	// During re-verification the user is not READY: fail closed, visibly.
	if _, tn := h.tenantStatus(rid); tn != "CONFIGURING" {
		t.Fatalf("tenant = %s", tn)
	}
	if !reflect.DeepEqual(k.got(), []string{fid, rid}) {
		t.Fatalf("kicks = %v", k.got())
	}
	h.userIn("pend", "PENDING", "UNPROVISIONED", "USER", false)
	if w := admin.act(h.userID("pend"), "tenant-binding", ``); w.Code != 409 {
		t.Fatalf("tenant-binding on a pending user: %d", w.Code)
	}
}

func TestAuditFeedShowsActionsWithoutContent(t *testing.T) {
	h := newHarness(t)
	admin, adminID, _ := h.adminWithKicks()
	h.userIn("t1", "PENDING", "UNPROVISIONED", "USER", false)
	id := h.userID("t1")
	admin.act(id, "reject", `{"reason":"private note"}`)
	w := admin.get("/api/admin/audit")
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	var out struct {
		Entries []AuditView `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	found := false
	for _, e := range out.Entries {
		if e.Action == "admin.reject" && e.Actor == adminID && e.TargetID == id && e.Outcome == "ok" && e.CreatedAt != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reject not in the feed: %s", w.Body.String())
	}
	if has(w.Body.String(), "private note") {
		t.Fatal("the audit feed carries the free-text reason")
	}
}

func TestAdminViewsHaveNoSecretOrContentFields(t *testing.T) {
	for _, v := range []any{AdminUserView{}, BindingView{}, AuditView{}, AccountView{}, StatusView{}} {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			f := strings.ToLower(typ.Field(i).Tag.Get("json") + typ.Field(i).Name)
			for _, bad := range []string{"profile", "credential", "secret", "token", "cipher", "slug", "apikey", "memory", "message", "content"} {
				if strings.Contains(f, bad) {
					t.Errorf("%s.%s looks like a secret or content field", typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}

// The whole path with the real pipeline behind it: approve a user, let the
// provisioner run, and prove neither the key, the sealed key, the profile name
// nor the helper token appears in any response the browser can get.
func TestApprovingAUserProvisionsEndToEndAndNothingSecretReachesAnyResponse(t *testing.T) {
	const key = "fabricated-e2e-key-0123456789abcdef0123"
	h := newHarness(t)
	box, _ := secrets.New(bytes.Repeat([]byte{3}, 32))
	var slug string
	var mu sync.Mutex
	hp := helperFunc(func(s string) (hermes.Credential, error) {
		mu.Lock()
		slug = s
		mu.Unlock()
		return hermes.Credential{Profile: s, Key: key}, nil
	})
	svc, err := tenant.New(tenant.Options{DB: h.db, Box: box, Helper: hp, Now: h.clk.Now,
		Verifier: verifyFunc(func(hermes.Target) hermes.Report { return hermes.Report{{Check: "endpoint", Passed: true}} })})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h.a.tenants = svc

	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	user := h.userIn("newbie", "PENDING", "UNPROVISIONED", "USER", false)
	id := h.userID("newbie")
	var bodies []string
	note := func(w *httptest.ResponseRecorder) { bodies = append(bodies, w.Body.String()) }

	if w := admin.act(id, "approve", ``); w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	} else {
		note(w)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, tn := h.tenantStatus(id); tn == "READY" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, tn := h.tenantStatus(id); tn != "READY" {
		t.Fatalf("tenant = %s: the user never reached the ready state without a manual step", tn)
	}
	var me AccountView
	w := user.get("/api/me")
	note(w)
	json.Unmarshal(w.Body.Bytes(), &me)
	if me.State != StateActive || !me.CanChat {
		t.Fatalf("me = %+v", me)
	}
	for _, p := range []string{"/api/account/status", "/api/admin/users", "/api/admin/users/" + id, "/api/admin/audit", "/api/admin/users?status=APPROVED"} {
		note(admin.get(p))
	}
	note(user.get("/api/chats"))
	note(user.get("/api/admin/users"))
	note(admin.act(id, "tenant-verify", ``))
	note(admin.act(id, "approve", ``))
	note(admin.act(id, "approve", `{"profile":"x"}`))

	mu.Lock()
	profile := slug
	var sealed []byte
	h.db.QueryRow(`SELECT credential_ciphertext FROM tenant_bindings WHERE user_id = ?`, id).Scan(&sealed)
	secretsToHide := []string{key, profile, sentinelCiphertext, hex.EncodeToString(sealed), base64.StdEncoding.EncodeToString(sealed), base64.RawURLEncoding.EncodeToString(sealed)}
	mu.Unlock()
	if len(sealed) == 0 {
		t.Fatal("no sealed credential stored")
	}
	if profile == "" {
		t.Fatal("helper was never called")
	}
	for _, s := range secretsToHide {
		for i, b := range bodies {
			if strings.Contains(b, s) {
				t.Errorf("response %d leaks %q: %s", i, s, b)
			}
		}
	}
}

type helperFunc func(slug string) (hermes.Credential, error)

func (f helperFunc) Provision(_ context.Context, s string) (hermes.Credential, error) { return f(s) }
func (f helperFunc) Rotate(_ context.Context, s string) (hermes.Credential, error)    { return f(s) }

type verifyFunc func(hermes.Target) hermes.Report

func (f verifyFunc) Verify(_ context.Context, t hermes.Target) hermes.Report { return f(t) }

func TestAuditAndDirectoryParametersAreValidated(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	for _, p := range []string{
		"/api/admin/audit?offset=1", "/api/admin/audit?before=nopipe", "/api/admin/audit?before=|id", "/api/admin/audit?limit=0",
		"/api/admin/users?q=%00", "/api/admin/users?q=" + strings.Repeat("a", 101),
	} {
		if w := admin.get(p); w.Code != 400 {
			t.Errorf("%s = %d, want 400", p, w.Code)
		}
	}
	for _, p := range []string{"/api/admin/audit?before=2026-01-01T00:00:00Z|abc", "/api/admin/users?q=%25_&status=PENDING"} {
		if w := admin.get(p); w.Code != 200 {
			t.Errorf("%s = %d, want 200: %s", p, w.Code, w.Body.String())
		}
	}
}

func TestAuditPagesByCursor(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	for i := 0; i < 3; i++ {
		h.userIn("pg"+strconv.Itoa(i), "PENDING", "UNPROVISIONED", "USER", false)
		admin.act(h.userID("pg"+strconv.Itoa(i)), "reject", `{"reason":"r"}`)
	}
	var seen = map[string]bool{}
	before := ""
	for pages := 0; pages < 20; pages++ {
		w := admin.get("/api/admin/audit?limit=2&before=" + url.QueryEscape(before))
		var out struct {
			Entries []AuditView `json:"entries"`
			Next    string      `json:"next"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		for _, e := range out.Entries {
			if seen[e.ID] {
				t.Fatalf("entry %s served twice", e.ID)
			}
			seen[e.ID] = true
		}
		if out.Next == "" {
			break
		}
		before = out.Next
	}
	if len(seen) < 3 {
		t.Fatalf("paged through only %d entries", len(seen))
	}
}

// #193 review: the revocation and its audit row commit together. When the audit insert fails, nothing is revoked.
func TestAdminRevokeSessionsRollsBackWhenTheAuditFails(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	target := h.userIn("act", "APPROVED", "READY", "USER", true)
	id := h.userID("act")
	if _, err := h.db.Exec(`CREATE TRIGGER fail_revoke_audit BEFORE INSERT ON audit_log
		WHEN NEW.action = 'admin.revoke_sessions' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if w := admin.act(id, "revoke-sessions", ``); w.Code < 500 {
		t.Fatalf("revoke with the audit failing: %d %s", w.Code, w.Body.String())
	}
	if w := target.get("/api/chats"); w.Code != http.StatusNotImplemented {
		t.Fatalf("the session was revoked although the audit failed: %d", w.Code)
	}
	if _, err := h.db.Exec(`DROP TRIGGER fail_revoke_audit`); err != nil {
		t.Fatal(err)
	}
	if w := admin.act(id, "revoke-sessions", ``); w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := target.get("/api/chats"); w.Code != 401 {
		t.Fatalf("after a good revoke: %d", w.Code)
	}
}
