// Package server is Antares' HTTP surface: health endpoints, the API mux and
// the static React bundle served from disk.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Checker reports whether a dependency is usable right now.
type Checker func(ctx context.Context) error

// Auth is the identity layer the server mounts: its routes and screens, and a
// guard for "/" so only an eligible user reaches the app shell.
type Auth interface {
	Register(mux *http.ServeMux)
	Landing(next http.Handler) http.Handler
}

type Server struct {
	ready     Checker
	staticDir string
	auth      Auth
}

func New(ready Checker, staticDir string) *Server {
	return &Server{ready: ready, staticDir: staticDir}
}

// WithAuth mounts the identity layer. Without it the server serves no login.
func (s *Server) WithAuth(a Auth) *Server {
	s.auth = a
	return s
}

func (s *Server) Routes() http.Handler {
	return reserve(s.routes())
}

// reserve answers 404 JSON for the whole /api tree (any case) and for every
// path under /healthz or /readyz other than the exact probes, so none of them
// can fall through to the SPA shell and look healthy.
func reserve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Path
		lower := strings.ToLower(raw)
		for _, root := range []string{"/api", "/healthz", "/readyz"} {
			// Case-insensitive only to decide whether the path is reserved.
			if lower != root && !strings.HasPrefix(lower, root+"/") {
				continue
			}
			// Let through only what the mux is meant to own, judged on the raw
			// path: the exact probes, and /api/... with a lowercase prefix (the
			// rest, e.g. an opaque id, may be any case).
			if root == "/api" {
				if strings.HasPrefix(raw, "/api/") {
					break
				}
			} else if raw == root || raw == root+"/" {
				break
			}
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	// The slash forms are probes too; without these they fall through to the
	// SPA shell and report healthy with the database down.
	mux.HandleFunc("GET /healthz/{$}", s.healthz)
	mux.HandleFunc("GET /readyz/{$}", s.readyz)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	if s.auth != nil {
		s.auth.Register(mux)
		mux.Handle("GET /{$}", s.auth.Landing(http.HandlerFunc(s.static)))
	}
	mux.HandleFunc("/", s.static)
	return mux
}

// healthz says the process is up. It checks nothing else, by design.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz says the service can do its job: the database answers and is at the
// current schema. The failure reason is logged by the caller's checker, never
// returned, so the endpoint cannot leak configuration.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// static serves the built bundle; unknown non-API paths fall back to
// index.html so client-side routes work. http.Dir rejects path traversal.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fs := http.Dir(s.staticDir)
	p := filepath.ToSlash(filepath.Clean("/" + r.URL.Path))
	if f, err := fs.Open(p); err == nil {
		st, serr := f.Stat()
		f.Close()
		if serr == nil && !st.IsDir() {
			http.FileServer(fs).ServeHTTP(w, r)
			return
		}
	}
	if strings.Contains(filepath.Base(p), ".") { // a missing asset is a 404, not the app shell
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(filepath.Join(s.staticDir, "index.html")); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.staticDir, "index.html"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
