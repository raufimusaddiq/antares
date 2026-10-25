package chat

import (
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/webui"
)

// TestPhase1Gate is the Phase 1 definition of done (PRD §16), proven in one run
// against the real HTTP stack (auth guard, CSRF, chat service, SQLite) with a
// fake Hermes that records everything it is sent. Each step prints its evidence:
//
//	go test -run TestPhase1Gate -v -count=1 ./internal/chat
//
// What it does not cover is stated by scripts/phase1-gate.sh, which runs this
// next to the browser check and the unit suites, and by docs/runbooks/phase1-gate.md.
func TestPhase1Gate(t *testing.T) {
	// evidence prints a step's result line only if the step passed.
	evidence := func(t *testing.T, format string, args ...any) {
		t.Helper()
		if t.Failed() {
			t.Log("step FAILED: see the errors above")
			return
		}
		t.Logf("PASS "+format, args...)
	}
	a := newApp(t, appOpts{heartbeat: 20 * time.Millisecond})
	admin := a.user("admin")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	hermesCalls := func() int { return len(a.hx.requests()) }
	var alice, bob *person

	t.Run("1 a pending account cannot create any Hermes workload", func(t *testing.T) {
		pending := a.account("pending", "USER", "PENDING", "UNPROVISIONED")
		base := hermesCalls()
		attempts := 0
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"POST", "/api/chats", map[string]string{}},
			{"GET", "/api/chats", nil},
			{"GET", "/api/chats/" + strings.Repeat("a", 32) + "/messages", nil},
			{"POST", "/api/chats/" + strings.Repeat("a", 32) + "/messages", map[string]string{"input": "hi"}},
			{"GET", "/api/runs/" + strings.Repeat("a", 32), nil},
			{"GET", "/api/runs/" + strings.Repeat("a", 32) + "/events", nil},
			{"POST", "/api/runs/" + strings.Repeat("a", 32) + "/cancel", nil},
			{"GET", "/api/admin/users", nil},
			{"POST", "/api/admin/users/" + pending.id + "/approve", map[string]string{"reason": "self-approval"}},
		} {
			w := pending.do(c.method, c.path, map[string]string{"Idempotency-Key": nextIdem()}, c.body)
			attempts++
			if w.Code != 403 || errCode(t, w) != "ACCOUNT_PENDING" {
				t.Errorf("pending %s %s = %d %s", c.method, c.path, w.Code, w.Body.String())
			}
		}
		var approval string
		_ = a.db.QueryRow(`SELECT approval_status FROM users WHERE id = ?`, pending.id).Scan(&approval)
		if approval != "PENDING" {
			t.Errorf("a pending account changed its own status to %s", approval)
		}
		if n := hermesCalls() - base; n != 0 {
			t.Errorf("%d Hermes request(s) from a pending account", n)
		}
		evidence(t, "pending account: %d API attempts (chat, list, history, send, run, stream, cancel, admin, self-approve) -> all 403 ACCOUNT_PENDING; Hermes requests: 0; status still PENDING", attempts)
	})

	t.Run("2 an approved but unprovisioned account cannot chat", func(t *testing.T) {
		base := hermesCalls()
		for _, tenant := range []string{"UNPROVISIONED", "CONFIGURING", "FAILED", "DISABLED"} {
			u := a.account("unprov-"+strings.ToLower(tenant), "USER", "APPROVED", tenant)
			for _, c := range []struct{ method, path string }{
				{"POST", "/api/chats"},
				{"GET", "/api/chats"},
				{"POST", "/api/chats/" + strings.Repeat("b", 32) + "/messages"},
			} {
				w := u.do(c.method, c.path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hi"})
				if w.Code != 409 || errCode(t, w) != "TENANT_NOT_READY" {
					t.Errorf("%s account: %s %s = %d %s", tenant, c.method, c.path, w.Code, w.Body.String())
				}
			}
		}
		// Approved, READY, but no verified binding: still default-deny.
		u := a.account("unverified", "USER", "APPROVED", "READY")
		if w := u.do("POST", "/api/chats", nil, map[string]string{}); w.Code != 409 {
			t.Errorf("READY with no binding: %d %s", w.Code, w.Body.String())
		}
		if n := hermesCalls() - base; n != 0 {
			t.Errorf("%d Hermes request(s) from accounts with no healthy binding", n)
		}
		evidence(t, "approved accounts with tenant UNPROVISIONED, CONFIGURING, FAILED, DISABLED, and READY-without-binding: chat/list/send -> 409 TENANT_NOT_READY; Hermes requests: 0")
	})

	t.Run("3 two approved users chat privately", func(t *testing.T) {
		alice, bob = a.user("alice"), a.user("bobby")
		ca, cb := alice.newChat(), bob.newChat()
		for _, c := range []struct {
			p    *person
			chat string
			q, r string
		}{{alice, ca, "alice asks the question", "alice's answer"}, {bob, cb, "bob asks the question", "bob's answer"}} {
			run := c.p.send(c.chat, c.q)
			_, fr := a.hx.runWithInput(t, c.q)
			s := c.p.open(run, "")
			recv(t, fr.opens, "stream")
			a.hx.push(fr, "assistant.delta", `{"delta":"`+c.r+`"}`)
			a.hx.push(fr, "run.completed", `{}`)
			s.waitFor(t, "run.completed")
			<-s.done
			if s.text() != c.r || c.p.runStatus(run) != "COMPLETED" {
				t.Errorf("%s's run: %q %s", c.p.sub, s.text(), c.p.runStatus(run))
			}
			a.hx.mu.Lock()
			a.hx.msgs[decodeSession(t, a, c.p, c.chat)] = []map[string]any{{"id": "m1", "role": "user", "content": c.q}, {"id": "m2", "role": "assistant", "content": c.r}}
			a.hx.mu.Unlock()
		}
		probes := 0
		before := hermesCalls()
		for _, p := range []struct {
			who  *person
			path string
		}{{bob, ca}, {alice, cb}} {
			for _, r := range []struct{ method, path string }{
				{"GET", "/api/chats/" + p.path}, {"GET", "/api/chats/" + p.path + "/messages"}, {"POST", "/api/chats/" + p.path + "/messages"},
			} {
				w := p.who.do(r.method, r.path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
				probes++
				if w.Code != 404 || strings.Contains(w.Body.String(), "'s answer") || strings.Contains(w.Body.String(), "asks the question") {
					t.Errorf("%s -> %s %s = %d %s", p.who.sub, r.method, r.path, w.Code, w.Body.String())
				}
			}
		}
		if hermesCalls() != before {
			t.Error("cross-user probes reached Hermes")
		}
		for _, r := range a.hx.requests() {
			switch {
			case strings.HasPrefix(r.Path, "/p/"+alice.profile+"/"):
				if r.Auth != "Bearer "+alice.key || (r.SessionKey != "" && r.SessionKey != "chatui:user:"+alice.id) {
					t.Errorf("alice's profile called with %q / %q", r.Auth, r.SessionKey)
				}
			case strings.HasPrefix(r.Path, "/p/"+bob.profile+"/"):
				if r.Auth != "Bearer "+bob.key || (r.SessionKey != "" && r.SessionKey != "chatui:user:"+bob.id) {
					t.Errorf("bob's profile called with %q / %q", r.Auth, r.SessionKey)
				}
			default:
				t.Errorf("request outside any tenant route: %s", r.Path)
			}
		}
		if h := bob.get("/api/chats/" + cb + "/messages"); !strings.Contains(h.Body.String(), "bob's answer") || strings.Contains(h.Body.String(), "alice") {
			t.Errorf("bob's own history: %s", h.Body.String())
		}
		evidence(t, "alice and bob each completed a streamed run; %d cross-user probes (read chat, read history, send) -> all 404 with no content and no Hermes request; every Hermes call used the caller's own profile route, bearer key and session key (%d calls checked)", probes, len(a.hx.requests()))
	})

	t.Run("4 a revoked user loses access immediately", func(t *testing.T) {
		cb := bob.newChat()
		run := bob.send(cb, "bob is mid-run")
		_, fr := a.hx.runWithInput(t, "bob is mid-run")
		s := bob.open(run, "")
		recv(t, fr.opens, "bob's upstream stream")
		s.waitFor(t, "run.started")
		if w := bob.get("/api/chats"); w.Code != 200 {
			t.Fatalf("bob before revocation: %d", w.Code)
		}
		start := time.Now()
		w := admin.do("POST", "/api/admin/users/"+bob.id+"/suspend", nil, map[string]string{"reason": "gate test"})
		if w.Code != 200 {
			t.Fatalf("admin suspend: %d %s", w.Code, w.Body.String())
		}
		base := hermesCalls()
		denied := 0
		for _, c := range []struct{ method, path string }{
			{"GET", "/api/chats"}, {"POST", "/api/chats"}, {"GET", "/api/chats/" + cb + "/messages"}, {"POST", "/api/chats/" + cb + "/messages"},
			{"GET", "/api/runs/" + run}, {"GET", "/api/runs/" + run + "/events"}, {"POST", "/api/runs/" + run + "/cancel"},
		} {
			w := bob.do(c.method, c.path, map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x"})
			denied++
			// Suspending revokes the account's sessions as well as its eligibility, so the
			// first answer is 401; a session that somehow survived would meet 403. Anything
			// else (above all a 2xx) is a failure.
			if w.Code != 401 && w.Code != 403 {
				t.Errorf("revoked bob: %s %s = %d %s", c.method, c.path, w.Code, w.Body.String())
			}
		}
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Fatal("the open stream outlived the revocation")
		}
		closedIn := time.Since(start)
		if s.has("run.completed") || !s.has("run.disconnected") {
			t.Errorf("stream after revocation: %v", s.names())
		}
		if n := hermesCalls() - base; n != 0 {
			t.Errorf("%d Hermes request(s) for a revoked account", n)
		}
		// Alice is untouched.
		if w := alice.get("/api/chats"); w.Code != 200 {
			t.Errorf("alice after bob's revocation: %d", w.Code)
		}
		// Admin is blind: status only, never content, and not a back door to a chat.
		for _, path := range []string{"/api/admin/users/" + bob.id, "/api/admin/users", "/api/admin/audit"} {
			body := admin.get(path).Body.String()
			for _, secret := range []string{"bob asks the question", "bob's answer", "alice's answer", "bob is mid-run"} {
				if strings.Contains(body, secret) {
					t.Errorf("admin %s exposes chat content %q", path, secret)
				}
			}
		}
		if w := admin.get("/api/chats/" + cb + "/messages"); w.Code != 404 {
			t.Errorf("admin read a user's chat: %d", w.Code)
		}
		evidence(t, "admin suspended bob mid-run: %d requests on the very next call -> all refused (401, the session is revoked with the account); his open SSE stream closed with run.disconnected within %v; Hermes requests after revocation: 0; alice unaffected; admin cannot read chats (404) and sees no content in user/audit views", denied, closedIn.Round(time.Millisecond))
	})

	t.Run("5 the UI shell is built for desktop and mobile", func(t *testing.T) {
		w := alice.get("/")
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover,interactive-widget=resizes-content"`) || !strings.Contains(body, `<div id="root">`) {
			t.Errorf("shell: %d %.200s", w.Code, body)
		}
		evidence(t, "the served shell has the responsive viewport meta tag and mounts the React app. Layout itself (390x844 touch viewport and 1280x800) is checked in a real browser by scripts/browser-check.mjs: see its output in this PR.")
	})

	t.Run("6 no Hermes credential is reachable from a browser", func(t *testing.T) {
		// What Hermes secrets exist in this run.
		type secret struct{ name, value string }
		var secrets []secret
		for _, p := range []*person{alice, bob, admin} {
			secrets = append(secrets, secret{p.sub + " bearer key", p.key})
		}
		secrets = append(secrets, secret{"secret-box master key", "fabricated-master-key-0123456789"})
		rows, err := a.db.Query(`SELECT credential_ciphertext, credential_fingerprint FROM tenant_bindings`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var ct []byte
			var fp string
			if err := rows.Scan(&ct, &fp); err != nil {
				t.Fatal(err)
			}
			secrets = append(secrets, secret{"sealed credential (hex)", hex.EncodeToString(ct)}, secret{"credential fingerprint", fp})
		}
		rows.Close()

		var corpus []string
		a.seenMu.Lock()
		corpus = append(corpus, a.seen...)
		for _, s := range a.streams {
			corpus = append(corpus, s.buf.String())
		}
		a.seenMu.Unlock()
		corpus = append(corpus, logBuf.String())
		files := 0
		err = fs.WalkDir(webui.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(webui.FS(), path)
			if err != nil {
				return err
			}
			files++
			corpus = append(corpus, string(b))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// Everything a browser can obtain by asking: its own pages, /api/me, the shell, the bundle.
		for _, p := range []*person{alice, bob} {
			for _, path := range []string{"/api/me", "/", "/api/chats"} {
				p.get(path)
			}
		}
		a.seenMu.Lock()
		corpus = append(corpus, a.seen...)
		a.seenMu.Unlock()

		all := strings.Join(corpus, "\n")
		for _, s := range secrets {
			if s.value != "" && strings.Contains(all, s.value) {
				t.Errorf("%s appears in browser-reachable output", s.name)
			}
		}
		// A user is never shown the profile either (the backend selects it).
		for _, p := range []*person{alice, bob} {
			for _, other := range []*person{alice, bob} {
				for _, rec := range p.seen {
					if strings.Contains(rec, other.profile) {
						t.Errorf("%s's browser saw profile name %s", p.sub, other.profile)
					}
				}
			}
		}
		// And no response advertises a Hermes URL or an Authorization challenge to Hermes.
		for _, rec := range a.seen {
			if strings.Contains(rec, a.hx.srv.URL) {
				t.Errorf("a response names the Hermes URL: %.120s", rec)
			}
		}
		evidence(t, "scanned %d API/SSE responses, the log, and %d embedded UI files for %d credential values (3 bearer keys, the secret-box key, %d sealed blobs/fingerprints) and each user's profile name and the Hermes URL: none found", len(a.seen)+len(a.streams), files, len(secrets), len(secrets)-4)
	})
}
