package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// newModelsApp is an app whose fake Hermes advertises model_options the way the deployed one does:
// the real captured /v1/capabilities (docs/hermes/fixtures, shared with capability discovery).
func newModelsApp(t *testing.T) *app {
	a := newApp(t)
	a.hx.mu.Lock()
	a.hx.canned["/v1/capabilities"] = readFixtureNoT("../../docs/hermes/fixtures/capabilities.body.json")
	a.hx.mu.Unlock()
	return a
}

type modelsBody struct {
	Source      string
	Default     *struct{ Provider, Model string }
	Unsupported map[string]string
	Providers   []struct {
		Slug      string
		Available bool
		Reason    string
		Models    []modelRow
	}
}

type modelRow struct {
	ID               string
	Available        bool
	Hidden           bool
	Reasoning        *bool
	ReasoningEfforts []string
	Modalities       *struct{ Input, Output []string }
	ContextWindow    *float64
	Price            *struct {
		Source, Currency, Unit string
		Free                   bool
		Input, Output          *float64
	}
}

func (m modelsBody) find(id string) (modelRow, bool) {
	for _, p := range m.Providers {
		for _, r := range p.Models {
			if r.ID == id {
				return r, true
			}
		}
	}
	return modelRow{}, false
}

func (p *person) putModel(chatID string, body map[string]any) (int, string) {
	w := p.do("PUT", "/api/chats/"+chatID+"/model", nil, body)
	return w.Code, w.Body.String()
}

func (p *person) putSettings(chatID string, body map[string]any) (int, string) {
	w := p.do("PUT", "/api/chats/"+chatID+"/settings", nil, body)
	return w.Code, w.Body.String()
}

func (p *person) chatModel(chatID string) *struct{ Provider, Model, ReasoningEffort, ServiceTier string } {
	w := p.get("/api/chats/" + chatID)
	b := decodeBody[struct {
		Model *struct{ Provider, Model, ReasoningEffort, ServiceTier string }
	}](p.a.t, w)
	return b.Model
}

func (p *person) models() modelsBody {
	p.a.t.Helper()
	w := p.get("/api/models")
	if w.Code != 200 {
		p.a.t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[modelsBody](p.a.t, w)
}

// Models that Kenari lists and Hermes' own inventory (setDefaultModel) does not.
const (
	kenariOnly      = "glm-5-3-flash"              // Kenari: reasoning, levels low/high/max
	kenariSixLevels = "claude-fable-5"             // Kenari: levels low..max
	kenariNoLevels  = "gemma-4-31b-it"             // Kenari: reasoning true, no levels published
	kenariImageOnly = "agnes-image-2-0-flash:free" // Kenari: images endpoint, not a chat model
)

// LLM-001/006, LLM-ACC-004/006/013: the picker is Kenari's catalogue, not Hermes' inventory.
func TestModelsAreTheKenariCatalogue(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	m := u.models()
	if m.Source != "kenari" || m.Default == nil || m.Default.Model != visionModel || m.Default.Provider != "custom" {
		t.Fatalf("unexpected header: %+v", m)
	}
	if len(m.Providers) != 1 || m.Providers[0].Slug != "custom" || !m.Providers[0].Available {
		t.Fatalf("models run through the profile's own provider: %+v", m.Providers)
	}
	glm, ok := m.find(kenariOnly)
	if !ok || !glm.Available {
		t.Fatal("a Kenari model that Hermes' inventory does not list must still be offered")
	}
	for _, id := range []string{kenariImageOnly, "kling-v3", "whisper-large-v3-turbo", "gemini-3-1-flash-tts"} {
		if _, ok := m.find(id); ok {
			t.Errorf("%s is not a chat model and must not be offered", id)
		}
	}
	ds, _ := m.find(visionModel)
	if ds.ContextWindow == nil || *ds.ContextWindow != 1000000 || ds.Modalities == nil || !slices.Contains(ds.Modalities.Input, "image") {
		t.Fatalf("metadata not taken from Kenari: %+v", ds)
	}
	if ds.Price == nil || ds.Price.Source != "kenari" || ds.Price.Currency != "IDR" || ds.Price.Input == nil || *ds.Price.Input != 2750000000 {
		t.Fatalf("price: %+v", ds.Price)
	}
	// Kenari's "none" is not a level Hermes accepts, so it is not offered.
	if !slices.Equal(ds.ReasoningEfforts, []string{"low", "high", "max"}) {
		t.Fatalf("efforts must be Kenari's levels that Hermes accepts: %v", ds.ReasoningEfforts)
	}
	if gm, _ := m.find(kenariNoLevels); gm.Reasoning == nil || !*gm.Reasoning || len(gm.ReasoningEfforts) != 0 {
		t.Fatalf("reasoning with no published levels offers no effort: %+v", gm)
	}
	free, _ := m.find(textOnlyModel)
	if free.Price == nil || !free.Price.Free || free.Price.Input != nil || free.Reasoning != nil || len(free.ReasoningEfforts) != 0 {
		t.Fatalf("a price or flag Kenari did not publish must be null: %+v", free)
	}
	if m.Unsupported["serviceTier"] == "" {
		t.Fatal("the missing service tier must be stated, not silently absent")
	}
	if got := a.hx.requests(); !strings.Contains(got[len(got)-1].Path, "/api/model/options") || !strings.HasPrefix(got[len(got)-1].Path, "/p/"+u.profile+"/") {
		t.Fatalf("transport must come from the caller's own profile route: %+v", got[len(got)-1])
	}
}

// LLM-ACC-004/013: a model Kenari publishes nothing about is unknown, never zero or a default.
func TestUnpublishedMetadataIsNull(t *testing.T) {
	a := mediaApp(t, "bare")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"bare","endpoints":["chat"]}]}`))
	}))
	t.Cleanup(srv.Close)
	a.svc.kenari = kenari.New(srv.URL, nil)
	r, ok := a.user("a").models().find("bare")
	if !ok || r.Modalities != nil || r.ContextWindow != nil || r.Price != nil || r.Reasoning != nil || len(r.ReasoningEfforts) != 0 {
		t.Fatalf("unpublished fields must be null/empty: %+v", r)
	}
}

// LLM-002/003/004, LLM-ACC-002: the exact Kenari model is saved per chat and is what Hermes is asked to run.
func TestModelChoiceIsPerChatValidatedAndSent(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	c1, c2 := u.newChat(), u.newChat()
	if u.chatModel(c1) != nil {
		t.Fatal("a new chat starts on the profile default")
	}
	good := map[string]any{"model": kenariOnly, "reasoningEffort": "high"}
	if code, body := u.putSettings(c1, good); code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	if m := u.chatModel(c1); m == nil || m.Model != kenariOnly || m.Provider != "custom" || m.ReasoningEffort != "high" || m.ServiceTier != "" {
		t.Fatalf("not saved: %+v", m)
	}
	if u.chatModel(c2) != nil {
		t.Fatal("another chat must not inherit the choice")
	}
	s := decodeBody[struct {
		Model            *struct{ Model, ReasoningEffort string }
		State            string
		ReasoningEfforts []string
		Unsupported      map[string]string
	}](t, u.get("/api/chats/"+c1+"/settings"))
	if s.Model == nil || s.Model.Model != kenariOnly || s.State != "AVAILABLE" || !slices.Equal(s.ReasoningEfforts, []string{"low", "high", "max"}) || s.Unsupported["serviceTier"] == "" {
		t.Fatalf("settings: %+v", s)
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
	if body["model"] != kenariOnly || body["provider"] != "custom" || reasoning["effort"] != "high" || opts["service_tier"] != nil {
		t.Fatalf("run body does not carry the exact choice: %v", body)
	}
	if code, _ := u.putSettings(c1, map[string]any{"reset": true}); code != 200 || u.chatModel(c1) != nil {
		t.Fatal("reset must return the chat to the default")
	}
}

// LLM-ACC-002: nothing Kenari or the transport does not support is saved, coerced or sent.
func TestUnsupportedChoicesAreRefused(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	c := u.newChat()
	for name, tc := range map[string]struct {
		body map[string]any
		code string
	}{
		"unlisted model":        {map[string]any{"model": "nope"}, "MODEL_UNAVAILABLE"},
		"image-only model":      {map[string]any{"model": kenariImageOnly}, "MODEL_UNAVAILABLE"},
		"no model":              {map[string]any{}, "MODEL_UNAVAILABLE"},
		"foreign provider":      {map[string]any{"provider": "alpha", "model": kenariOnly}, "MODEL_UNAVAILABLE"},
		"effort Hermes lacks":   {map[string]any{"model": visionModel, "reasoningEffort": "none"}, "OPTION_UNSUPPORTED"},
		"effort not published":  {map[string]any{"model": kenariOnly, "reasoningEffort": "medium"}, "OPTION_UNSUPPORTED"},
		"effort without levels": {map[string]any{"model": kenariNoLevels, "reasoningEffort": "high"}, "OPTION_UNSUPPORTED"},
		"effort, no reasoning":  {map[string]any{"model": textOnlyModel, "reasoningEffort": "high"}, "OPTION_UNSUPPORTED"},
		"tier priority":         {map[string]any{"model": visionModel, "serviceTier": "priority"}, "OPTION_UNSUPPORTED"},
		"tier unknown":          {map[string]any{"model": visionModel, "serviceTier": "flex"}, "OPTION_UNSUPPORTED"},
	} {
		if code, out := u.putSettings(c, tc.body); code != 422 || !strings.Contains(out, tc.code) {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	for _, body := range []map[string]any{
		{"model": kenariOnly, "profile": "other"},
		{"model": kenariOnly, "apiKey": "x"},
		{"model": kenariOnly, "sessionId": "x"},
	} {
		if code, _ := u.putSettings(c, body); code != 400 {
			t.Errorf("a client-supplied server field must be rejected, got %d for %v", code, body)
		}
	}
	if u.chatModel(c) != nil {
		t.Fatal("a refused choice must save nothing")
	}
}

// Unsupported transport is stated, not papered over: Hermes' provider unusable -> nothing saved.
func TestChoiceNeedsAUsableTransport(t *testing.T) {
	a := mediaApp(t, visionModel)
	a.hx.mu.Lock()
	a.hx.canned["/api/model/options"] = `{"model":"` + visionModel + `","provider":"custom","providers":[{"slug":"custom","name":"Custom","is_current":true,"authenticated":false,"warning":"no key","models":[]}]}`
	a.hx.mu.Unlock()
	u := a.user("a")
	c := u.newChat()
	if code, out := u.putSettings(c, map[string]any{"model": kenariOnly}); code != 422 || !strings.Contains(out, "MODEL_TRANSPORT_UNAVAILABLE") {
		t.Fatalf("got %d %s", code, out)
	}
	if u.chatModel(c) != nil {
		t.Fatal("nothing may be saved")
	}
}

// A catalogue that cannot be read is an explicit error, never another list.
func TestCatalogueDownIsExplicit(t *testing.T) {
	a := mediaApp(t, visionModel)
	a.svc.kenari = kenari.New("http://127.0.0.1:1/v1/models", nil)
	u := a.user("a")
	if w := u.get("/api/models"); w.Code != 503 || errCode(t, w) != "CATALOGUE_UNAVAILABLE" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	c := u.newChat()
	if code, out := u.putSettings(c, map[string]any{"model": kenariOnly}); code != 503 || !strings.Contains(out, "CATALOGUE_UNAVAILABLE") {
		t.Fatalf("got %d %s", code, out)
	}
	a.svc.kenari = nil
	if w := u.get("/api/models"); w.Code != 503 {
		t.Fatalf("an unconfigured catalogue must not fall back to Hermes' list, got %d", w.Code)
	}
}

// LLM-ACC-001/002: independent choices per user; B cannot read or change A's chat settings.
func TestModelSettingsAreScopedToTheOwner(t *testing.T) {
	a := mediaApp(t, visionModel)
	ua, ub := a.user("a"), a.user("b")
	chatA, chatB := ua.newChat(), ub.newChat()
	if code, out := ua.putSettings(chatA, map[string]any{"model": kenariOnly, "reasoningEffort": "low"}); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := ub.putSettings(chatB, map[string]any{"model": kenariSixLevels, "reasoningEffort": "xhigh"}); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if m := ua.chatModel(chatA); m.Model != kenariOnly || m.ReasoningEffort != "low" {
		t.Fatalf("A's choice was disturbed: %+v", m)
	}
	// B addresses A's chat in every way the API offers.
	for name, w := range map[string]*httptest.ResponseRecorder{
		"GET settings": ub.get("/api/chats/" + chatA + "/settings"),
		"PUT settings": ub.do("PUT", "/api/chats/"+chatA+"/settings", nil, map[string]any{"model": kenariSixLevels}),
		"PUT model":    ub.do("PUT", "/api/chats/"+chatA+"/model", nil, map[string]any{"model": kenariSixLevels}),
		"PUT reset":    ub.do("PUT", "/api/chats/"+chatA+"/settings", nil, map[string]any{"reset": true}),
		"GET chat":     ub.get("/api/chats/" + chatA),
	} {
		if w.Code != 404 {
			t.Errorf("%s: B got %d, want 404", name, w.Code)
		}
		if strings.Contains(w.Body.String(), kenariOnly) {
			t.Errorf("%s: A's model leaked: %s", name, w.Body.String())
		}
	}
	if m := ua.chatModel(chatA); m == nil || m.Model != kenariOnly {
		t.Fatalf("B changed A's chat: %+v", m)
	}
	// What a send carries is the sender's own chat's model.
	ub.send(chatB, "hi")
	eventually(t, func() bool { return len(a.runPosts(t)) == 1 }, "B's run")
	if got := a.runPosts(t)[0]["model"]; got != kenariSixLevels {
		t.Fatalf("B's run named %v", got)
	}
}

// LLM-ACC-003: hiding is the caller's own; global admin policy always wins.
func TestModelVisibilityIsPerUserAndPolicyWins(t *testing.T) {
	a := mediaApp(t, visionModel)
	ua, ub := a.user("a"), a.user("b")
	before := len(a.hx.requests())
	if w := ua.do("PUT", "/api/me/model-visibility", nil, map[string]any{"hidden": []string{kenariOnly}}); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(a.hx.requests()) != before {
		t.Fatal("hiding a model must not reach Hermes")
	}
	if r, _ := ua.models().find(kenariOnly); !r.Hidden {
		t.Fatal("A's hidden model must be flagged for A")
	}
	if r, ok := ub.models().find(kenariOnly); !ok || r.Hidden {
		t.Fatal("B must still see A's hidden model, unhidden")
	}
	if got := decodeBody[struct{ Hidden []string }](t, ub.get("/api/me/model-visibility")).Hidden; len(got) != 0 {
		t.Fatalf("B read A's preference: %v", got)
	}
	// A hidden model is still selectable by A: hiding is a view preference.
	if code, _ := ua.putSettings(ua.newChat(), map[string]any{"model": kenariOnly}); code != 200 {
		t.Fatalf("a hidden model must stay selectable, got %d", code)
	}
	// Policy wins: blocked for everyone, even where A has not hidden it.
	admin := a.account("admin", "ADMIN", "APPROVED", "READY")
	if err := store.SetBlockedModels(context.Background(), a.db, admin.id, []string{kenariOnly}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*person{ua, ub} {
		if _, ok := p.models().find(kenariOnly); ok {
			t.Fatal("a policy-blocked model must not be offered")
		}
		if code, out := p.putSettings(p.newChat(), map[string]any{"model": kenariOnly}); code != 422 || !strings.Contains(out, "MODEL_UNAVAILABLE") {
			t.Fatalf("a policy-blocked model must not be selectable: %d %s", code, out)
		}
	}
	// A chat that saved it before the block is refused on send, never moved to another model.
	if err := store.SetBlockedModels(context.Background(), a.db, admin.id, nil, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	chat := ub.newChat()
	if code, _ := ub.putSettings(chat, map[string]any{"model": kenariOnly}); code != 200 {
		t.Fatal("setup")
	}
	if err := store.SetBlockedModels(context.Background(), a.db, admin.id, []string{kenariOnly}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := ub.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hello"}); w.Code != 422 || errCode(t, w) != "MODEL_UNAVAILABLE" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if len(a.runPosts(t)) != 0 {
		t.Fatal("no run may reach Hermes with a substitute model")
	}
	if s := decodeBody[struct{ State string }](t, ub.get("/api/chats/"+chat+"/settings")); s.State != "BLOCKED" {
		t.Fatalf("settings must say the saved model is blocked: %+v", s)
	}
	// Reveal / reset: an empty list.
	if w := ua.do("PUT", "/api/me/model-visibility", nil, map[string]any{"hidden": []string{}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for name, body := range map[string]any{"missing": map[string]any{}, "bad id": map[string]any{"hidden": []string{"a\nb"}}, "empty id": map[string]any{"hidden": []string{""}}, "extra": map[string]any{"hidden": []string{}, "userId": "x"}} {
		if w := ua.do("PUT", "/api/me/model-visibility", nil, body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

// Default deny: an account without a healthy binding makes no Hermes request.
func TestModelsNeedAHealthyTenant(t *testing.T) {
	a := newModelsApp(t)
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
	a := newModelsApp(t)
	u := a.user("a")
	a.hx.mu.Lock()
	a.hx.failNext = map[string]int{"GET /api/model/options": 404}
	a.hx.mu.Unlock()
	w := u.get("/api/models")
	if w.Code != 501 || errCode(t, w) != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

// The picker follows capability discovery: the route comes from /v1/capabilities and a Hermes that
// does not enable or advertise model_options gets no request to a guessed path.
func TestModelsNeedTheAdvertisedRoute(t *testing.T) {
	for name, caps := range map[string]string{
		"flag off":       `{"object":"hermes.api_server.capabilities","features":{"model_options":false},"endpoints":{"model_options":{"method":"GET","path":"/api/model/options"}}}`,
		"not advertised": `{"object":"hermes.api_server.capabilities","features":{"model_options":true},"endpoints":{}}`,
		"no document":    `{"nope":1}`,
	} {
		a := newApp(t)
		u := a.user("a")
		a.hx.mu.Lock()
		a.hx.canned["/v1/capabilities"] = caps
		a.hx.mu.Unlock()
		if w := u.get("/api/models"); w.Code != 501 || errCode(t, w) != "CAPABILITY_UNAVAILABLE" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		for _, r := range a.hx.requests() {
			if strings.Contains(r.Path, "/api/model/options") {
				t.Errorf("%s: a request reached %s", name, r.Path)
			}
		}
		if code, _ := u.putModel(u.newChat(), map[string]any{"provider": "alpha", "model": "m-reason"}); code != 501 {
			t.Errorf("%s: choosing a model must be refused, got %d", name, code)
		}
	}
}

// Global policy covers the profile default too: a chat with no saved choice, a new chat and a
// reset chat all refuse to send while the default is blocked, and no run reaches Hermes.
func TestBlockedDefaultModelIsNotExempt(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	admin := a.account("admin", "ADMIN", "APPROVED", "READY")
	reset := u.newChat()
	if code, _ := u.putSettings(reset, map[string]any{"model": kenariOnly}); code != 200 {
		t.Fatal("setup")
	}
	if code, _ := u.putSettings(reset, map[string]any{"reset": true}); code != 200 {
		t.Fatal("reset")
	}
	fresh := u.newChat()
	if err := store.SetBlockedModels(context.Background(), a.db, admin.id, []string{visionModel}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, chat := range map[string]string{"new chat": fresh, "reset chat": reset} {
		w := u.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]string{"input": "hello"})
		if w.Code != 422 || errCode(t, w) != "MODEL_UNAVAILABLE" {
			t.Errorf("%s: got %d %s", name, w.Code, w.Body.String())
		}
	}
	if n := len(a.runPosts(t)); n != 0 {
		t.Fatalf("%d runs reached Hermes with a blocked default", n)
	}
	// An unrelated block does not stop the default chat, and the run names the default explicitly.
	if err := store.SetBlockedModels(context.Background(), a.db, admin.id, []string{kenariOnly}, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	u.send(fresh, "hello")
	eventually(t, func() bool { return len(a.runPosts(t)) == 1 }, "the default run")
	if got := a.runPosts(t)[0]["model"]; got != visionModel {
		t.Fatalf("run named %v, want the checked default", got)
	}
}

// #145: fetchedAt is when Kenari was read, not when the picker asked; an answer kept through a Kenari
// outage is labelled stale; ?refresh=1 is accepted and does not break the answer.
func TestModelsSayHowOldTheCatalogueIs(t *testing.T) {
	a := mediaApp(t, visionModel)
	u := a.user("a")
	var down bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"data":[{"id":"` + visionModel + `","endpoints":["chat"]}]}`))
	}))
	t.Cleanup(srv.Close)
	a.svc.kenari = kenari.New(srv.URL, nil)
	read := time.Now()
	later := read.Add(3 * time.Hour)
	a.svc.now = func() time.Time { return later }
	type view struct {
		FetchedAt string
		Stale     bool
	}
	get := func(path string) view {
		w := u.get(path)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return decodeBody[view](t, w)
	}
	first := get("/api/models")
	got, err := time.Parse(time.RFC3339, first.FetchedAt)
	if err != nil || got.Before(read.Add(-time.Minute)) || got.After(read.Add(time.Minute)) || first.Stale {
		t.Fatalf("fetchedAt must be the Kenari read (%v), not the request clock (%v): %+v", read, later, first)
	}
	down = true
	if v := get("/api/models?refresh=1"); v.Stale || v.FetchedAt != first.FetchedAt {
		t.Fatalf("a refresh inside the throttle is served from the last read, not stale: %+v", v)
	}
}

// #146: the provider account is shared by every user. Hermes reports its quota windows (scope
// "account") in the inventory, and the picker used to pass them to any signed-in user. It carries
// none now: the only reader of account usage is the admin route (internal/auth/admin_usage.go).
func TestModelsNeverCarryTheSharedAccountQuota(t *testing.T) {
	a := mediaApp(t, visionModel)
	a.hx.mu.Lock()
	a.hx.canned["/api/model/options"] = `{"model":"` + visionModel + `","provider":"custom","providers":[{"slug":"custom","name":"Custom","is_current":true,
	  "models":["` + visionModel + `"],"capabilities":{"` + visionModel + `":{"fast":false,"reasoning":false}},
	  "limit":{"resets_at":"2026-10-24T10:00:00+00:00"},
	  "usage":{"windows":[{"label":"5h","used_percent":42.5,"resets_at":"2026-10-24T10:00:00+00:00","scope":"account"}]}}]}`
	a.hx.mu.Unlock()
	w := a.user("a").get("/api/models")
	if w.Code != 200 {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"quota", "42.5", "usedPercent", "limitedUntil", "resetsAt", "2026-10-24T10:00"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("GET /api/models carries %q of the shared provider account: %s", leak, w.Body.String())
		}
	}
}
