// Command hermes-provisioner is a host-side helper that creates and removes
// per-user Hermes profiles on behalf of Antares. It reaches Hermes only through
// `docker exec` argv arrays and is protected by a bearer token.
package main

import (
	"context"
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
	h         hermes
	token     string
	inference inferenceBootstrap
	disabled  []string   // toolsets written to every profile at provision and rotate
	mu        sync.Mutex // one profile mutation at a time
}

// opCtx returns the context for a whole request. It is detached from the
// client connection and bounded by opBudget, so a disconnecting caller cannot
// cancel an operation midway (e.g. after the key is written).
func opCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), opBudget)
}

// begin takes the mutation lock, then starts the clock: the write deadline is
// set only after the lock is held, so time spent queued cannot expire the
// response while the operation runs. The server-level WriteTimeout is
// therefore unset.
func (s *server) begin(w http.ResponseWriter, r *http.Request) (context.Context, func()) {
	s.mu.Lock()
	ctx, cancel := opCtx(r)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(opBudget + writeSlack))
	return ctx, func() { cancel(); s.mu.Unlock() }
}

func newServerWithPolicy(r Runner, token string, disabled []string) *server {
	return &server{h: hermes{r}, token: token, disabled: disabled, inference: inferenceBootstrap{Key: os.Getenv("PROVISIONER_INFERENCE_API_KEY"), BaseURL: os.Getenv("PROVISIONER_INFERENCE_BASE_URL")}}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /provision", s.auth(s.provision))
	mux.HandleFunc("POST /deprovision", s.auth(s.deprovision))
	mux.HandleFunc("POST /rotate", s.auth(s.rotate))
	mux.HandleFunc("POST /media", s.auth(s.media))
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
	// The revision is reported even when Hermes is unreachable: a deploy check
	// must be able to tell a stale binary from a down container.
	revision, modified := buildRevision()
	body := map[string]any{"status": "ok", "revision": revision, "modified": modified}
	code := http.StatusOK
	if !s.h.reachable(r.Context()) {
		body["status"] = "hermes unreachable"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}

// readRequest decodes {"slug":..., "toolsets":[...]} strictly and validates it. Any other field (profile, key,
// header...) is rejected. toolsets is optional: Antares sends the admin's tenant policy (one of offeredToolsets);
// without it the operator's PROVISIONER_ALLOWED_TOOLSETS applies. It returns the profile's disabled list.
func (s *server) readRequest(w http.ResponseWriter, r *http.Request) (string, []string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req struct {
		Slug     string   `json:"slug"`
		Toolsets []string `json:"toolsets"`
	}
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return "", nil, false
	}
	if err := validateSlug(req.Slug); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return "", nil, false
	}
	disabled := s.disabled
	if req.Toolsets != nil {
		d, err := requestPolicy(req.Toolsets)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return "", nil, false
		}
		disabled = d
	}
	return req.Slug, disabled, true
}

func newKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *server) provision(w http.ResponseWriter, r *http.Request) {
	slug, disabled, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	ctx, done := s.begin(w, r)
	defer done()
	exists, err := s.h.exists(ctx, slug)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !exists {
		if err := s.inference.validate(); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := s.h.create(ctx, slug); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error()) // fail closed: no key written
			return
		}
	}
	// Applied on every provision, existing profile or not, before any key is
	// issued: a profile that cannot be locked down never gets a key.
	if err := s.h.applyPolicy(ctx, slug, disabled); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	key, err := newKey()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key generation failed")
		return
	}
	// Offered for an existing profile too: the script applies it only while the
	// profile holds no variable, so a retry completes a failed bootstrap.
	if err := s.h.writeKey(ctx, "init", slug, key, &s.inference); err != nil {
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
	slug, disabled, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	ctx, done := s.begin(w, r)
	defer done()
	exists, err := s.h.exists(ctx, slug)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !exists {
		writeErr(w, http.StatusNotFound, errNoProfile.Error())
		return
	}
	// Reapplied so provision and rotate leave a profile in the same state.
	if err := s.h.applyPolicy(ctx, slug, disabled); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	key, err := newKey()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key generation failed")
		return
	}
	if err := s.h.writeKey(ctx, "rotate", slug, key, nil); err != nil {
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
	slug, _, ok := s.readRequest(w, r) // a toolsets field is validated, then has nothing to apply to
	if !ok {
		return
	}
	ctx, done := s.begin(w, r)
	defer done()
	if err := s.h.delete(ctx, slug); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"profile": slug, "status": "deprovisioned"})
}

// sweepEvery must exceed the slowest single docker exec (callTimeout) so a slow
// sweep cannot make the next tick fire the moment it ends.
const sweepEvery = 2 * time.Minute

// sweepLoop periodically removes residue directories (see hermes.sweep). It
// takes the same lock as the mutating handlers so it never races a create.
func (s *server) sweepLoop(every time.Duration) {
	for range time.Tick(every) {
		s.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), opBudget)
		removed, err := s.h.sweep(ctx)
		cancel()
		s.mu.Unlock()
		if err != nil {
			log.Printf("sweep: %v", err)
		}
		for _, name := range removed {
			log.Printf("sweep: removed residue directory for %s", name)
		}
	}
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
	disabled, err := policyFromEnv(os.Getenv("PROVISIONER_ALLOWED_TOOLSETS"))
	if err != nil {
		log.Fatal("PROVISIONER_ALLOWED_TOOLSETS: ", err)
	}
	s := newServerWithPolicy(dockerRunner{container}, token, disabled)
	if err := s.inference.validate(); err != nil {
		log.Fatal("PROVISIONER_INFERENCE_API_KEY and PROVISIONER_INFERENCE_BASE_URL must configure the shared inference bootstrap")
	}
	go s.sweepLoop(sweepEvery)
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("hermes-provisioner listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
