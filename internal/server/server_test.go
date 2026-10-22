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
