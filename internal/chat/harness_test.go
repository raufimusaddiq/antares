package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/push"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/webui"
)

const testOrigin = "https://chat.example.test"

// ---- a fake Hermes ----------------------------------------------------------------
//
// It speaks the documented shapes (PRD §9A), enforces a per-profile key like
// the real multiplexed API, and records every request so tests can prove what
// did and did not reach it. All values are fabricated.

type rec struct {
	Method, Path, Auth, SessionKey, Idem, Body string
	Header                                     http.Header
}

type fakeRun struct {
	profile string
	input   string
	frames  chan string
	status  string // what GET /v1/runs/{id} reports
	output  string
	usage   string // raw JSON for GET /v1/runs/{id} "usage"; "" omits it
	stopped bool
	opens   chan struct{} // one token per upstream stream subscription
	ended   chan struct{} // one token per upstream stream that ended
}

type fakeHermes struct {
	t    testing.TB
	srv  *httptest.Server
	mu   sync.Mutex
	keys map[string]string // profile -> key
	reqs []rec
	runs map[string]*fakeRun
	msgs map[string][]map[string]any
	n    int
	// mem models Hermes' per-profile memory for the memory-scoping tests: "remember X"
	// stores X under the calling profile, "recall…" answers with what that profile holds.
	// It is a model of the property under test, not Hermes itself (docs/runbooks/memory-scoping.md).
	mem map[string]string

	canned map[string]string // GET path -> JSON body, for the discovery routes

	failNext map[string]int // "METHOD suffix" -> status to return once (sticky when negative)
	hang     map[string]bool
}

func newFakeHermes(t testing.TB) *fakeHermes {
	f := &fakeHermes{t: t, keys: map[string]string{}, runs: map[string]*fakeRun{}, msgs: map[string][]map[string]any{}, mem: map[string]string{},
		failNext: map[string]int{}, hang: map[string]bool{}, canned: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHermes) requests() []rec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rec(nil), f.reqs...)
}

func (f *fakeHermes) run(id string) *fakeRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id]
}

// runWithInput waits for a run with this input to exist (the worker is async).
func (f *fakeHermes) runWithInput(t testing.TB, input string) (string, *fakeRun) {
	t.Helper()
	var id string
	var fr *fakeRun
	eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for k, r := range f.runs {
			if r.input == input {
				id, fr = k, r
				return true
			}
		}
		return false
	}, "fake Hermes to receive the run")
	return id, fr
}

func (f *fakeHermes) push(r *fakeRun, name, data string) {
	f.mu.Lock()
	ch := r.frames
	f.mu.Unlock()
	ch <- "event: " + name + "\ndata: " + data + "\n\n"
}

// dropStream ends the current upstream connection without a terminal frame, as
// a broken network would, and arms a fresh channel for the next subscription.
func (f *fakeHermes) dropStream(r *fakeRun) {
	f.mu.Lock()
	old := r.frames
	r.frames = make(chan string, 64)
	f.mu.Unlock()
	close(old)
}

func (f *fakeHermes) setStatus(r *fakeRun, status, output string) {
	f.mu.Lock()
	r.status, r.output = status, output
	f.mu.Unlock()
}

func recv(t testing.TB, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func (f *fakeHermes) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rest, ok := strings.CutPrefix(r.URL.Path, "/p/")
	profile, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	f.mu.Lock()
	f.reqs = append(f.reqs, rec{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"),
		SessionKey: r.Header.Get("X-Hermes-Session-Key"), Idem: r.Header.Get("Idempotency-Key"), Body: string(body), Header: r.Header.Clone()})
	key, known := f.keys[profile]
	failKey := r.Method + " " + path
	var forced int
	for k, st := range f.failNext {
		if strings.HasPrefix(failKey, k) {
			forced = st
			if st > 0 {
				delete(f.failNext, k)
			}
		}
	}
	hang := false
	for k := range f.hang {
		if strings.HasPrefix(failKey, k) {
			hang = true
		}
	}
	f.mu.Unlock()
	switch {
	case !ok || !known:
		http.Error(w, "Unknown or unconfigured profile", http.StatusNotFound)
		return
	case r.Header.Get("Authorization") != "Bearer "+key:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case hang:
		<-r.Context().Done()
		return
	case forced != 0:
		w.WriteHeader(forced)
		fmt.Fprintf(w, `{"error":"upstream said no; key was %s"}`, key) // must never be echoed onward
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && path == "/api/sessions":
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("sess_%s_%d", profile, f.n)
		f.mu.Unlock()
		fmt.Fprintf(w, `{"id":%q}`, id)
	case r.Method == "POST" && path == "/v1/runs":
		var in struct{ SessionID, Input string }
		var raw map[string]string
		_ = json.Unmarshal(body, &raw)
		in.SessionID, in.Input = raw["session_id"], raw["input"]
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("run_%d", f.n)
		out := ""
		if rest, ok := strings.CutPrefix(in.Input, "remember "); ok {
			f.mem[profile] = rest
		} else if strings.HasPrefix(in.Input, "recall") {
			out = f.mem[profile]
		}
		f.runs[id] = &fakeRun{profile: profile, input: in.Input, output: out, frames: make(chan string, 64), status: "running",
			opens: make(chan struct{}, 16), ended: make(chan struct{}, 16)}
		f.mu.Unlock()
		fmt.Fprintf(w, `{"run_id":%q,"status":"started"}`, id)
	case r.Method == "GET" && strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/messages"):
		sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/sessions/"), "/messages")
		f.mu.Lock()
		m := f.msgs[sid]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": m})
	case strings.HasPrefix(path, "/v1/runs/"):
		parts := strings.Split(strings.TrimPrefix(path, "/v1/runs/"), "/")
		f.mu.Lock()
		fr := f.runs[parts[0]]
		f.mu.Unlock()
		if fr == nil || fr.profile != profile {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1 && r.Method == "GET":
			f.mu.Lock()
			st, out, usage := fr.status, fr.output, fr.usage
			f.mu.Unlock()
			if usage != "" {
				usage = `,"usage":` + usage
			}
			fmt.Fprintf(w, `{"run_id":%q,"status":%q,"output":%q%s}`, parts[0], st, out, usage)
		case len(parts) == 2 && parts[1] == "stop" && r.Method == "POST":
			f.mu.Lock()
			fr.stopped, fr.status = true, "cancelled"
			f.mu.Unlock()
			fmt.Fprint(w, `{"status":"stopping"}`)
		case len(parts) == 2 && parts[1] == "steer" && r.Method == "POST":
			// Hermes: 200 {"object":"hermes.run.steer","accepted":true} while running, else 409
			// run_not_accepting_steer (api_server_runs.py _steer_refused).
			f.mu.Lock()
			running := fr.status == "running" && !fr.stopped
			f.mu.Unlock()
			if !running {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"error":{"code":"run_not_accepting_steer"}}`)
				return
			}
			fmt.Fprintf(w, `{"object":"hermes.run.steer","run_id":%q,"accepted":true}`, parts[0])
		case len(parts) == 2 && parts[1] == "events" && r.Method == "GET":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			f.mu.Lock()
			frames := fr.frames
			f.mu.Unlock()
			fr.opens <- struct{}{}
			defer func() { fr.ended <- struct{}{} }()
			for {
				select {
				case fr_, ok := <-frames:
					if !ok {
						return
					}
					io.WriteString(w, fr_)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	case r.Method == "GET" && f.cannedBody(path) != "":
		io.WriteString(w, f.cannedBody(path))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeHermes) cannedBody(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canned[path]
}

// ---- the app ------------------------------------------------------------------------

type app struct {
	t      testing.TB
	db     *sql.DB
	box    *secrets.Box
	svc    *Service
	hx     *fakeHermes
	h      http.Handler
	users  map[string]*person
	limits *auth.Limits
	push   *push.Service // set by withPush; nil leaves the push routes unmounted

	seenMu  sync.Mutex
	seen    []string  // every response a browser would have received: headers and body
	streams []*stream // every SSE stream opened
}

type person struct {
	a       *app
	sub     string
	id      string
	profile string
	key     string
	cookie  string
	seen    []string // what this person's browser received
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

var logBuf = &syncBuf{}

func init() { log.SetOutput(logBuf) }

type appOpts struct {
	limits    *auth.Limits
	backoff   func(int) time.Duration
	client    hermes.ClientOptions
	heartbeat time.Duration
}

func newApp(t testing.TB, o ...appOpts) *app {
	t.Helper()
	var opt appOpts
	if len(o) > 0 {
		opt = o[0]
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "db", "antares.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(context.Background(), db, store.Migrations()); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New([]byte("fabricated-master-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	hx := newFakeHermes(t)
	cl, err := hermes.NewClient(hx.srv.URL, opt.client)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Options{DB: db, Box: box, Hermes: cl, Backoff: opt.backoff, Heartbeat: opt.heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	ap := &app{t: t, db: db, box: box, svc: svc, hx: hx, users: map[string]*person{}, limits: opt.limits}
	ap.h = rebuild(t, ap, svc)
	logBuf.mu.Lock()
	logBuf.b.Reset()
	logBuf.mu.Unlock()
	return ap
}

// rebuild mounts svc behind a fresh auth layer on the app's database, as a
// restarted process would.
func rebuild(t testing.TB, a *app, svc *Service) http.Handler {
	t.Helper()
	au, err := auth.New(auth.Options{DB: a.db, ClientID: "c", ClientSecret: "s", PublicOrigin: testOrigin,
		Endpoints: auth.Endpoints{Issuer: "i", Auth: "https://a", Token: "https://t", JWKS: "https://j", Issued: []string{"i"}}, Chat: svc, Limits: a.limits, Push: pushHandlers(a)})
	if err != nil {
		t.Fatal(err)
	}
	return server.New(func(context.Context) error { return nil }, "").WithAssets(webui.FS()).WithAuth(au).Routes()
}

// newHTTPServer is a real net/http server with a ReadTimeout far shorter than
// the stream, as production sets one (cmd/antares/main.go): a stream must
// outlive it.
func newHTTPServer(h http.Handler) *httptest.Server {
	s := httptest.NewUnstartedServer(h)
	s.Config.ReadTimeout = 300 * time.Millisecond
	s.Start()
	return s
}

// user creates an approved account with a verified, READY binding on the fake
// Hermes and a live session. sub must be lower-case letters/digits.
func (a *app) user(sub string) *person {
	a.t.Helper()
	id := store.NewID()
	if _, err := a.db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, 'USER', 'APPROVED', 'READY', 'x')`, id, sub, sub+"@example.test"); err != nil {
		a.t.Fatal(err)
	}
	p := &person{a: a, sub: sub, id: id, profile: "u_" + sub + "profile", key: "fabricated-key-" + sub + "-0123456789"}
	bid := store.NewID()
	sealed, err := a.box.Seal(p.key, bid)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,
		credential_key_version,credential_fingerprint,memory_scope_key,status,verified_at,created_at)
		VALUES (?,?,?,'MUX_PREFIX','ep',?,1,?,?, 'READY','2026-01-01T00:00:00.000000000Z','x')`,
		bid, id, p.profile, sealed, a.box.Fingerprint(p.key), "mem-"+sub); err != nil {
		a.t.Fatal(err)
	}
	p.cookie = "tok-" + sub + "-" + store.NewID()
	sum := sha256.Sum256([]byte(p.cookie))
	if err := store.CreateSession(context.Background(), a.db, id, hex.EncodeToString(sum[:]), time.Now(), time.Hour); err != nil {
		a.t.Fatal(err)
	}
	a.hx.mu.Lock()
	a.hx.keys[p.profile] = p.key
	a.hx.mu.Unlock()
	a.users[sub] = p
	return p
}

func (p *person) newRequest(method, path string, hdr map[string]string, body any) *http.Request {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, testOrigin+path, rd)
	r.AddCookie(&http.Cookie{Name: "__Host-antares_session", Value: p.cookie})
	if method != "GET" {
		r.Header.Set("Origin", testOrigin)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func (p *person) do(method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	p.a.h.ServeHTTP(w, p.newRequest(method, path, hdr, body))
	rec := fmt.Sprintf("%s %s -> %d\n%v\n%s", method, path, w.Code, w.Header(), w.Body.String())
	p.a.seenMu.Lock()
	p.a.seen = append(p.a.seen, rec)
	p.seen = append(p.seen, rec)
	p.a.seenMu.Unlock()
	return w
}

// account creates a signed-in account in any state, with no Hermes binding.
func (a *app) account(sub, role, approval, tenant string) *person {
	a.t.Helper()
	id := store.NewID()
	if _, err := a.db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, ?, ?, ?, 'x')`, id, sub, sub+"@example.test", role, approval, tenant); err != nil {
		a.t.Fatal(err)
	}
	p := &person{a: a, sub: sub, id: id}
	p.cookie = "tok-" + sub + "-" + store.NewID()
	sum := sha256.Sum256([]byte(p.cookie))
	if err := store.CreateSession(context.Background(), a.db, id, hex.EncodeToString(sum[:]), time.Now(), time.Hour); err != nil {
		a.t.Fatal(err)
	}
	a.users[sub] = p
	return p
}

func (p *person) get(path string) *httptest.ResponseRecorder { return p.do("GET", path, nil, nil) }

func decodeBody[T any](t testing.TB, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("not JSON (%d): %q", w.Code, w.Body.String())
	}
	return v
}

func errCode(t testing.TB, w *httptest.ResponseRecorder) string {
	t.Helper()
	e := decodeBody[struct{ Error struct{ Code string } }](t, w)
	return e.Error.Code
}

func (p *person) newChat() string {
	p.a.t.Helper()
	w := p.do("POST", "/api/chats", nil, map[string]string{"title": "t"})
	if w.Code != 201 {
		p.a.t.Fatalf("create chat: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ ID string }](p.a.t, w).ID
}

var idemSeq int
var idemMu sync.Mutex

func nextIdem() string {
	idemMu.Lock()
	defer idemMu.Unlock()
	idemSeq++
	return fmt.Sprintf("idem-key-%06d", idemSeq)
}

// send posts a message and returns the app run id.
func (p *person) send(chatID, text string) string {
	p.a.t.Helper()
	w := p.do("POST", "/api/chats/"+chatID+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": text})
	if w.Code != 202 {
		p.a.t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ RunID string }](p.a.t, w).RunID
}

func (p *person) runStatus(runID string) string {
	w := p.get("/api/runs/" + runID)
	return decodeBody[struct{ Status string }](p.a.t, w).Status
}

// ---- SSE client -----------------------------------------------------------------------

type sseEv struct{ ID, Name, Data string }

type stream struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	status int
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *stream) Header() http.Header { return s.header }
func (s *stream) WriteHeader(c int)   { s.status = c }
func (s *stream) Flush()              {}
func (s *stream) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *stream) events() []sseEv {
	s.mu.Lock()
	raw := s.buf.String()
	s.mu.Unlock()
	var out []sseEv
	for _, block := range strings.Split(raw, "\n\n") {
		var e sseEv
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				e.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				e.Name = line[7:]
			case strings.HasPrefix(line, "data: "):
				e.Data = line[6:]
			}
		}
		if e.Name != "" {
			out = append(out, e)
		}
	}
	return out
}

func (s *stream) names() []string {
	var n []string
	for _, e := range s.events() {
		n = append(n, e.Name)
	}
	return n
}

// text is the concatenation of every assistant.delta, as the browser renders it.
func (s *stream) text() string {
	var b strings.Builder
	for _, e := range s.events() {
		switch e.Name {
		case "assistant.delta":
			var d struct{ Text string }
			_ = json.Unmarshal([]byte(e.Data), &d)
			b.WriteString(d.Text)
		case "run.resumed":
			b.Reset()
		}
	}
	return b.String()
}

func (s *stream) has(name string) bool {
	for _, n := range s.names() {
		if n == name {
			return true
		}
	}
	return false
}

// open connects like a browser's EventSource. lastID is sent as Last-Event-ID.
func (p *person) open(runID, lastID string) *stream {
	ctx, cancel := context.WithCancel(context.Background())
	hdr := map[string]string{"Accept": "text/event-stream"}
	if lastID != "" {
		hdr["Last-Event-ID"] = lastID
	}
	r := p.newRequest("GET", "/api/runs/"+runID+"/events", hdr, nil).WithContext(ctx)
	s := &stream{header: http.Header{}, cancel: cancel, done: make(chan struct{})}
	p.a.seenMu.Lock()
	p.a.streams = append(p.a.streams, s)
	p.a.seenMu.Unlock()
	go func() {
		defer close(s.done)
		p.a.h.ServeHTTP(s, r)
	}()
	p.a.t.Cleanup(func() { cancel(); <-s.done })
	return s
}

func (s *stream) waitFor(t testing.TB, name string) {
	t.Helper()
	eventually(t, func() bool { return s.has(name) }, "event "+name)
}

func (s *stream) close() { s.cancel(); <-s.done }

func eventually(t testing.TB, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// pushHandlers is the push service as auth.Options wants it: a nil interface, not a nil
// pointer inside one, when the app has none.
func pushHandlers(a *app) auth.ChatHandlers {
	if a.push == nil {
		return nil
	}
	return a.push
}
