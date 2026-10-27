// Package server is Antares' HTTP surface: health endpoints, the API mux and
// the static React bundle served from disk.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/raufimusaddiq/antares/internal/obs"
)

// Checker reports whether a dependency is usable right now.
type Checker func(ctx context.Context) error

// Auth is the identity layer the server mounts: its /api routes.
type Auth interface {
	Register(mux *http.ServeMux)
}

type Server struct {
	ready  Checker
	assets fs.FS
	auth   Auth
	gz     gzipCache
}

// New serves the bundle from staticDir (a development override). Production
// passes WithAssets(webui.FS()) instead and needs no directory on disk.
func New(ready Checker, staticDir string) *Server {
	return &Server{ready: ready, assets: os.DirFS(staticDir)}
}

// WithAssets serves the SPA from fsys, e.g. the embedded bundle.
func (s *Server) WithAssets(fsys fs.FS) *Server {
	s.assets = fsys
	return s
}

// WithAuth mounts the identity layer. Without it the server serves no login.
func (s *Server) WithAuth(a Auth) *Server {
	s.auth = a
	return s
}

func (s *Server) Routes() http.Handler {
	mux := s.routes()
	return accessLog(mux, reserve(mux))
}

// accessLog writes one line per /api request with the PRD §13.2 field set (issue #114): request id, method, route
// pattern (never the raw path, which carries ids and query text), status, latency, bytes, the internal user id, and the
// error code of a refusal. No header, body, cookie or query value is logged; the line also passes through the
// redacting log writer. 401 and 403 answers are counted as denials.
func accessLog(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &obs.Request{ID: newID()}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		began := time.Now()
		next.ServeHTTP(sw, r.WithContext(obs.With(r.Context(), rec)))
		_, route := mux.Handler(r)
		if route == "" {
			route = "(none)"
		}
		user, _ := rec.Fields()
		if user == "" {
			user = "-"
		}
		category := "-"
		if sw.status >= 400 {
			category = errorCode(sw.head.Bytes())
		}
		if sw.status == http.StatusUnauthorized || sw.status == http.StatusForbidden {
			obs.Denied()
		}
		log.Printf("http req=%s method=%s route=%q status=%d ms=%d bytes=%d user=%s error=%s",
			rec.ID, r.Method, route, sw.status, time.Since(began).Milliseconds(), sw.bytes, user, category)
	})
}

// statusWriter keeps the status, the byte count and the first bytes of an error body (for its code). It passes
// Flush through, so streams stay streams.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	head   bytes.Buffer
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status >= 400 && s.head.Len() < 512 {
		s.head.Write(b[:min(len(b), 512-s.head.Len())])
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

var codeRE = regexp.MustCompile(`"code"\s*:\s*"([A-Z_]{2,40})"`)

func errorCode(body []byte) string {
	if m := codeRE.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return "-"
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
			// The mux would redirect a path with dot or empty segments to its
			// cleaned form, so a client that follows redirects lands on a route it did
			// not ask for. Under a reserved root such a path is simply not found.
			if uncleanSegments(raw) {
				writeErr(w, http.StatusNotFound, "not found")
				return
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

// uncleanSegments reports a "." or ".." segment, or an empty segment other than
// a single trailing slash. The path is already percent-decoded, so %2e%2e counts.
func uncleanSegments(p string) bool {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, seg := range segs {
		switch {
		case seg == "." || seg == "..":
			return true
		case seg == "" && i != len(segs)-1:
			return true
		}
	}
	return false
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
// index.html so client-side routes work. fs.FS rejects path traversal.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if st, err := fs.Stat(s.assets, p); err == nil && !st.IsDir() {
		s.serveFile(w, r, p)
		return
	}
	if strings.Contains(path.Base(p), ".") { // a missing asset is a 404, not the app shell
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(s.assets, "index.html"); err != nil {
		http.NotFound(w, r) // no bundle: say so, never substitute another page
		return
	}
	s.serveFile(w, r, "index.html")
}

// openUIStyleHash allows exactly one inline stylesheet: the @keyframes rule that
// @openuidev/react-lang@1.0.1 injects into <head> when its Renderer mounts (a
// spinner animation we never show). It is a hash of that fixed text, not
// 'unsafe-inline': any other inline style is still refused. web/src/openui/csp.test.ts
// fails if the pinned library's text stops matching this value.
const openUIStyleHash = "'sha256-vai9tBS1dbqDlLt2vYYQwJl16j1hMfOCO5KhCK8LRBg='"

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := s.assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	rs, ok := f.(io.ReadSeeker)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if name == "index.html" || name == "offline.html" {
		// Both shells allow only our own bundle and refuse framing.
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' "+openUIStyleHash+"; connect-src 'self'; img-src 'self' blob:; media-src blob:; font-src 'self'; frame-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		if name == "index.html" {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Cache-Control", "public, max-age=0, must-revalidate")
		}
	} else if name == "sw.js" || name == "manifest.webmanifest" {
		h.Set("Cache-Control", "no-cache")
		if name == "manifest.webmanifest" {
			h.Set("Content-Type", "application/manifest+json")
		}
	} else if strings.HasPrefix(name, "assets/") { // content-hashed by the build
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if canGzip(name, st.Size()) {
		// The representation depends on Accept-Encoding, so say so on identity responses too.
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			if gz := s.gz.get(s.assets, name, st.Size(), st.ModTime()); gz != nil {
				if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
					h.Set("Content-Type", ct)
				}
				h.Set("Content-Encoding", "gzip")
				// net/http leaves Content-Length unset when Content-Encoding is present.
				h.Set("Content-Length", strconv.Itoa(len(gz)))
				http.ServeContent(w, r, name, st.ModTime(), bytes.NewReader(gz))
				return
			}
		}
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
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
