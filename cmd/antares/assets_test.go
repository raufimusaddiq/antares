package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultServesTheEmbeddedBundle(t *testing.T) {
	h := newServer(nil, "").Routes() // no ANTARES_STATIC_DIR, no directory on disk
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<div id="root">`) {
		t.Fatalf("/ = %d %q", rec.Code, rec.Body.String())
	}
}

func TestStaticDirOverrideReplacesTheBundle(t *testing.T) {
	h := newServer(nil, t.TempDir()).Routes() // empty dir: no shell, so 404 — not the embedded one
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 404 {
		t.Fatalf("/ = %d", rec.Code)
	}
}
