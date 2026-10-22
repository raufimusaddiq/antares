package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzAlwaysOK(t *testing.T) {
	s := New(func(context.Context) error { return errors.New("db down") }, t.TempDir())
	if rec := get(s.Routes(), "/healthz"); rec.Code != 200 {
		t.Fatalf("healthz %d", rec.Code)
	}
}

func TestReadyzReflectsChecker(t *testing.T) {
	var err error
	s := New(func(context.Context) error { return err }, t.TempDir())
	if rec := get(s.Routes(), "/readyz"); rec.Code != 200 {
		t.Fatalf("ready %d", rec.Code)
	}
	err = errors.New("open /secret/path/antares.db: boom")
	rec := get(s.Routes(), "/readyz")
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("not ready: %d %s", rec.Code, rec.Body)
	}
}

func TestStaticServing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>shell"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("js"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.txt"), []byte("nope"), 0o644)
	h := New(nil, dir).Routes()
	if rec := get(h, "/app.js"); rec.Code != 200 || rec.Body.String() != "js" {
		t.Fatalf("asset: %d", rec.Code)
	}
	if rec := get(h, "/chats/123"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "shell") {
		t.Fatalf("spa fallback: %d", rec.Code)
	}
	if rec := get(h, "/missing.js"); rec.Code != 404 {
		t.Fatalf("missing asset: %d", rec.Code)
	}
	if rec := get(h, "/api/nothing"); rec.Code != 404 || strings.Contains(rec.Body.String(), "shell") {
		t.Fatalf("api must not fall back: %d", rec.Code)
	}
	if rec := get(h, "/../outside.txt"); strings.Contains(rec.Body.String(), "nope") {
		t.Fatal("path traversal")
	}
}

func TestTrailingSlashProbesDoNotServeTheShell(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>shell"), 0o644)
	s := New(func(context.Context) error { return errors.New("db down") }, dir)
	for _, p := range []string{"/readyz/", "/healthz/"} {
		rec := get(s.Routes(), p)
		if strings.Contains(rec.Body.String(), "<!doctype") {
			t.Errorf("%s served the SPA shell (status %d)", p, rec.Code)
		}
	}
	if rec := get(s.Routes(), "/readyz/"); rec.Code != 503 {
		t.Errorf("/readyz/ = %d with the database down, want 503", rec.Code)
	}
	if rec := get(s.Routes(), "/healthz/"); rec.Code != 200 {
		t.Errorf("/healthz/ = %d, want 200", rec.Code)
	}
}

func TestReservedPrefixesNeverServeTheShell(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>shell"), 0o644)
	h := New(func(context.Context) error { return errors.New("db down") }, dir).Routes()
	for _, p := range []string{"/API/x", "/Api/x", "/api", "/API", "/readyz/x", "/readyz/health", "/healthz/x", "/READYZ", "/readyz//", "/readyz/./x"} {
		rec := get(h, p)
		if strings.Contains(rec.Body.String(), "<!doctype") || rec.Code != 404 {
			t.Errorf("%s -> %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if rec := get(h, "/chats/readyz"); rec.Code != 200 {
		t.Errorf("an unrelated client route must still get the shell: %d", rec.Code)
	}
}

func TestAPIMixedCaseIDsReachTheMux(t *testing.T) {
	s := New(nil, t.TempDir())
	mux := s.routes()
	mux.HandleFunc("GET /api/chats/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"id": r.PathValue("id")})
	})
	h := reserve(mux)
	if rec := get(h, "/api/chats/AbC"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "AbC") {
		t.Fatalf("opaque mixed-case id must reach the route: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/API/chats/abc"); rec.Code != 404 {
		t.Fatalf("uppercase prefix stays reserved: %d", rec.Code)
	}
}
