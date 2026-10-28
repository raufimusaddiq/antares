package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// runBodyFor is the POST /v1/runs body Hermes received for the run with this input.
func runBodyFor(t *testing.T, a *app, input string) map[string]any {
	t.Helper()
	a.hx.runWithInput(t, input)
	a.hx.mu.Lock()
	defer a.hx.mu.Unlock()
	for _, r := range a.hx.reqs {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/v1/runs") && strings.Contains(r.Body, input) {
			var b map[string]any
			if err := json.Unmarshal([]byte(r.Body), &b); err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("no run body for %q", input)
	return nil
}

// #111, LLM-ACC-012: A's personality reaches A's next run as Hermes' per-run instructions, framed as lower priority
// than the profile's own prompt, and only A's profile receives it; B's runs carry none. Clearing it stops it.
func TestPersonalityAppliesToTheOwnersNextRunOnly(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	if w := alice.do("PUT", "/api/me/personality", nil, map[string]string{"text": "  Answer in Indonesian. Be brief.  "}); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if got := decodeBody[personalityView](t, alice.get("/api/me/personality")); got.Text != "Answer in Indonesian. Be brief." || got.Max != maxPersonality {
		t.Fatalf("read back: %+v", got)
	}
	if got := decodeBody[personalityView](t, bob.get("/api/me/personality")); got.Text != "" {
		t.Fatalf("bob sees %q", got.Text)
	}
	alice.send(alice.newPlainChat(), "alice asks")
	body := runBodyFor(t, a, "alice asks")
	instr, _ := body["instructions"].(string)
	if !strings.HasPrefix(instr, personalityPrefix) || !strings.HasSuffix(instr, "Answer in Indonesian. Be brief.") {
		t.Errorf("alice's run instructions: %q", instr)
	}
	bob.send(bob.newPlainChat(), "bob asks")
	if b := runBodyFor(t, a, "bob asks"); b["instructions"] != nil {
		t.Errorf("bob's run carries instructions: %v", b["instructions"])
	}
	alice.do("PUT", "/api/me/personality", nil, map[string]string{"text": ""})
	alice.send(alice.newPlainChat(), "alice again")
	if b := runBodyFor(t, a, "alice again"); b["instructions"] != nil {
		t.Errorf("cleared personality still sent: %v", b["instructions"])
	}
}

func TestPersonalityInputIsStrict(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	for name, body := range map[string]any{
		"missing":      map[string]any{},
		"too long":     map[string]string{"text": strings.Repeat("x", maxPersonality+1)},
		"not a string": map[string]any{"text": 5},
		"server field": map[string]any{"text": "x", "profile": "u_other"},
	} {
		if w := alice.do("PUT", "/api/me/personality", nil, body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if got := decodeBody[personalityView](t, alice.get("/api/me/personality")); got.Text != "" {
		t.Errorf("a refused write changed it: %q", got.Text)
	}
}
