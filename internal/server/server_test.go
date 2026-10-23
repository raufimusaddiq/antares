package server

import (
	"context"
	"errors"
	"github.com/raufimusaddiq/antares/internal/webui"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
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

// The shell comes from the built bundle. These fail when the bundle is missing
// instead of falling back to some other page.
func TestRootServesTheBuiltBundle(t *testing.T) {
	h := New(nil, "").WithAssets(webui.FS()).Routes()
	for _, p := range []string{"/", "/pending", "/error", "/login"} {
		rec := get(h, p)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") ||
			!strings.Contains(csp, "style-src 'self' 'sha256-") || strings.Count(csp, "sha256-") != 1 ||
			!strings.Contains(csp, "font-src 'self';") {
			t.Errorf("%s csp = %q", p, csp)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s shell must not be cached", p)
		}
		wantCSP := "default-src 'none'; script-src 'self'; style-src 'self' " + openUIStyleHash + "; connect-src 'self'; img-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
		if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s CSP = %q, want %q", p, got, wantCSP)
		}
	}
	shell := get(h, "/").Body.String()
	m := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(shell)
	if m == nil {
		t.Fatal("shell has no script")
	}
	js := get(h, m[1])
	if js.Code != 200 || !strings.Contains(js.Header().Get("Content-Type"), "javascript") || !strings.Contains(js.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("bundle script: %d %v", js.Code, js.Header())
	}
}

func TestMissingBundleIsNotFoundNotAFallback(t *testing.T) {
	for name, fsys := range map[string]fs.FS{"empty": fstest.MapFS{}, "assets only": fstest.MapFS{"assets/x.js": {Data: []byte("x")}}} {
		h := New(nil, "").WithAssets(fsys).Routes()
		for _, p := range []string{"/", "/pending"} {
			if rec := get(h, p); rec.Code != 404 {
				t.Errorf("%s %s = %d %q", name, p, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestPWAPublicAssets(t *testing.T) {
	h := New(nil, "").WithAssets(webui.FS()).Routes()
	for _, p := range []string{"/sw.js", "/manifest.webmanifest", "/offline.html", "/icon-192.png", "/icon-512.png"} {
		rec := get(h, p)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
		if p == "/sw.js" || p == "/manifest.webmanifest" {
			if rec.Header().Get("Cache-Control") != "no-cache" {
				t.Errorf("%s must revalidate", p)
			}
		}
		if p == "/sw.js" && (strings.Contains(rec.Body.String(), "__VERSION__") || strings.Contains(rec.Body.String(), "__PRECACHE__")) {
			t.Error("service worker was not versioned by the build")
		}
		if p == "/offline.html" {
			if rec.Header().Get("Content-Security-Policy") != get(h, "/").Header().Get("Content-Security-Policy") {
				t.Error("offline shell must have the same pinned CSP")
			}
			if !strings.Contains(rec.Body.String(), "You are offline") || !strings.Contains(rec.Body.String(), "role=\"status\"") {
				t.Error("offline shell must announce its state")
			}
		}
	}
	if got := get(h, "/manifest.webmanifest").Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("manifest Content-Type = %q", got)
	}
}

// A dot or empty segment under a reserved root is 404, never the mux's redirect to
// the cleaned path: a client that follows it would land on a route it did not ask for.
func TestDotSegmentsUnderReservedRootsAre404NotARedirect(t *testing.T) {
	h := New(func(context.Context) error { return nil }, t.TempDir()).Routes()
	for _, p := range []string{"/api/admin/users/..", "/api/admin/users/.", "/api/admin/../me", "/api/x//y", "/api//x", "/api/./x",
		"/healthz/..", "/readyz/./", "/api/admin/users/../../..", "/api/admin/users/%2e%2e", "/api/admin/users/..%2f"} {
		for _, m := range []string{"GET", "POST"} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(m, p, nil))
			if rec.Code != 404 || rec.Header().Get("Location") != "" {
				t.Errorf("%s %s -> %d Location=%q", m, p, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	if rec := get(h, "/api/"); rec.Code != 404 { // the reserved catch-all, unchanged
		t.Errorf("/api/ -> %d", rec.Code)
	}
}
