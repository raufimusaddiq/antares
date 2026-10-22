// Command hermes-provisioner is a host-side helper that creates and removes
// per-user Hermes profiles on behalf of Antares. It reaches Hermes only through
// `docker exec` argv arrays and is protected by a bearer token.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type server struct {
	h     hermes
	token string
	mu    sync.Mutex // one profile mutation at a time
}

func newServer(r Runner, token string) *server {
	return &server{h: hermes{r}, token: token}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /provision", s.auth(s.provision))
	mux.HandleFunc("POST /deprovision", s.auth(s.deprovision))
	mux.HandleFunc("POST /rotate", s.auth(s.rotate))
	return mux
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
			subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if !s.h.reachable(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "hermes unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readSlug decodes {"slug":...} strictly and validates it. Any extra field
// (profile, key, header...) is rejected.
func readSlug(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req struct {
		Slug string `json:"slug"`
	}
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	if err := validateSlug(req.Slug); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return req.Slug, true
}

func newKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *server) provision(w http.ResponseWriter, r *http.Request) {
	slug, ok := readSlug(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := r.Context()
	exists, err := s.h.exists(ctx, slug)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !exists {
		if err := s.h.create(ctx, slug); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error()) // fail closed: no key written
			return
		}
	}
	key, err := newKey()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key generation failed")
		return
	}
	if err := s.h.writeKey(ctx, "init", slug, key); err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errKeyExists) {
			code = http.StatusConflict
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"profile": slug, "key": key, "created_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *server) rotate(w http.ResponseWriter, r *http.Request) {
	slug, ok := readSlug(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := r.Context()
	exists, err := s.h.exists(ctx, slug)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !exists {
		writeErr(w, http.StatusNotFound, errNoProfile.Error())
		return
	}
	key, err := newKey()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key generation failed")
		return
	}
	if err := s.h.writeKey(ctx, "rotate", slug, key); err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errNoKey) {
			code = http.StatusConflict
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"profile": slug, "key": key, "created_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *server) deprovision(w http.ResponseWriter, r *http.Request) {
	slug, ok := readSlug(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.h.delete(r.Context(), slug); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"profile": slug, "status": "deprovisioned"})
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

func main() {
	token := os.Getenv("PROVISIONER_TOKEN")
	if len(token) < 32 {
		log.Fatal("PROVISIONER_TOKEN must be set (at least 32 characters)")
	}
	container := os.Getenv("HERMES_CONTAINER")
	if container == "" {
		container = "hermes"
	}
	addr := os.Getenv("PROVISIONER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8750"
	}
	s := newServer(dockerRunner{container}, token)
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 120 * time.Second}
	log.Printf("hermes-provisioner listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
