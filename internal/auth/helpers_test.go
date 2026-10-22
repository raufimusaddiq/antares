package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
)

// All values below are fabricated.
const (
	testOrigin = "https://chat.example.test"
	testClient = "client-id.example.test"
	testSecret = "fabricated-client-secret"
)

type fakeGoogle struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	now    func() time.Time
	mu     sync.Mutex
	codes  map[string]string // code -> challenge
	verify string            // last verifier seen on the token endpoint
	// claims returns the ID token claims for an exchange; nonce is the one the app sent.
	claims    func(nonce string) map[string]any
	tokenFor  func(claims map[string]any) string // override signing for negative tests
	tokenHit  int
	acceptAny bool // token endpoint accepts any code/verifier pair (replay tests)
}

func newFakeGoogle(t *testing.T, now func() time.Time) *fakeGoogle {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGoogle{t: t, key: key, kid: "kid-1", now: now, codes: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/certs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": g.kid,
			"n": b64(key.N.Bytes()), "e": b64([]byte{1, 0, 1}),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.tokenHit++
		chal, ok := g.codes[r.PostForm.Get("code")]
		delete(g.codes, r.PostForm.Get("code"))
		v := r.PostForm.Get("code_verifier")
		g.verify = v
		sum := sha256.Sum256([]byte(v))
		if g.acceptAny {
			ok, chal = true, base64.RawURLEncoding.EncodeToString(sum[:])
		}
		if !ok || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("client_id") != testClient ||
			r.PostForm.Get("client_secret") != testSecret || r.PostForm.Get("redirect_uri") != testOrigin+callbackPath ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != chal {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "fabricated-access-token", "id_token": g.mint(r.PostForm.Get("code"))})
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (g *fakeGoogle) endpoints() Endpoints {
	return Endpoints{
		Issuer: "https://accounts.google.com",
		Auth:   g.srv.URL + "/auth",
		Token:  g.srv.URL + "/token",
		JWKS:   g.srv.URL + "/certs",
		Issued: []string{"https://accounts.google.com", "accounts.google.com"},
	}
}

// nonces maps code -> nonce so the token endpoint can embed it.
var nonceByCode sync.Map

func (g *fakeGoogle) mint(code string) string {
	n, _ := nonceByCode.Load(code)
	nonce, _ := n.(string)
	c := g.claims(nonce)
	if g.tokenFor != nil {
		return g.tokenFor(c)
	}
	return g.sign(c, "RS256", g.kid, g.key)
}

func (g *fakeGoogle) sign(c map[string]any, alg, kid string, key *rsa.PrivateKey) string {
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	p, _ := json.Marshal(c)
	signing := b64(h) + "." + b64(p)
	if alg == "none" {
		return signing + "."
	}
	if key == nil { // an unsupported algorithm with a plausible-looking signature
		return signing + "." + b64([]byte("not-a-real-signature"))
	}
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		g.t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

// goodClaims is a valid token body for the given subject.
func (g *fakeGoogle) goodClaims(sub, email, nonce string) map[string]any {
	now := g.now()
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": testClient, "sub": sub, "email": email, "email_verified": true,
		"name": "Test Person", "picture": "https://img.example.test/p.png", "nonce": nonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

type harness struct {
	t   *testing.T
	a   *Auth
	db  *sql.DB
	g   *fakeGoogle
	h   http.Handler
	clk *clock
}

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

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "db", "antares.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(context.Background(), db, store.Migrations()); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g := newFakeGoogle(t, clk.Now)
	a, err := New(Options{DB: db, Endpoints: g.endpoints(), ClientID: testClient, ClientSecret: testSecret,
		PublicOrigin: testOrigin, HTTPClient: g.srv.Client(), Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(func(context.Context) error { return nil }, t.TempDir()).WithAuth(a).Routes()
	return &harness{t: t, a: a, db: db, g: g, h: h, clk: clk}
}

// browser is a cookie jar that, unlike net/http's, replays Secure cookies to
// the https test origin.
type browser struct {
	h       *harness
	cookies map[string]string
}

func (h *harness) browser() *browser { return &browser{h: h, cookies: map[string]string{}} }

func (b *browser) do(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, testOrigin+path, nil)
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

func (b *browser) get(path string) *httptest.ResponseRecorder { return b.do("GET", path, nil) }

// begin runs /start and returns the state sent to Google and registers the code
// Google would hand back, so a test can drive the callback by hand.
func (b *browser) begin() (state, code string) {
	b.h.t.Helper()
	w := b.get("/api/auth/google/start")
	if w.Code != http.StatusFound {
		b.h.t.Fatalf("start = %d", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		b.h.t.Fatal(err)
	}
	q := loc.Query()
	code = "code-" + q.Get("state")[:8]
	b.h.g.mu.Lock()
	b.h.g.codes[code] = q.Get("code_challenge")
	b.h.g.mu.Unlock()
	nonceByCode.Store(code, q.Get("nonce"))
	return q.Get("state"), code
}

func (b *browser) callback(state, code string) *httptest.ResponseRecorder {
	return b.get(callbackPath + "?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(code))
}

// login performs a whole successful login for (sub, email).
func (b *browser) login(sub, email string) *httptest.ResponseRecorder {
	b.h.t.Helper()
	b.h.g.claims = func(nonce string) map[string]any { return b.h.g.goodClaims(sub, email, nonce) }
	state, code := b.begin()
	w := b.callback(state, code)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		b.h.t.Fatalf("login callback = %d %s", w.Code, w.Header().Get("Location"))
	}
	return w
}

func (h *harness) exec(q string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(q, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) userID(sub string) string {
	h.t.Helper()
	var id string
	if err := h.db.QueryRow(`SELECT id FROM users WHERE google_sub = ?`, sub).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// userIn logs sub in, then forces the account into the given statuses (as an
// admin or the provisioner would later) and returns its browser.
func (h *harness) userIn(sub, approval, tenant, role string, bind bool) *browser {
	h.t.Helper()
	b := h.browser()
	b.login(sub, sub+"@example.test")
	id := h.userID(sub)
	h.exec(`UPDATE users SET approval_status=?, tenant_status=?, role=? WHERE id=?`, approval, tenant, role, id)
	if bind {
		h.bind(id, sub)
	}
	return b
}

const sentinelCiphertext = "SENTINEL-CIPHERTEXT-DO-NOT-LEAK"

func (h *harness) bind(userID, sub string) {
	h.exec(`INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,
		credential_key_version,credential_fingerprint,memory_scope_key,status,created_at)
		VALUES (?,?,?,'MUX_PREFIX','ep',?,1,?,?, 'READY','x')`,
		store.NewID(), userID, "profile-"+sub, []byte(sentinelCiphertext), "fp-"+sub, "mem-"+sub)
}

func has(s, sub string) bool { return strings.Contains(s, sub) }
