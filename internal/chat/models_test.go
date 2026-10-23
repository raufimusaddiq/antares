package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

type modelsBody struct {
	Source, ContextWindow string
	Default               *struct{ Provider, Model string }
	ReasoningEfforts      []string
	Providers             []struct {
		Slug      string
		Available bool
		Reason    string
		Models    []struct {
			ID          string
			Available   bool
			Reasoning   *bool
			ServiceTier *bool
			Price       *struct{ Input, Output string }
		}
		Quota struct {
			State   string
			Windows []struct {
				Label       string
				UsedPercent float64
			}
		}
	}
}

func (p *person) putModel(chatID string, body map[string]any) (int, string) {
	w := p.do("PUT", "/api/chats/"+chatID+"/model", nil, body)
	return w.Code, w.Body.String()
}

func (p *person) chatModel(chatID string) *struct{ Provider, Model, ReasoningEffort, ServiceTier string } {
	w := p.get("/api/chats/" + chatID)
	b := decodeBody[struct {
		Model *struct{ Provider, Model, ReasoningEffort, ServiceTier string }
	}](p.a.t, w)
	return b.Model
}

// LLM-001/006/007, CTX-004: the picker is Hermes' inventory, with unreported values null.
func TestModelsAreTheHermesInventory(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	w := u.get("/api/models")
	if w.Code != 200 {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	m := decodeBody[modelsBody](t, w)
	if m.Source != "GET /api/model/options" || m.Default == nil || m.Default.Model != "m-reason" || m.ContextWindow != "unavailable" {
		t.Fatalf("unexpected header: %+v", m)
	}
	alpha, beta := m.Providers[0], m.Providers[1]
	if !alpha.Available || beta.Available || beta.Reason == "" || len(beta.Models) != 0 {
		t.Fatalf("availability not taken from Hermes: %+v", m.Providers)
	}
	if alpha.Models[0].Price == nil || alpha.Models[1].Price != nil {
		t.Fatalf("a price Hermes did not send must be null: %+v", alpha.Models)
	}
	if r := alpha.Models[1].Reasoning; r == nil || *r {
		t.Fatalf("m-plain reasoning should be false")
	}
	if alpha.Quota.State != "AVAILABLE" || alpha.Quota.Windows[0].UsedPercent != 42.5 {
		t.Fatalf("quota: %+v", alpha.Quota)
	}
	if beta.Quota.State != "PROVIDER_USAGE_UNAVAILABLE" || len(beta.Quota.Windows) != 0 {
		t.Fatalf("a provider with no usage must say so, not show 0: %+v", beta.Quota)
	}
	if got := a.hx.requests(); !strings.Contains(got[len(got)-1].Path, "/api/model/options") || !strings.HasPrefix(got[len(got)-1].Path, "/p/"+u.profile+"/") {
		t.Fatalf("inventory must come from the caller's own profile route: %+v", got[len(got)-1])
	}
}

// LLM-002/003/004, LLM-ACC-002: the choice is saved per chat, validated, and sent on the run.
func TestModelChoiceIsPerChatValidatedAndSent(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	c1, c2 := u.newChat(), u.newChat()
	if u.chatModel(c1) != nil {
		t.Fatal("a new chat starts on the profile default")
	}
	good := map[string]any{"provider": "alpha", "model": "m-reason", "reasoningEffort": "high", "serviceTier": "priority"}
	if code, body := u.putModel(c1, good); code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	if m := u.chatModel(c1); m == nil || m.Model != "m-reason" || m.ReasoningEffort != "high" || m.ServiceTier != "priority" {
		t.Fatalf("not saved: %+v", m)
	}
	if u.chatModel(c2) != nil {
		t.Fatal("another chat must not inherit the choice")
	}
	u.send(c1, "hello")
	var body map[string]any
	eventually(t, func() bool {
		for _, r := range a.hx.requests() {
			if r.Method == "POST" && strings.HasSuffix(r.Path, "/v1/runs") {
				return json.Unmarshal([]byte(r.Body), &body) == nil
			}
		}
		return false
	}, "the run request")
	opts, _ := body["model_options"].(map[string]any)
	reasoning, _ := opts["reasoning"].(map[string]any)
	if body["model"] != "m-reason" || body["provider"] != "alpha" || opts["service_tier"] != "priority" || reasoning["effort"] != "high" {
		t.Fatalf("run body does not carry the choice: %v", body)
	}
	if code, _ := u.putModel(c1, map[string]any{"reset": true}); code != 200 || u.chatModel(c1) != nil {
		t.Fatal("reset must return the chat to the default")
	}
}

// LLM-001, LLM-003/004: nothing Hermes does not list as available can be selected.
func TestUnavailableChoicesAreRefused(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	c := u.newChat()
	for name, body := range map[string]map[string]any{
		"unconfigured provider": {"provider": "beta", "model": "x"},
		"unlisted model":        {"provider": "alpha", "model": "nope"},
		"unknown provider":      {"provider": "zzz", "model": "m-reason"},
		"reasoning unsupported": {"provider": "alpha", "model": "m-plain", "reasoningEffort": "high"},
		"effort not accepted":   {"provider": "alpha", "model": "m-reason", "reasoningEffort": "turbo"},
		"tier unsupported":      {"provider": "alpha", "model": "m-plain", "serviceTier": "priority"},
		"tier not accepted":     {"provider": "alpha", "model": "m-reason", "serviceTier": "flex"},
		"no model":              {"provider": "alpha"},
	} {
		if code, out := u.putModel(c, body); code != 422 || !strings.Contains(out, "MODEL_UNAVAILABLE") {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	if code, _ := u.putModel(c, map[string]any{"provider": "alpha", "model": "m-reason", "profile": "other"}); code != 400 {
		t.Errorf("a client-supplied profile must be rejected, got %d", code)
	}
	if u.chatModel(c) != nil {
		t.Fatal("a refused choice must save nothing")
	}
}

// LLM-ACC-001: models come from each caller's own profile; B cannot touch A's chat.
func TestModelsAreScopedToTheCaller(t *testing.T) {
	a := newApp(t)
	ua, ub := a.user("a"), a.user("b")
	a.hx.mu.Lock()
	a.hx.options = map[string]string{ub.profile: `{"model":"b-only","provider":"gamma","providers":[{"slug":"gamma","name":"Gamma","models":["b-only"],"capabilities":{"b-only":{"fast":false,"reasoning":false}}}]}`}
	a.hx.mu.Unlock()
	if m := decodeBody[modelsBody](t, ua.get("/api/models")); m.Default.Model != "m-reason" || len(m.Providers) != 2 {
		t.Fatalf("A saw: %+v", m)
	}
	if m := decodeBody[modelsBody](t, ub.get("/api/models")); m.Default.Model != "b-only" || len(m.Providers) != 1 {
		t.Fatalf("B saw: %+v", m)
	}
	if code, _ := ub.putModel(ub.newChat(), map[string]any{"provider": "alpha", "model": "m-reason"}); code != 422 {
		t.Fatalf("B must not select A's model, got %d", code)
	}
	chatA := ua.newChat()
	if code, _ := ub.putModel(chatA, map[string]any{"provider": "gamma", "model": "b-only"}); code != 404 {
		t.Fatalf("B setting A's chat model must be 404, got %d", code)
	}
	if code, _ := ub.putModel(chatA, map[string]any{"reset": true}); code != 404 {
		t.Fatalf("B resetting A's chat model must be 404, got %d", code)
	}
}

// Default deny: an account without a healthy binding makes no Hermes request.
func TestModelsNeedAHealthyTenant(t *testing.T) {
	a := newApp(t)
	p := a.account("pending", "USER", "PENDING", "UNPROVISIONED")
	before := len(a.hx.requests())
	if w := p.get("/api/models"); w.Code == 200 {
		t.Fatal("an unapproved account must not read the inventory")
	}
	if len(a.hx.requests()) != before {
		t.Fatal("no Hermes request may be made for it")
	}
}

// An upstream without the route answers CAPABILITY_UNAVAILABLE, not an invented list.
func TestModelsUpstreamWithoutRoute(t *testing.T) {
	a := newApp(t)
	u := a.user("a")
	a.hx.mu.Lock()
	a.hx.failNext = map[string]int{"GET /api/model/options": 404}
	a.hx.mu.Unlock()
	w := u.get("/api/models")
	if w.Code != 501 || errCode(t, w) != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
