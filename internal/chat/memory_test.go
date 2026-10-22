package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

// Private memory scoping (PRD §5.4 MEM-001..003, MEM-006; §5.6). What Antares
// controls is *which profile, key, Hermes session and session key* every call
// carries. These tests prove that against a fake Hermes whose memory is keyed by
// profile. They do NOT prove that real Hermes keeps one profile's memory from
// another's: that is the live-container test in docs/runbooks/memory-scoping.md,
// which has not been run (Phase 0 FAIL; the container may not be touched).

// completeRun finishes a run the way Hermes would, with the fake's answer.
func completeRun(t *testing.T, a *app, p *person, chat, text string) (out string) {
	t.Helper()
	run := p.send(chat, text)
	_, fr := a.hx.runWithInput(t, text)
	s := p.open(run, "")
	recv(t, fr.opens, "upstream stream")
	a.hx.mu.Lock()
	out = fr.output
	a.hx.mu.Unlock()
	b, _ := json.Marshal(map[string]string{"output": out})
	a.hx.push(fr, "run.completed", string(b))
	s.waitFor(t, "run.completed")
	<-s.done
	return out
}

func sessionKeysSeen(a *app, profile string) map[string]bool {
	keys := map[string]bool{}
	for _, r := range a.hx.requests() {
		if r.Method == "POST" && r.Path == "/p/"+profile+"/v1/runs" {
			keys[r.SessionKey] = true
		}
	}
	return keys
}

// MEM-002 / MEM-003: one stable server-derived key per user (not per chat),
// a distinct Hermes session per chat, nothing shared between users.
func TestSessionKeyIsPerUserAndSessionIsPerChat(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	a1, a2, b1 := alice.newChat(), alice.newChat(), bob.newChat()
	completeRun(t, a, alice, a1, "first")
	completeRun(t, a, alice, a2, "second")
	completeRun(t, a, bob, b1, "third")

	ka, kb := sessionKeysSeen(a, alice.profile), sessionKeysSeen(a, bob.profile)
	if len(ka) != 1 || !ka["chatui:user:"+alice.id] {
		t.Errorf("alice's runs carried session keys %v; want exactly the one derived from her account", ka)
	}
	if len(kb) != 1 || !kb["chatui:user:"+bob.id] {
		t.Errorf("bob's runs carried session keys %v", kb)
	}
	for k := range ka {
		if kb[k] {
			t.Errorf("session key %q is shared between users", k)
		}
		if len(k) > 256 || strings.IndexFunc(k, unicode.IsControl) >= 0 {
			t.Errorf("session key %q is over 256 chars or has control characters", k)
		}
	}
	sa1, sa2, sb1 := decodeSession(t, a, alice, a1), decodeSession(t, a, alice, a2), decodeSession(t, a, bob, b1)
	if sa1 == sa2 || sa1 == sb1 || sa2 == sb1 {
		t.Errorf("Hermes sessions are shared: %s %s %s", sa1, sa2, sb1)
	}
	if !strings.Contains(sa1, alice.profile) || !strings.Contains(sb1, bob.profile) {
		t.Errorf("a session was created under the wrong profile: %s %s", sa1, sb1)
	}
}

// §5.6: A's memory recalls for A in a new chat; B never recalls it; a different
// key or session offered by the client changes nothing.
func TestMemoryOfOneUserIsNeverRecalledForAnother(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	ca1, ca2, cb := alice.newChat(), alice.newChat(), bob.newChat()

	completeRun(t, a, alice, ca1, "remember canary-7f3a-for-alice")
	if got := completeRun(t, a, alice, ca2, "recall as alice"); got != "canary-7f3a-for-alice" {
		t.Errorf("alice in a new chat recalls %q", got)
	}
	if got := completeRun(t, a, bob, cb, "recall as bob"); got != "" {
		t.Errorf("bob recalled %q", got)
	}
	// Bob tries to borrow Alice's identity in every client-controllable place: refused, nothing sent.
	before := len(a.hx.requests())
	for _, h := range []map[string]string{
		{"X-Hermes-Session-Key": "chatui:user:" + alice.id},
		{"X-Hermes-Session-Id": decodeSession(t, a, alice, ca1)},
		{"X-Hermes-Profile": alice.profile},
		{"Authorization": "Bearer " + alice.key},
	} {
		h["Idempotency-Key"] = nextIdem()
		if w := bob.do("POST", "/api/chats/"+cb+"/messages", h, map[string]string{"input": "recall"}); w.Code != 400 {
			t.Errorf("bob with %v: %d", h, w.Code)
		}
	}
	if w := bob.do("POST", "/api/chats/"+cb+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "x", "hermes_session_id": decodeSession(t, a, alice, ca1)}); w.Code != 400 {
		t.Errorf("session id in the body: %d", w.Code)
	}
	if n := len(a.hx.requests()); n != before {
		t.Errorf("%d request(s) reached Hermes from refused attempts", n-before)
	}
	for _, r := range a.hx.requests() {
		if strings.HasPrefix(r.Path, "/p/"+bob.profile+"/") && (strings.Contains(r.Body, alice.id) || r.SessionKey == "chatui:user:"+alice.id || r.Auth == "Bearer "+alice.key) {
			t.Errorf("Bob's call carried Alice's identity: %+v", r)
		}
	}
}

// §5.6: invalid per-profile key fails closed; no other credential is tried.
func TestWrongProfileKeyFailsClosedWithoutFallback(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chat := bob.newChat()
	// Hermes now expects a different key for Bob's profile than the one Antares holds.
	a.hx.mu.Lock()
	a.hx.keys[bob.profile] = "rotated-elsewhere-key-0123456789"
	a.hx.mu.Unlock()
	before := len(a.hx.requests())
	run := bob.send(chat, "hello")
	eventually(t, func() bool { return bob.runStatus(run) == "FAILED" }, "the run to fail")
	if w := bob.get("/api/chats/" + chat + "/messages"); w.Code < 400 || strings.Contains(w.Body.String(), `"messages":[`) {
		t.Errorf("history with a rejected key: %d %s", w.Code, w.Body.String())
	}
	for _, r := range a.hx.requests()[before:] {
		if r.Auth != "Bearer "+bob.key {
			t.Errorf("a different credential was tried: %q on %s", r.Auth, r.Path)
		}
		if !strings.HasPrefix(r.Path, "/p/"+bob.profile+"/") {
			t.Errorf("a different route was tried: %s", r.Path)
		}
	}
	_ = alice
}

// §5.6: A and B stream at the same time; neither sees the other's events or text.
func TestSimultaneousStreamsNeverCross(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	ca, cb := alice.newChat(), bob.newChat()
	ra, rb := alice.send(ca, "alice asks"), bob.send(cb, "bob asks")
	_, fa := a.hx.runWithInput(t, "alice asks")
	_, fb := a.hx.runWithInput(t, "bob asks")
	sa, sb := alice.open(ra, ""), bob.open(rb, "")
	recv(t, fa.opens, "alice's upstream")
	recv(t, fb.opens, "bob's upstream")
	for i := 0; i < 20; i++ { // interleaved deltas
		a.hx.push(fa, "assistant.delta", `{"delta":"A`+string(rune('a'+i))+` "}`)
		a.hx.push(fb, "assistant.delta", `{"delta":"B`+string(rune('a'+i))+` "}`)
	}
	a.hx.push(fa, "run.completed", `{}`)
	a.hx.push(fb, "run.completed", `{}`)
	sa.waitFor(t, "run.completed")
	sb.waitFor(t, "run.completed")
	if strings.Contains(sa.text(), "B") || strings.Contains(sb.text(), "A") {
		t.Errorf("text crossed: alice %q bob %q", sa.text(), sb.text())
	}
	if !strings.HasPrefix(sa.text(), "Aa Ab ") || !strings.HasPrefix(sb.text(), "Ba Bb ") {
		t.Errorf("deltas lost or reordered: %q / %q", sa.text(), sb.text())
	}
	for _, e := range sa.events() {
		if strings.Contains(e.Data, rb) || strings.Contains(e.Data, cb) {
			t.Errorf("alice's stream carries bob's ids: %s", e.Data)
		}
	}
	// Bob cannot subscribe to Alice's run, even while it streams.
	if w := bob.get("/api/runs/" + ra + "/events"); w.Code != 404 {
		t.Errorf("bob subscribed to alice's run: %d", w.Code)
	}
}
