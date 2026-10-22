package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// All keys and names below are fabricated.
const (
	keyA = "fabricated-key-for-tenant-a-0123456789abcdef"
	keyB = "fabricated-key-for-tenant-b-0123456789abcdef"
	slA  = "u_aaaaaaaaaaaa"
	slB  = "u_bbbbbbbbbbbb"
)

type tset struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// fakeHermes models the multiplexed API: per-profile keys, a default profile
// with its own key, and a switch for every way a deployment could be wrong.
type fakeHermes struct {
	srv  *httptest.Server
	mu   sync.Mutex
	keys map[string]string

	defaultKey          string
	defaultAcceptsAny   bool // default route accepts a tenant key
	noKeyOK             bool
	anyKeyOK            bool
	siblingAcceptsOther bool // a tenant route accepts another tenant's key
	absentOK            bool
	redirectOwn         bool
	capsObject          string
	modelsBody          string
	toolsets            []tset
	toolsetsRaw         string
	sessions            int
	sessionsBody        string
	memWrite, adminRW   *bool
	authRequired        *bool
	hits                []string
}

func bp(b bool) *bool { return &b }

func newFake(t *testing.T) *fakeHermes {
	f := &fakeHermes{
		keys: map[string]string{slA: keyA, slB: keyB}, defaultKey: "fabricated-default-key-0123456789abcdef0",
		capsObject: "hermes.api_server.capabilities", memWrite: bp(false), adminRW: bp(false), authRequired: bp(true),
	}
	for _, n := range DeniedToolsets {
		f.toolsets = append(f.toolsets, tset{n, false})
	}
	for _, n := range []string{"web", "vision", "memory", "todo", "yuanbao", "kanban"} {
		f.toolsets = append(f.toolsets, tset{n, n != "yuanbao" && n != "kanban"})
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHermes) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, r.URL.Path)
	rest := r.URL.Path
	var want string
	known := true
	if strings.HasPrefix(rest, "/p/") {
		slug, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/p/"), "/")
		rest = "/" + tail
		if slug == absentSlug && f.absentOK {
			want = ""
		} else if k, ok := f.keys[slug]; ok {
			want = k
		} else {
			known = false
		}
	} else {
		want = f.defaultKey
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !known {
		http.Error(w, `{"error":"Unknown or unconfigured profile"}`, http.StatusNotFound)
		return
	}
	authed := got == want && got != ""
	if want == "" || f.anyKeyOK || (f.noKeyOK && got == "") {
		authed = true
	}
	if !strings.HasPrefix(r.URL.Path, "/p/") && f.defaultAcceptsAny && (got == keyA || got == keyB) {
		authed = true
	}
	if strings.HasPrefix(r.URL.Path, "/p/") && f.siblingAcceptsOther && (got == keyA || got == keyB) {
		authed = true
	}
	if !authed {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if f.redirectOwn && strings.HasSuffix(rest, "/v1/models") {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
		return
	}
	switch rest {
	case "/v1/capabilities":
		doc := map[string]any{"object": f.capsObject, "auth": map[string]any{}, "features": map[string]any{}}
		if f.authRequired != nil {
			doc["auth"] = map[string]any{"type": "bearer", "required": *f.authRequired}
		}
		feat := map[string]any{}
		if f.memWrite != nil {
			feat["memory_write_api"] = *f.memWrite
		}
		if f.adminRW != nil {
			feat["admin_config_rw"] = *f.adminRW
		}
		doc["features"] = feat
		json.NewEncoder(w).Encode(doc)
	case "/v1/models":
		if f.modelsBody != "" {
			w.Write([]byte(f.modelsBody))
			return
		}
		w.Write([]byte(`{"object":"list","data":[{"id":"hermes-agent","object":"model"}]}`))
	case "/v1/toolsets":
		if f.toolsetsRaw != "" {
			w.Write([]byte(f.toolsetsRaw))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "platform": "api_server", "data": f.toolsets})
	case "/api/sessions":
		if f.sessionsBody != "" {
			w.Write([]byte(f.sessionsBody))
			return
		}
		items := make([]map[string]string, f.sessions)
		for i := range items {
			items[i] = map[string]string{"id": "s"}
		}
		json.NewEncoder(w).Encode(map[string]any{"sessions": items})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeHermes) verifier(t *testing.T) *Verifier {
	v, err := NewVerifier(f.srv.URL, DefaultAllowedToolsets, f.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func target() Target { return Target{Profile: slA, Key: keyA, Siblings: []string{slB}, Fresh: true} }

func failed(r Report) map[string]string {
	m := map[string]string{}
	for _, c := range r {
		if !c.Passed {
			m[c.Check] = c.Reason
		}
	}
	return m
}

func TestVerifyPassesAWellConfiguredProfile(t *testing.T) {
	f := newFake(t)
	rep := f.verifier(t).Verify(context.Background(), target())
	if !rep.OK() || len(rep) != 6 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Detail() != "" {
		t.Fatalf("detail on success: %q", rep.Detail())
	}
}

func TestVerifyFailsClosedOnEveryDeviation(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeHermes)
		tgt   func(*Target)
		check string
	}{
		{"route does not exist (profile not registered)", func(f *fakeHermes) { delete(f.keys, slA) }, nil, CheckEndpoint},
		{"capabilities object differs", func(f *fakeHermes) { f.capsObject = "something.else" }, nil, CheckEndpoint},
		{"models answers in a different shape", func(f *fakeHermes) { f.modelsBody = `{"models":[]}` }, nil, CheckEndpoint},
		{"models answers with a redirect", func(f *fakeHermes) { f.redirectOwn = true }, nil, CheckEndpoint},
		{"default profile accepts the tenant key", func(f *fakeHermes) { f.defaultAcceptsAny = true }, nil, CheckIdentity},
		{"an unprovisioned profile answers", func(f *fakeHermes) { f.absentOK = true }, nil, CheckIdentity},
		{"route accepts no key at all", func(f *fakeHermes) { f.noKeyOK = true }, nil, CheckIsolation},
		{"route accepts any key", func(f *fakeHermes) { f.anyKeyOK = true }, nil, CheckIsolation},
		{"another tenant accepts this key", func(f *fakeHermes) { f.siblingAcceptsOther = true }, nil, CheckIsolation},
		{"new profile already has sessions", func(f *fakeHermes) { f.sessions = 2 }, nil, CheckSessions},
		{"sessions answers in an unrecognised shape", func(f *fakeHermes) { f.sessionsBody = `{"rows":"x"}` }, nil, CheckSessions},
		{"terminal toolset enabled", func(f *fakeHermes) { setTool(f, "terminal", true) }, nil, CheckToolsets},
		{"delegation toolset enabled", func(f *fakeHermes) { setTool(f, "delegation", true) }, nil, CheckToolsets},
		{"session_search toolset enabled", func(f *fakeHermes) { setTool(f, "session_search", true) }, nil, CheckToolsets},
		{"unknown toolset enabled", func(f *fakeHermes) { f.toolsets = append(f.toolsets, tset{"brand_new", true}) }, nil, CheckToolsets},
		{"a denied toolset is not reported", func(f *fakeHermes) { f.toolsets = f.toolsets[1:] }, nil, CheckToolsets},
		{"no toolsets reported", func(f *fakeHermes) { f.toolsetsRaw = `{"object":"list","data":[]}` }, nil, CheckToolsets},
		{"toolset enabled flag missing", func(f *fakeHermes) { f.toolsetsRaw = `{"object":"list","data":[{"name":"web"}]}` }, nil, CheckToolsets},
		{"memory write API exposed", func(f *fakeHermes) { f.memWrite = bp(true) }, nil, CheckMemory},
		{"memory write flag absent", func(f *fakeHermes) { f.memWrite = nil }, nil, CheckMemory},
		{"config writable through the API", func(f *fakeHermes) { f.adminRW = bp(true) }, nil, CheckMemory},
		{"auth not required", func(f *fakeHermes) { f.authRequired = bp(false) }, nil, CheckMemory},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			c.setup(f)
			tg := target()
			if c.tgt != nil {
				c.tgt(&tg)
			}
			rep := f.verifier(t).Verify(context.Background(), tg)
			if rep.OK() {
				t.Fatalf("READY-worthy report for %q: %+v", c.name, rep)
			}
			if _, ok := failed(rep)[c.check]; !ok {
				t.Fatalf("check %s did not fail: %+v", c.check, rep)
			}
			for _, secret := range []string{keyA, keyB, f.defaultKey} {
				if strings.Contains(rep.Detail(), secret) {
					t.Fatalf("detail leaks a key: %q", rep.Detail())
				}
			}
		})
	}
}

func setTool(f *fakeHermes, name string, on bool) {
	for i := range f.toolsets {
		if f.toolsets[i].Name == name {
			f.toolsets[i].Enabled = on
		}
	}
}

func TestSessionsMayBeNonEmptyAfterTheFirstVerification(t *testing.T) {
	f := newFake(t)
	f.sessions = 3
	tg := target()
	tg.Fresh = false
	if rep := f.verifier(t).Verify(context.Background(), tg); !rep.OK() {
		t.Fatalf("a re-verification of a profile with chats must pass: %+v", rep)
	}
}

func TestOwnKeyIsNeverSentToTheWrongPlaceBeyondTheProbes(t *testing.T) {
	// The tenant key goes only to the Hermes origin that was configured; the
	// verifier follows no redirect.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target was contacted: %s", r.URL.Path)
	}))
	defer other.Close()
	f := newFake(t)
	f.redirectOwn = true
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/models") && strings.HasPrefix(r.URL.Path, "/p/") {
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
			return
		}
		f.serve(w, r)
	})
	if rep := f.verifier(t).Verify(context.Background(), target()); rep.OK() {
		t.Fatalf("redirected verification passed: %+v", rep)
	}
}

func TestUnreachableHermesFailsEveryCheckWithoutLeaking(t *testing.T) {
	f := newFake(t)
	v := f.verifier(t)
	f.srv.Close()
	rep := v.Verify(context.Background(), target())
	if rep.OK() {
		t.Fatal("verification passed with Hermes down")
	}
	if len(failed(rep)) != 6 {
		t.Fatalf("every check must fail: %+v", rep)
	}
	if strings.Contains(rep.Detail(), keyA) || strings.Contains(rep.Detail(), f.srv.URL) {
		t.Fatalf("detail leaks: %q", rep.Detail())
	}
}

func TestVerifyRefusesBadTargets(t *testing.T) {
	f := newFake(t)
	v := f.verifier(t)
	for name, tg := range map[string]Target{
		"default profile": {Profile: "default", Key: keyA},
		"empty key":       {Profile: slA},
		"path injection":  {Profile: "u_a/../../x", Key: keyA},
		"bad sibling":     {Profile: slA, Key: keyA, Siblings: []string{"../default"}},
		"self as sibling": {Profile: slA, Key: keyA, Siblings: []string{slA}},
	} {
		if rep := v.Verify(context.Background(), tg); rep.OK() {
			t.Errorf("%s: passed", name)
		}
	}
	if len(f.hits) != 0 {
		t.Fatalf("a refused target still reached Hermes: %v", f.hits)
	}
	if (Report{}).OK() {
		t.Fatal("an empty report must not be OK")
	}
}

func TestToolsetPolicyFloorCannotBeConfiguredAway(t *testing.T) {
	for _, n := range DeniedToolsets {
		if _, err := NewVerifier("http://hermes:8642", append([]string{"web"}, n), nil); err == nil {
			t.Errorf("allow-list containing %s was accepted", n)
		}
	}
	if _, err := NewVerifier("http://hermes:8642", DefaultAllowedToolsets, nil); err != nil {
		t.Fatalf("default policy rejected: %v", err)
	}
	// A narrower allow-list is fine, and is enforced: memory is enabled on the fake.
	f := newFake(t)
	v, err := NewVerifier(f.srv.URL, []string{"web", "vision", "todo"}, f.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if rep := v.Verify(context.Background(), target()); failed(rep)[CheckToolsets] == "" {
		t.Fatalf("narrowed policy not enforced: %+v", rep)
	}
}

func TestNewVerifierRefusesUnsafeBaseURLs(t *testing.T) {
	for _, u := range []string{"", "ftp://hermes", "http://user:pw@hermes:8642", "http://hermes:8642/p/x", "http://hermes:8642?x=1", "hermes:8642"} {
		if _, err := NewVerifier(u, DefaultAllowedToolsets, nil); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
}
