package smoke

// A real Antares (store, auth, tenant pipeline, chat, HTTP server) in-process,
// talking over HTTP to a fake provisioner and a fake Hermes, with a fake OIDC
// provider for the login. Everything the smoke test sends crosses a real socket
// to the real handlers; only the two upstreams and Google are fabricated. All
// values are fabricated.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/chat"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/tenant"
)

const (
	testOrigin   = "https://antares.smoke.test"
	provToken    = "fabricated-provisioner-token-0123456789abcdef"
	clientID     = "smoke-client-id"
	clientSecret = "fabricated-client-secret-0123456789"
)

// ---- fake OIDC provider ---------------------------------------------------------------

type idp struct {
	url  string
	key  *rsa.PrivateKey
	mu   sync.Mutex
	subs map[string]idpGrant // code -> grant
}

type idpGrant struct {
	sub, email, nonce, challenge, aud string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: k, subs: map[string]idpGrant{}}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return p
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *idp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/jwks":
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": b64(p.key.N.Bytes()), "e": b64(big.NewInt(int64(p.key.E)).Bytes())}}})
	case "/token":
		_ = r.ParseForm()
		p.mu.Lock()
		g, ok := p.subs[r.Form.Get("code")]
		delete(p.subs, r.Form.Get("code"))
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || b64(sum[:]) != g.challenge || r.Form.Get("client_secret") != clientSecret {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		now := time.Now().Unix()
		hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		pl, _ := json.Marshal(map[string]any{"iss": p.url, "aud": g.aud, "sub": g.sub, "email": g.email, "email_verified": true,
			"name": g.sub, "nonce": g.nonce, "iat": now, "exp": now + 600})
		signing := b64(hdr) + "." + b64(pl)
		d := sha256.Sum256([]byte(signing))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, d[:])
		json.NewEncoder(w).Encode(map[string]string{"id_token": signing + "." + b64(sig), "access_token": "unused"})
	default:
		http.NotFound(w, r)
	}
}

func (p *idp) endpoints() auth.Endpoints {
	return auth.Endpoints{Issuer: p.url, Auth: p.url + "/authorize", Token: p.url + "/token", JWKS: p.url + "/jwks", Issued: []string{p.url}}
}

// login completes the whole Authorization Code + PKCE flow against Antares the way
// a browser does: /start, the provider's redirect target, the callback.
func (p *idp) login(c *Client, sub string) func(context.Context) (*Session, error) {
	return func(ctx context.Context) (*Session, error) {
		start, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/api/auth/google/start", nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(start)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || resp.StatusCode != 302 || !strings.HasPrefix(loc.String(), p.url+"/authorize") {
			return nil, fmt.Errorf("start did not redirect to the provider: %d %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		var flow string
		for _, ck := range resp.Cookies() {
			if ck.Name == "__Host-antares_oidc" {
				flow = ck.Value
			}
		}
		q := loc.Query()
		code := "code-" + sub
		p.mu.Lock()
		p.subs[code] = idpGrant{sub: sub, email: sub + "@smoke.invalid", nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), aud: q.Get("client_id")}
		p.mu.Unlock()
		cb, _ := http.NewRequestWithContext(ctx, "GET", c.Base+"/api/auth/google/callback?code="+code+"&state="+url.QueryEscape(q.Get("state")), nil)
		cb.AddCookie(&http.Cookie{Name: "__Host-antares_oidc", Value: flow})
		resp, err = c.HTTP.Do(cb)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		for _, ck := range resp.Cookies() {
			if ck.Name == SessionCookie && ck.Value != "" {
				return &Session{Name: sub, Cookie: ck.Value}, nil
			}
		}
		return nil, fmt.Errorf("callback issued no session (HTTP %d, Location %q)", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// ---- fake Hermes + provisioner ---------------------------------------------------------

type fakeHermes struct {
	mu       sync.Mutex
	srv      *httptest.Server
	keys     map[string]string // profile -> key
	sessions map[string]string // session id -> profile
	msgs     map[string][]map[string]any
	runs     map[string]*fakeRun
	n        int
	allowed  map[string]bool
	stale    bool   // the provisioner never wrote agent.disabled_toolsets
	mode     string // "ok", "fail", "empty", "hang"
}

type fakeRun struct {
	profile, session, input, mode string
}

func newFakeHermes(t *testing.T) *fakeHermes {
	f := &fakeHermes{keys: map[string]string{}, sessions: map[string]string{}, msgs: map[string][]map[string]any{},
		runs: map[string]*fakeRun{}, allowed: map[string]bool{}, mode: "ok"}
	for _, n := range hermes.DefaultAllowedToolsets {
		f.allowed[n] = true
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHermes) setMode(m string) { f.mu.Lock(); f.mode = m; f.mu.Unlock() }
func (f *fakeHermes) setStale(v bool)  { f.mu.Lock(); f.stale = v; f.mu.Unlock() }

func (f *fakeHermes) serve(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/p/")
	profile, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	f.mu.Lock()
	key, known := f.keys[profile]
	stale, mode := f.stale, f.mode
	f.mu.Unlock()
	switch {
	case !ok: // the default profile's route: a tenant key opens nothing there
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case !known:
		http.Error(w, "unknown profile", http.StatusNotFound)
		return
	case r.Header.Get("Authorization") != "Bearer "+key:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch {
	case path == "/v1/capabilities":
		io.WriteString(w, `{"object":"hermes.api_server.capabilities","auth":{"required":true},"features":{"memory_write_api":false,"admin_config_rw":false}}`)
	case path == "/v1/models":
		io.WriteString(w, `{"object":"list","data":[]}`)
	case path == "/v1/toolsets":
		var data []map[string]any
		for _, n := range hermes.KnownToolsets {
			on := f.allowed[n]
			if stale && n == "terminal" {
				on = true // the helper never wrote agent.disabled_toolsets
			}
			data = append(data, map[string]any{"name": n, "enabled": on})
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	case r.Method == "GET" && path == "/api/sessions":
		io.WriteString(w, `{"object":"list","data":[],"limit":1,"offset":0,"has_more":false}`)
	case r.Method == "POST" && path == "/api/sessions":
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("sess_%d", f.n)
		f.sessions[id] = profile
		f.mu.Unlock()
		fmt.Fprintf(w, `{"id":%q}`, id)
	case r.Method == "GET" && strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/messages"):
		sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/sessions/"), "/messages")
		f.mu.Lock()
		owner, m := f.sessions[sid], f.msgs[sid]
		f.mu.Unlock()
		if owner != profile {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": m})
	case r.Method == "POST" && path == "/v1/runs":
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("run_%d", f.n)
		f.runs[id] = &fakeRun{profile: profile, session: in["session_id"], input: in["input"], mode: mode}
		f.mu.Unlock()
		fmt.Fprintf(w, `{"run_id":%q,"status":"started"}`, id)
	case strings.HasPrefix(path, "/v1/runs/"):
		parts := strings.Split(strings.TrimPrefix(path, "/v1/runs/"), "/")
		f.mu.Lock()
		run := f.runs[parts[0]]
		f.mu.Unlock()
		if run == nil || run.profile != profile {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1:
			st, out := f.outcome(run)
			fmt.Fprintf(w, `{"run_id":%q,"status":%q,"output":%q}`, parts[0], st, out)
		case len(parts) == 2 && parts[1] == "events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			if run.mode == "hang" {
				<-r.Context().Done()
				return
			}
			st, out := f.outcome(run)
			if st == "completed" {
				fmt.Fprintf(w, "event: assistant.delta\ndata: %s\n\n", mustJSON(map[string]string{"delta": out}))
				fmt.Fprintf(w, "event: run.completed\ndata: %s\n\n", mustJSON(map[string]string{"output": out}))
			} else {
				fmt.Fprint(w, "event: run.failed\ndata: {}\n\n")
			}
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// outcome is the run's result; it also writes the turns into the transcript the
// first time a finished run is read.
func (f *fakeHermes) outcome(run *fakeRun) (status, output string) {
	switch run.mode {
	case "hang":
		return "running", ""
	case "fail":
		return "failed", ""
	case "empty":
		f.record(run, "")
		return "completed", ""
	}
	out := "Hello from the fake Hermes: I am working."
	f.record(run, out)
	return "completed", out
}

func (f *fakeHermes) record(run *fakeRun, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs[run.session]) > 0 {
		return
	}
	f.msgs[run.session] = []map[string]any{{"role": "user", "content": run.input}, {"role": "assistant", "content": reply}}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (f *fakeHermes) provisioner(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+provToken {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var in struct{ Slug string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		raw := make([]byte, 24)
		_, _ = rand.Read(raw)
		f.mu.Lock()
		_, exists := f.keys[in.Slug]
		if r.URL.Path == "/provision" && exists {
			f.mu.Unlock()
			http.Error(w, "exists", http.StatusConflict)
			return
		}
		key := hex.EncodeToString(raw)
		f.keys[in.Slug] = key
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"profile": in.Slug, "key": key, "created_at": "2026-10-23T00:00:00Z"})
	}))
	t.Cleanup(s.Close)
	return s
}

// ---- the stack ---------------------------------------------------------------------------

type stack struct {
	t       *testing.T
	idp     *idp
	hermes  *fakeHermes
	db      *sql.DB
	srv     *httptest.Server
	client  *Client
	red     *Redactor
	wrap    func(http.Handler) http.Handler
	cmu     sync.Mutex
	cookies map[string]string // sub -> session cookie value, filled as accounts sign in
}

func (s *stack) cookie(sub string) string {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.cookies[sub]
}

// newStack starts Antares. wrap, if set, sits in front of Antares' handler: it is
// how a test plants a defect (an ownership check that stopped working) to prove
// the smoke test notices it.
func newStack(t *testing.T, stale bool, wrap func(*stack, http.Handler) http.Handler) *stack {
	t.Helper()
	s := &stack{t: t, idp: newIDP(t), hermes: newFakeHermes(t), red: &Redactor{}, cookies: map[string]string{}}
	s.hermes.setStale(stale)
	prov := s.hermes.provisioner(t)

	db, err := store.Open(filepath.Join(t.TempDir(), "antares.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s.db = db
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := store.Migrate(ctx, db, store.Migrations()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BootstrapAdmin(ctx, db, s.idp.url, "sub-admin", "sub-admin@smoke.invalid", time.Now()); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := hermes.NewHelper(prov.URL, provToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := hermes.NewVerifier(s.hermes.srv.URL, hermes.DefaultAllowedToolsets, nil)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenant.New(tenant.Options{DB: db, Box: box, Helper: helper, Verifier: verifier, MaxAttempts: 2, BaseBackoff: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	go tenants.Run(ctx)
	hx, err := hermes.NewClient(s.hermes.srv.URL, hermes.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	chats, err := chat.New(chat.Options{DB: db, Box: box, Hermes: hx})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{DB: db, Endpoints: s.idp.endpoints(), ClientID: clientID, ClientSecret: clientSecret,
		PublicOrigin: testOrigin, Tenants: tenants, Chat: chats})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(func(context.Context) error { return nil }, t.TempDir()).WithAuth(identity).Routes()
	if wrap != nil {
		h = wrap(s, h)
	}
	s.srv = httptest.NewServer(h)
	t.Cleanup(func() {
		s.srv.CloseClientConnections()
		cancel()
		chats.Close()
		tenants.Close()
		s.srv.Close()
	})
	s.client = NewClient(s.srv.URL, testOrigin, s.red)
	return s
}

// actor signs a fabricated account in through the fake OIDC flow.
func (s *stack) actor(name, sub string) *Actor {
	login := s.idp.login(s.client, sub)
	return &Actor{Name: name, Login: func(ctx context.Context) (*Session, error) {
		sess, err := login(ctx)
		if err == nil {
			s.red.Add(sess.Cookie)
			s.cmu.Lock()
			s.cookies[sub] = sess.Cookie
			s.cmu.Unlock()
		}
		return sess, err
	}}
}

func (s *stack) config() *Config {
	return &Config{Client: s.client, RunID: "t1",
		Admin: s.actor("admin", "sub-admin"), A: s.actor("A", "sub-a"), B: s.actor("B", "sub-b"), Pending: s.actor("pending", "sub-pending"),
		RunTimeout: 15 * time.Second, TenantTimeout: 30 * time.Second, FailedGrace: 500 * time.Millisecond, Poll: 25 * time.Millisecond}
}

func (s *stack) run(cfg *Config) Report {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r := &Runner{Red: s.red}
	started := time.Now()
	Run(ctx, cfg, r)
	return r.Report(s.srv.URL, "t1", started)
}

func (r Report) step(id string) StepResult {
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	return StepResult{ID: id, Status: "missing"}
}

// waitAdminReady blocks until the bootstrap admin's own tenant is READY, so a test
// can change the world after that point.
func (s *stack) waitAdminReady() {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var st string
		if err := s.db.QueryRow(`SELECT tenant_status FROM users WHERE role = 'ADMIN'`).Scan(&st); err == nil && st == "READY" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatal("admin tenant never became READY")
}

// ownerSub is the google_sub of whoever owns the chat or run an /api path names.
func (s *stack) ownerSub(path string) string {
	var q string
	switch {
	case strings.HasPrefix(path, "/api/chats/"):
		q = `SELECT u.google_sub FROM chats c JOIN users u ON u.id = c.user_id WHERE c.id = ?`
	case strings.HasPrefix(path, "/api/runs/"):
		q = `SELECT u.google_sub FROM runs c JOIN users u ON u.id = c.user_id WHERE c.id = ?`
	default:
		return ""
	}
	id := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(path, "/api/chats/"), "/api/runs/"), "/", 2)[0]
	var sub string
	_ = s.db.QueryRow(q, id).Scan(&sub)
	return sub
}
