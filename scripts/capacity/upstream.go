//go:build capacity

package main

// The fabricated world around the scratch Antares: a fake Google OIDC provider,
// a fake provisioning helper and a fake Hermes, in ONE separate process so that
// none of their memory is counted against Antares. Adapted from
// internal/smoke/stack_test.go. A run holds for a configurable time, streaming
// the reply in chunks, then completes. All values are fabricated.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

const (
	provToken    = "fabricated-provisioner-token-0123456789abcdef"
	clientID     = "scratch-client-id"
	clientSecret = "fabricated-client-secret-0123456789"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type up struct {
	mu         sync.Mutex
	idpURL     string
	key        *rsa.PrivateKey
	grants     map[string]grant
	keys       map[string]string
	sessions   map[string]string
	msgs       map[string][]map[string]any
	runs       map[string]*run
	n          int
	holdMS     int
	replyBytes int
	allowed    map[string]bool
}

type grant struct{ sub, email, nonce, challenge, aud string }
type run struct {
	profile, session, input string
	started                 time.Time
	hold                    time.Duration
	recorded                bool
}

func runUpstream(addr, idpURL string) error {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	u := &up{idpURL: idpURL, key: k, grants: map[string]grant{}, keys: map[string]string{}, sessions: map[string]string{},
		msgs: map[string][]map[string]any{}, runs: map[string]*run{}, allowed: map[string]bool{}, holdMS: 2000, replyBytes: 2000}
	for _, n := range hermes.DefaultAllowedToolsets {
		u.allowed[n] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/idp/", u.idp)
	mux.HandleFunc("/prov/", u.prov)
	mux.HandleFunc("/p/", u.hermes)
	// the default profile's route: a tenant key opens nothing there (credential_isolation)
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", 401) })
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", 401) })
	mux.HandleFunc("/_ctl", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if v, err := strconv.Atoi(r.URL.Query().Get("hold_ms")); err == nil {
			u.holdMS = v
		}
		if v, err := strconv.Atoi(r.URL.Query().Get("reply_bytes")); err == nil {
			u.replyBytes = v
		}
		fmt.Fprintf(w, "hold_ms=%d reply_bytes=%d\n", u.holdMS, u.replyBytes)
	})
	// the provisioner URL may not carry a path, so it gets its own port: addr+1
	host, port, _ := strings.Cut(addr, ":")
	pp, _ := strconv.Atoi(port)
	go func() { log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", host, pp+1), http.HandlerFunc(u.prov))) }()
	log.Printf("upstream on %s (provisioner on %d)", addr, pp+1)
	return (&http.Server{Addr: addr, Handler: mux}).ListenAndServe()
}

func (u *up) idp(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, "/idp") {
	case "/jwks":
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": b64(u.key.N.Bytes()), "e": b64(big.NewInt(int64(u.key.E)).Bytes())}}})
	case "/_grant": // the driver stands in for the browser's trip to the provider
		q := r.URL.Query()
		u.mu.Lock()
		u.grants[q.Get("code")] = grant{q.Get("sub"), q.Get("sub") + "@scratch.invalid", q.Get("nonce"), q.Get("challenge"), q.Get("aud")}
		u.mu.Unlock()
	case "/token":
		_ = r.ParseForm()
		u.mu.Lock()
		g, ok := u.grants[r.Form.Get("code")]
		delete(u.grants, r.Form.Get("code"))
		u.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || b64(sum[:]) != g.challenge || r.Form.Get("client_secret") != clientSecret {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		now := time.Now().Unix()
		hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		pl, _ := json.Marshal(map[string]any{"iss": u.idpURL, "aud": g.aud, "sub": g.sub, "email": g.email, "email_verified": true,
			"name": g.sub, "nonce": g.nonce, "iat": now, "exp": now + 600})
		signing := b64(hdr) + "." + b64(pl)
		d := sha256.Sum256([]byte(signing))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, u.key, crypto.SHA256, d[:])
		json.NewEncoder(w).Encode(map[string]string{"id_token": signing + "." + b64(sig), "access_token": "unused"})
	default:
		http.NotFound(w, r)
	}
}

func (u *up) prov(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+provToken {
		http.Error(w, "no", 401)
		return
	}
	var in struct{ Slug string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	u.mu.Lock()
	_, exists := u.keys[in.Slug]
	if strings.HasSuffix(r.URL.Path, "/provision") && exists {
		u.mu.Unlock()
		http.Error(w, "exists", 409)
		return
	}
	key := hex.EncodeToString(raw)
	u.keys[in.Slug] = key
	u.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]string{"profile": in.Slug, "key": key, "created_at": "2026-10-23T00:00:00Z"})
}

// replyText is deterministic filler of the stated size.
func replyText(n int) string {
	const w = "The quick brown fox jumps over the lazy dog while the assistant explains the result in plain words. "
	return strings.Repeat(w, n/len(w)+1)[:n]
}

func (u *up) hermes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	profile, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	u.mu.Lock()
	key, known := u.keys[profile]
	hold, rb := time.Duration(u.holdMS)*time.Millisecond, u.replyBytes
	u.mu.Unlock()
	if !known {
		http.Error(w, "unknown profile", 404)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+key {
		http.Error(w, "unauthorized", 401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch {
	case path == "/v1/capabilities":
		io.WriteString(w, `{"object":"hermes.api_server.capabilities","auth":{"required":true},"features":{"memory_write_api":false,"admin_config_rw":false}}`)
	case path == "/v1/models":
		io.WriteString(w, `{"object":"list","data":[]}`)
	case path == "/v1/toolsets":
		var data []map[string]any
		for _, n := range hermes.KnownToolsets {
			data = append(data, map[string]any{"name": n, "enabled": u.allowed[n]})
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	case r.Method == "GET" && path == "/api/sessions":
		io.WriteString(w, `{"object":"list","data":[],"limit":1,"offset":0,"has_more":false}`)
	case r.Method == "POST" && path == "/api/sessions":
		u.mu.Lock()
		u.n++
		id := fmt.Sprintf("sess_%d", u.n)
		u.sessions[id] = profile
		u.mu.Unlock()
		fmt.Fprintf(w, `{"id":%q}`, id)
	case r.Method == "GET" && strings.HasPrefix(path, "/api/sessions/") && strings.HasSuffix(path, "/messages"):
		sid := strings.TrimSuffix(strings.TrimPrefix(path, "/api/sessions/"), "/messages")
		u.mu.Lock()
		owner, m := u.sessions[sid], append([]map[string]any(nil), u.msgs[sid]...)
		u.mu.Unlock()
		if owner != profile {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": m})
	case r.Method == "POST" && path == "/v1/runs":
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		u.mu.Lock()
		u.n++
		id := fmt.Sprintf("run_%d", u.n)
		u.runs[id] = &run{profile: profile, session: in["session_id"], input: in["input"], started: time.Now(), hold: hold}
		u.mu.Unlock()
		fmt.Fprintf(w, `{"run_id":%q,"status":"started"}`, id)
	case strings.HasPrefix(path, "/v1/runs/"):
		parts := strings.Split(strings.TrimPrefix(path, "/v1/runs/"), "/")
		u.mu.Lock()
		ru := u.runs[parts[0]]
		u.mu.Unlock()
		if ru == nil || ru.profile != profile {
			http.NotFound(w, r)
			return
		}
		out := replyText(rb)
		switch {
		case len(parts) == 1:
			st := "running"
			if time.Since(ru.started) >= ru.hold {
				st = "completed"
				u.record(ru, out)
			}
			fmt.Fprintf(w, `{"run_id":%q,"status":%q,"output":%q}`, parts[0], st, out)
		case len(parts) == 2 && parts[1] == "events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fl := w.(http.Flusher)
			fl.Flush()
			const chunks = 8
			step := ru.hold / chunks
			for i := 0; i < chunks; i++ {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(step):
				}
				lo, hi := i*len(out)/chunks, (i+1)*len(out)/chunks
				b, _ := json.Marshal(map[string]string{"delta": out[lo:hi]})
				fmt.Fprintf(w, "event: assistant.delta\ndata: %s\n\n", b)
				fl.Flush()
			}
			u.record(ru, out)
			b, _ := json.Marshal(map[string]string{"output": out})
			fmt.Fprintf(w, "event: run.completed\ndata: %s\n\n", b)
			fl.Flush()
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (u *up) record(ru *run, reply string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if ru.recorded {
		return
	}
	ru.recorded = true
	u.msgs[ru.session] = append(u.msgs[ru.session], map[string]any{"role": "user", "content": ru.input}, map[string]any{"role": "assistant", "content": reply})
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
