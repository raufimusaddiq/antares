package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStartSendsPKCEStateAndNonceWithMinimalScopes(t *testing.T) {
	h := newHarness(t)
	w := h.browser().get("/api/auth/google/start")
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if q.Get("scope") != "openid email profile" || q.Get("response_type") != "code" || q.Get("client_id") != testClient ||
		q.Get("redirect_uri") != testOrigin+callbackPath || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization request = %v", q)
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if len(q.Get(k)) < 40 {
			t.Errorf("%s too short to be unguessable: %q", k, q.Get(k))
		}
	}
	if q.Get("client_secret") != "" || strings.Contains(w.Header().Get("Location"), testSecret) {
		t.Fatal("client secret must never be in the browser redirect")
	}
}

func TestLoginCreatesPendingUserWithoutTenantAndPKCEVerifierMatches(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.login("sub-new", "new@example.test")

	var role, approval, tenant string
	if err := h.db.QueryRow(`SELECT role, approval_status, tenant_status FROM users WHERE google_sub='sub-new'`).Scan(&role, &approval, &tenant); err != nil {
		t.Fatal(err)
	}
	if role != "USER" || approval != "PENDING" || tenant != "UNPROVISIONED" {
		t.Fatalf("first login = %s/%s/%s", role, approval, tenant)
	}
	if n := h.count(`SELECT COUNT(*) FROM tenant_bindings`); n != 0 {
		t.Fatalf("a pending user must have no tenant binding, found %d", n)
	}
	if h.g.verify == "" {
		t.Fatal("token endpoint never saw a PKCE code_verifier")
	}
	var issuer string
	h.db.QueryRow(`SELECT google_issuer FROM users WHERE google_sub='sub-new'`).Scan(&issuer)
	if issuer != "https://accounts.google.com" {
		t.Fatalf("issuer stored = %q", issuer)
	}
}

func TestSessionCookieAttributesAndHashedStorage(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	w := b.login("sub-1", "a@example.test")
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		if x.Name == sessionCookie {
			c = x
		}
	}
	if c == nil {
		t.Fatal("no session cookie set")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge <= 0 {
		t.Fatalf("cookie attributes: %+v", c)
	}
	var stored string
	h.db.QueryRow(`SELECT token_hash FROM app_sessions`).Scan(&stored)
	sum := sha256.Sum256([]byte(c.Value))
	if stored == c.Value || stored != hexOf(sum[:]) {
		t.Fatalf("session table must hold the SHA-256 of the cookie, not the cookie: %q", stored)
	}
	if n := h.count(`SELECT COUNT(*) FROM app_sessions WHERE token_hash = ?`, c.Value); n != 0 {
		t.Fatal("raw cookie value is queryable as a session key")
	}
}

func hexOf(b []byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, d[x>>4], d[x&15])
	}
	return string(out)
}

func TestLoginRotatesSession(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.login("sub-1", "a@example.test")
	first := b.cookies[sessionCookie]
	b.login("sub-1", "a@example.test")
	if b.cookies[sessionCookie] == first {
		t.Fatal("session token was reused across logins")
	}
	old := h.browser()
	old.cookies[sessionCookie] = first
	if w := old.get("/api/me"); w.Code != http.StatusUnauthorized {
		t.Fatalf("pre-login session still works: %d", w.Code)
	}
	if w := b.get("/api/me"); w.Code != http.StatusOK {
		t.Fatalf("new session rejected: %d", w.Code)
	}
}

func TestExpiredOrReplayedStateIsRejected(t *testing.T) {
	cases := map[string]func(h *harness, b *browser, state, code string) *httpResult{
		"replayed": func(h *harness, b *browser, state, code string) *httpResult {
			if w := b.callback(state, code); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
				t.Fatalf("first use must succeed: %d %s", w.Code, w.Header().Get("Location"))
			}
			// A second browser that captured the callback URL cannot reuse it either.
			other := h.browser()
			other.cookies[flowCookie] = b.cookies[flowCookie]
			return wrap(other.callback(state, code))
		},
		"replayed in the same browser": func(h *harness, b *browser, state, code string) *httpResult {
			b.callback(state, code)
			return wrap(b.callback(state, code))
		},
		"expired": func(h *harness, b *browser, state, code string) *httpResult {
			h.clk.Add(flowTTL + time.Second)
			return wrap(b.callback(state, code))
		},
		"unknown state": func(h *harness, b *browser, state, code string) *httpResult {
			return wrap(b.callback("not-a-state", code))
		},
		"no flow cookie (login CSRF)": func(h *harness, b *browser, state, code string) *httpResult {
			delete(b.cookies, flowCookie)
			return wrap(b.callback(state, code))
		},
		"someone else's flow cookie": func(h *harness, b *browser, state, code string) *httpResult {
			b.cookies[flowCookie] = randomToken()
			return wrap(b.callback(state, code))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			b := h.browser()
			h.g.claims = func(n string) map[string]any { return h.g.goodClaims("sub-r", "r@example.test", n) }
			state, code := b.begin()
			res := run(h, b, state, code)
			if res.code != http.StatusSeeOther || res.loc != "/error?code=login_failed" {
				t.Fatalf("got %d -> %s", res.code, res.loc)
			}
			if res.hasSession {
				t.Fatal("rejected callback set a session cookie")
			}
			if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action='auth.login' AND outcome LIKE 'rejected:%' AND actor_user_id IS NULL`); n < 1 {
				t.Fatal("rejected attempt was not audited")
			}
		})
	}
}

type httpResult struct {
	code       int
	loc        string
	hasSession bool
}

func wrap(w interface {
	Result() *http.Response
}) *httpResult {
	r := w.Result()
	res := &httpResult{code: r.StatusCode, loc: r.Header.Get("Location")}
	for _, c := range r.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			res.hasSession = true
		}
	}
	return res
}

func TestReplayedStateAfterSuccessCreatesNoSecondSession(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	h.g.claims = func(n string) map[string]any { return h.g.goodClaims("sub-x", "x@example.test", n) }
	state, code := b.begin()
	b.callback(state, code)
	b.callback(state, code)
	if n := h.count(`SELECT COUNT(*) FROM app_sessions`); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
}

func TestIDTokenValidation(t *testing.T) {
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := func(h *harness) time.Time { return h.clk.Now() }
	cases := []struct {
		name string
		mut  func(h *harness, c map[string]any)
		sign func(h *harness, c map[string]any) string
	}{
		{name: "wrong signature key", sign: func(h *harness, c map[string]any) string { return h.g.sign(c, "RS256", h.g.kid, other) }},
		{name: "alg none", sign: func(h *harness, c map[string]any) string { return h.g.sign(c, "none", h.g.kid, nil) }},
		{name: "alg HS256", sign: func(h *harness, c map[string]any) string { return h.g.sign(c, "HS256", h.g.kid, nil) }},
		{name: "unknown kid", sign: func(h *harness, c map[string]any) string { return h.g.sign(c, "RS256", "kid-other", h.g.key) }},
		{name: "wrong issuer", mut: func(h *harness, c map[string]any) { c["iss"] = "https://evil.example.test" }},
		{name: "wrong audience", mut: func(h *harness, c map[string]any) { c["aud"] = "someone-else" }},
		{name: "audience list without us", mut: func(h *harness, c map[string]any) { c["aud"] = []string{"a", "b"} }},
		{name: "multi audience without azp", mut: func(h *harness, c map[string]any) { c["aud"] = []string{testClient, "b"} }},
		{name: "expired", mut: func(h *harness, c map[string]any) { c["exp"] = now(h).Add(-2 * time.Minute).Unix() }},
		{name: "no exp", mut: func(h *harness, c map[string]any) { delete(c, "exp") }},
		{name: "issued in the future", mut: func(h *harness, c map[string]any) { c["iat"] = now(h).Add(time.Hour).Unix() }},
		{name: "wrong nonce", mut: func(h *harness, c map[string]any) { c["nonce"] = "another-nonce" }},
		{name: "no nonce", mut: func(h *harness, c map[string]any) { delete(c, "nonce") }},
		{name: "email unverified", mut: func(h *harness, c map[string]any) { c["email_verified"] = false }},
		{name: "email_verified missing", mut: func(h *harness, c map[string]any) { delete(c, "email_verified") }},
		{name: "no sub", mut: func(h *harness, c map[string]any) { delete(c, "sub") }},
		{name: "no email", mut: func(h *harness, c map[string]any) { delete(c, "email") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			b := h.browser()
			h.g.claims = func(n string) map[string]any {
				c := h.g.goodClaims("sub-t", "t@example.test", n)
				if tc.mut != nil {
					tc.mut(h, c)
				}
				return c
			}
			if tc.sign != nil {
				h.g.tokenFor = func(c map[string]any) string { return tc.sign(h, c) }
			}
			state, code := b.begin()
			res := wrap(b.callback(state, code))
			if res.loc != "/error?code=login_failed" || res.hasSession {
				t.Fatalf("invalid token accepted: %d %s session=%v", res.code, res.loc, res.hasSession)
			}
			if n := h.count(`SELECT COUNT(*) FROM users`); n != 0 {
				t.Fatalf("a token that failed validation created %d users: a failed login must be wholly failed", n)
			}
			if n := h.count(`SELECT COUNT(*) FROM app_sessions`); n != 0 {
				t.Fatal("session created from an invalid token")
			}
		})
	}
}

func TestMultiAudienceWithAzpIsAccepted(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	h.g.claims = func(n string) map[string]any {
		c := h.g.goodClaims("sub-a", "a@example.test", n)
		c["aud"], c["azp"] = []string{testClient, "other"}, testClient
		c["email_verified"] = "true" // Google's historical string form
		return c
	}
	state, code := b.begin()
	if w := b.callback(state, code); w.Header().Get("Location") != "/" {
		t.Fatalf("valid multi-audience token refused: %s", w.Header().Get("Location"))
	}
}

func TestTokenEndpointRejectionIsAFailedLogin(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	h.g.claims = func(n string) map[string]any { return h.g.goodClaims("s", "s@example.test", n) }
	state, _ := b.begin()
	res := wrap(b.callback(state, "code-the-provider-never-issued"))
	if res.loc != "/error?code=login_failed" || h.count(`SELECT COUNT(*) FROM users`) != 0 {
		t.Fatalf("got %s", res.loc)
	}
}

func TestProviderDeniedConsentIsAFailedLogin(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	state, _ := b.begin()
	w := b.get(callbackPath + "?error=access_denied&state=" + url.QueryEscape(state))
	if w.Header().Get("Location") != "/error?code=login_failed" {
		t.Fatalf("got %s", w.Header().Get("Location"))
	}
}

func TestSameSubNewEmailIsSameAccountAndKeepsStatus(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	b.login("sub-1", "old@example.test")
	id := h.userID("sub-1")
	h.exec(`UPDATE users SET approval_status='APPROVED' WHERE id=?`, id)
	b.login("sub-1", "renamed@example.test")
	if n := h.count(`SELECT COUNT(*) FROM users`); n != 1 {
		t.Fatalf("users = %d", n)
	}
	var email, approval string
	h.db.QueryRow(`SELECT email, approval_status FROM users WHERE id=?`, id).Scan(&email, &approval)
	if email != "renamed@example.test" || approval != "APPROVED" {
		t.Fatalf("email=%s approval=%s: login must refresh metadata and never touch status", email, approval)
	}
	// And the reverse: a new sub with an old user's email is a different, pending account.
	b2 := h.browser()
	b2.login("sub-2", "renamed@example.test")
	var st string
	h.db.QueryRow(`SELECT approval_status FROM users WHERE google_sub='sub-2'`).Scan(&st)
	if st != "PENDING" {
		t.Fatalf("email must not be an authorization key; got %s", st)
	}
}

func TestStartRefusesWhenTooManyFlowsInProgress(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < maxFlows; i++ {
		h.a.flows.put(randomToken(), flow{expires: h.clk.Now().Add(time.Hour)}, h.clk.Now())
	}
	if w := h.browser().get("/api/auth/google/start"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", w.Code)
	}
}

func TestNewRequiresEveryInput(t *testing.T) {
	h := newHarness(t)
	for name, o := range map[string]Options{
		"no client id": {DB: h.db, Endpoints: h.g.endpoints(), ClientSecret: "s", PublicOrigin: testOrigin},
		"no secret":    {DB: h.db, Endpoints: h.g.endpoints(), ClientID: "c", PublicOrigin: testOrigin},
		"no origin":    {DB: h.db, Endpoints: h.g.endpoints(), ClientID: "c", ClientSecret: "s"},
		"no db":        {Endpoints: h.g.endpoints(), ClientID: "c", ClientSecret: "s", PublicOrigin: testOrigin},
		"no endpoints": {DB: h.db, ClientID: "c", ClientSecret: "s", PublicOrigin: testOrigin},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%s: New succeeded; there is no degraded mode", name)
		}
	}
}

// Single use must come from the app's own state store, not from the other
// layers that happen to stop a replay (cleared cookie, one-shot provider code):
// give the replay everything else it needs.
func TestStateIsSingleUseEvenWithValidCookieAndFreshCode(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	h.g.claims = func(n string) map[string]any { return h.g.goodClaims("sub-s", "s@example.test", n) }
	state, code := b.begin()
	flowCookieValue := b.cookies[flowCookie]
	if w := b.callback(state, code); w.Header().Get("Location") != "/" {
		t.Fatal("first use failed")
	}
	// The attacker keeps the browser binding and obtains another valid code for the same flow.
	b.cookies[flowCookie] = flowCookieValue
	h.g.mu.Lock()
	for k := range h.g.codes {
		delete(h.g.codes, k)
	}
	h.g.mu.Unlock()
	h.g.acceptAny = true
	defer func() { h.g.acceptAny = false }()
	n, _ := nonceByCode.Load(code) // the real nonce: only single-use of the state can stop this
	nonceByCode.Store("code-replay", n)
	w := b.callback(state, "code-replay")
	if w.Header().Get("Location") != "/error?code=login_failed" {
		t.Fatalf("replayed state accepted: %d %s", w.Code, w.Header().Get("Location"))
	}
	if n := h.count(`SELECT COUNT(*) FROM app_sessions`); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
}
