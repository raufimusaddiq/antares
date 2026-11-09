package blog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/auth"
	"github.com/raufimusaddiq/antares/internal/secrets"
	"github.com/raufimusaddiq/antares/internal/server"
	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/webui"
)

const testOrigin = "https://blog.example.test"

// app is the one harness every blog test uses: a real database, the real identity layer and server, and accounts in any
// state with a live session. No Hermes: the plain half of the blog never calls it.
type app struct {
	t   testing.TB
	db  *sql.DB
	svc *Service
	h   http.Handler
	box *secrets.Box
	fh  *fakeHermes
}

type person struct {
	a       *app
	id      string
	cookie  string
	profile string
}

func newApp(t testing.TB) *app { return newAppWithHermes(t, nil) }

// newAppWithHermes also turns the AI check on, against a scripted Hermes (nil: checking is not offered).
func newAppWithHermes(t testing.TB, fh *fakeHermes) *app { return newAppWith(t, fh, nil) }

type spendFake struct{ reached bool }

func (s *spendFake) HardLimitReached(context.Context, string) (bool, error) { return s.reached, nil }

func newAppWith(t testing.TB, fh *fakeHermes, spend Spend) *app {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "antares.sqlite"))
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
	opt := Options{DB: db}
	if fh != nil {
		opt.Hermes, opt.Box, opt.Spend = fh, box, spend
	}
	svc, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	au, err := auth.New(auth.Options{DB: db, ClientID: "c", ClientSecret: "s", PublicOrigin: testOrigin,
		Endpoints: auth.Endpoints{Issuer: "i", Auth: "https://a", Token: "https://t", JWKS: "https://j", Issued: []string{"i"}}, Blog: svc})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(func(context.Context) error { return nil }, "").WithAssets(webui.FS()).WithAuth(au).
		WithBlog(func(ctx context.Context, slug string) (server.PageMeta, error) {
			m, err := svc.Meta(ctx, slug)
			if errors.Is(err, ErrNotFound) {
				err = server.ErrNoPage
			}
			return server.PageMeta{Title: m.Title, Description: m.Description, Image: m.Image}, err
		}, au.BlogReadLimit()).Routes()
	return &app{t: t, db: db, svc: svc, h: h, box: box, fh: fh}
}

// user creates an account with the given statuses and a session. ACTIVE needs APPROVED, READY and a ready binding.
func (a *app) user(sub, approval, tenant string, bound bool) *person {
	a.t.Helper()
	id := store.NewID()
	if _, err := a.db.Exec(`INSERT INTO users (id, google_issuer, google_sub, email, display_name, role, approval_status, tenant_status, created_at)
		VALUES (?, 'iss', ?, ?, ?, 'USER', ?, ?, 'x')`, id, sub, sub+"@example.test", "Author "+sub, approval, tenant); err != nil {
		a.t.Fatal(err)
	}
	p := &person{a: a, id: id, cookie: "tok-" + sub + "-" + store.NewID(), profile: "u_" + sub + "profile"}
	if bound {
		bid := store.NewID()
		sealed, err := a.box.Seal("fabricated-key-"+sub+"-0123456789", bid)
		if err != nil {
			a.t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO tenant_bindings (id,user_id,profile_name,routing_mode,upstream_endpoint_id,credential_ciphertext,
			credential_key_version,credential_fingerprint,memory_scope_key,status,verified_at,created_at)
			VALUES (?,?,?,'MUX_PREFIX','ep',?,1,?,?, 'READY','2026-01-01T00:00:00.000000000Z','x')`,
			bid, id, p.profile, sealed, "fp-"+sub, "mem-"+sub); err != nil {
			a.t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte(p.cookie))
	if err := store.CreateSession(context.Background(), a.db, id, hex.EncodeToString(sum[:]), time.Now(), time.Hour); err != nil {
		a.t.Fatal(err)
	}
	return p
}

// active is the ordinary approved, ready account.
func (a *app) active(sub string) *person { return a.user(sub, "APPROVED", "READY", true) }

func (p *person) do(method, path string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
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
	w := httptest.NewRecorder()
	p.a.h.ServeHTTP(w, r)
	return w
}

// anon is a request from someone who is not signed in.
func (a *app) anon(method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, httptest.NewRequest(method, testOrigin+path, nil))
	return w
}

func into[T any](t testing.TB, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("not JSON (%d): %s", w.Code, w.Body.String())
	}
	return v
}

func (p *person) create() Post {
	p.a.t.Helper()
	w := p.do("POST", "/api/blog/posts", nil)
	if w.Code != 200 {
		p.a.t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return into[Post](p.a.t, w)
}

// publish saves a title and body, sets the address and publishes the original.
func (p *person) publish(title, slug, text string) Post {
	p.a.t.Helper()
	post := p.create()
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
	if w := p.do("PUT", "/api/blog/posts/"+post.ID, map[string]any{"baseVersion": post.LatestVersion, "title": title, "doc": doc}); w.Code != 200 {
		p.a.t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := p.do("PATCH", "/api/blog/posts/"+post.ID+"/settings", map[string]any{"slug": slug, "summary": "About " + title}); w.Code != 200 {
		p.a.t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	w := p.do("POST", "/api/blog/posts/"+post.ID+"/publish", map[string]any{"mode": "original"})
	if w.Code != 200 {
		p.a.t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	return into[Post](p.a.t, w)
}
