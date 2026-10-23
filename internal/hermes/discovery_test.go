package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeDiscovery serves bodies by Hermes path under /p/<profile>. A path that is
// absent from the map answers 404, as a route the deployed build lacks would.
func fakeDiscovery(t *testing.T, bodies map[string]string) (*Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/p/"+testProfile)
		b, ok := bodies[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if code, refused := strings.CutPrefix(b, "!"); refused { // "!401": answer with that status
			n, _ := strconv.Atoi(code)
			w.WriteHeader(n)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c, &seen
}

func realBodies(t *testing.T) map[string]string {
	return map[string]string{
		"/v1/capabilities":   fixture(t, "../../docs/hermes/fixtures/capabilities.body.json"),
		"/v1/toolsets":       fixture(t, "../../docs/hermes/fixtures/toolsets.body.json"),
		"/v1/skills":         fixture(t, "testdata/skills.body.json"),
		"/api/model/options": fixture(t, "testdata/model-options.body.json"),
	}
}

func byID(inv Inventory, id string) Capability {
	for _, c := range inv.Capabilities {
		if c.ID == id {
			return c
		}
	}
	return Capability{}
}

func TestDiscoverDerivesFromTheRealCapturedPayloads(t *testing.T) {
	c, seen := fakeDiscovery(t, realBodies(t))
	inv, err := c.Discover(context.Background(), bind())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		CapModels: StatusAvailable, CapModelLock: StatusAvailable, CapSkills: StatusAvailable,
		CapToolsets: StatusAvailable, CapToolProgress: StatusAvailable,
		// admin_config_rw is false in the captured /v1/capabilities.
		CapSettings: StatusUnsupported,
	}
	for id, st := range want {
		if got := byID(inv, id); got.Status != st {
			t.Errorf("%s = %q (%s), want %q", id, got.Status, got.Reason, st)
		}
	}
	if s := byID(inv, CapSettings); s.Reason == "" {
		t.Error("an unsupported capability must say why")
	}
	// Toolsets mirror the capture: browser is configured but not enabled; web is both.
	ts := map[string]Toolset{}
	for _, x := range inv.Toolsets {
		ts[x.Name] = x
	}
	if ts["web"].Status != StatusAvailable || ts["browser"].Status != StatusUnsupported || !strings.Contains(ts["browser"].Reason, "Not enabled") {
		t.Errorf("web=%+v browser=%+v", ts["web"], ts["browser"])
	}
	// Only an explicitly authenticated provider is offered.
	if len(inv.Providers) != 1 || inv.Providers[0].Slug != "example-ai" || len(inv.Providers[0].Models) != 2 {
		t.Errorf("providers = %+v", inv.Providers)
	}
	if len(inv.Skills) != 2 {
		t.Errorf("skills = %+v", inv.Skills)
	}
	for _, r := range *seen {
		if !strings.HasPrefix(r, "GET /p/"+testProfile+"/") {
			t.Errorf("discovery made a non-GET or off-profile request: %s", r)
		}
	}
}

func TestDiscoverNeverDefaultsToAvailable(t *testing.T) {
	cases := map[string]func(m map[string]string){
		"capabilities route missing": func(m map[string]string) { delete(m, "/v1/capabilities") },
		"capabilities is not a document": func(m map[string]string) {
			m["/v1/capabilities"] = `{"object":"something.else","features":{"skills_api":true}}`
		},
		"capabilities is malformed json": func(m map[string]string) { m["/v1/capabilities"] = `{"features":` },
		"features absent":                func(m map[string]string) { m["/v1/capabilities"] = `{"object":"hermes.api_server.capabilities"}` },
		"flags are strings, not booleans": func(m map[string]string) {
			m["/v1/capabilities"] = `{"object":"hermes.api_server.capabilities","features":{"skills_api":"true","model_options":1,"session_model_lock":"yes","tool_progress_events":"true","run_events_sse":true},"endpoints":{"skills":{"method":"GET","path":"/v1/skills"},"model_options":{"method":"GET","path":"/api/model/options"}}}`
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := realBodies(t)
			mutate(m)
			c, _ := fakeDiscovery(t, m)
			inv, err := c.Discover(context.Background(), bind())
			if err != nil {
				t.Fatal(err)
			}
			if len(inv.Capabilities) != 6 {
				t.Fatalf("want a verdict for every capability, got %d", len(inv.Capabilities))
			}
			for _, cp := range inv.Capabilities {
				if cp.Status != StatusUnsupported || cp.Reason == "" {
					t.Errorf("%s = %q reason %q: must be unsupported with a reason", cp.ID, cp.Status, cp.Reason)
				}
			}
			if len(inv.Skills)+len(inv.Providers) != 0 {
				t.Errorf("unsupported capabilities must not list items: %+v", inv)
			}
		})
	}
}

func TestDiscoverEachSourceFailsAlone(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]string)
		id     string
	}{
		{"skills body malformed", func(m map[string]string) { m["/v1/skills"] = `{"object":"list","data":"x"}` }, CapSkills},
		{"skills route 404", func(m map[string]string) { delete(m, "/v1/skills") }, CapSkills},
		{"skills empty", func(m map[string]string) { m["/v1/skills"] = `{"object":"list","data":[]}` }, CapSkills},
		{"toolsets wrong envelope", func(m map[string]string) { m["/v1/toolsets"] = `{"object":"error"}` }, CapToolsets},
		{"every toolset disabled", func(m map[string]string) {
			m["/v1/toolsets"] = `{"object":"list","data":[{"name":"web","enabled":false,"configured":true,"tools":[]}]}`
		}, CapToolsets},
		{"toolset flags missing", func(m map[string]string) {
			m["/v1/toolsets"] = `{"object":"list","data":[{"name":"web","tools":["web_search"]}]}`
		}, CapToolsets},
		{"model options without providers key", func(m map[string]string) { m["/api/model/options"] = `{"model":"x"}` }, CapModels},
		{"no authenticated provider", func(m map[string]string) {
			m["/api/model/options"] = `{"providers":[{"slug":"a","authenticated":false,"models":["m"]},{"slug":"b","models":["m"]},{"slug":"c","authenticated":"true","models":["m"]}]}`
		}, CapModels},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := realBodies(t)
			tc.mutate(m)
			c, _ := fakeDiscovery(t, m)
			inv, err := c.Discover(context.Background(), bind())
			if err != nil {
				t.Fatal(err)
			}
			if got := byID(inv, tc.id); got.Status != StatusUnsupported || got.Reason == "" {
				t.Errorf("%s = %+v, want unsupported with a reason", tc.id, got)
			}
			if got := byID(inv, CapToolProgress); got.Status != StatusAvailable {
				t.Errorf("one failed source must not take down the others: %+v", got)
			}
		})
	}
}

func TestDiscoverUsesOnlyAdvertisedSafeRoutes(t *testing.T) {
	m := realBodies(t)
	m["/v1/capabilities"] = `{"object":"hermes.api_server.capabilities","features":{"skills_api":true},"endpoints":{"skills":{"method":"POST","path":"/v1/skills"},"toolsets":{"method":"GET","path":"/v1/../admin"}}}`
	c, seen := fakeDiscovery(t, m)
	inv, err := c.Discover(context.Background(), bind())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{CapSkills, CapToolsets} {
		if byID(inv, id).Status != StatusUnsupported {
			t.Errorf("%s offered from a route Hermes did not advertise as a plain GET", id)
		}
	}
	if len(*seen) != 1 {
		t.Errorf("only /v1/capabilities should have been requested, got %v", *seen)
	}
}

func TestDiscoverRefusedBindingIsAnErrorNotAnInventory(t *testing.T) {
	c, _ := fakeDiscovery(t, realBodies(t))
	for _, b := range []Binding{{Profile: testProfile, Key: "wrong-key"}, {}} {
		if _, err := c.Discover(context.Background(), b); CodeOf(err) != CodeTenantNotReady {
			t.Errorf("binding %v: err = %v, want TENANT_NOT_READY", b, err)
		}
	}
}

// Capabilities answered, then the binding is refused on a later read: no partial
// inventory, whichever secondary route refuses and whether 401 or 403.
func TestDiscoverSecondaryRefusalIsTenantNotReady(t *testing.T) {
	for _, path := range []string{"/v1/toolsets", "/v1/skills", "/api/model/options"} {
		for _, code := range []string{"!401", "!403"} {
			m := realBodies(t)
			m[path] = code
			c, _ := fakeDiscovery(t, m)
			inv, err := c.Discover(context.Background(), bind())
			if CodeOf(err) != CodeTenantNotReady || len(inv.Capabilities) != 0 || len(inv.Toolsets)+len(inv.Providers)+len(inv.Skills) != 0 {
				t.Errorf("%s %s: err=%v inv=%+v, want TENANT_NOT_READY and no inventory", path, code, err, inv)
			}
		}
	}
}

// Empty or unreadable catalogs must marshal as [] and never null: the browser
// treats null as an unreadable answer and would drop the whole inventory.
func TestDiscoverListsAreNeverNull(t *testing.T) {
	for name, mutate := range map[string]func(m map[string]string){
		"empty catalog":      func(m map[string]string) { m["/api/model/options"] = `{"providers":[]}` },
		"unreadable catalog": func(m map[string]string) { m["/api/model/options"] = `{` },
		"missing catalog":    func(m map[string]string) { delete(m, "/api/model/options") },
		"nothing readable":   func(m map[string]string) { delete(m, "/v1/capabilities") },
	} {
		m := realBodies(t)
		mutate(m)
		c, _ := fakeDiscovery(t, m)
		inv, err := c.Discover(context.Background(), bind())
		if err != nil {
			t.Fatal(name, err)
		}
		raw, _ := json.Marshal(inv)
		for _, k := range []string{"providers", "toolsets", "skills", "capabilities"} {
			if strings.Contains(string(raw), `"`+k+`":null`) {
				t.Errorf("%s: %s is null in %s", name, k, raw)
			}
		}
	}
}
