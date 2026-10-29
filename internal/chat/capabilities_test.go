package chat

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serveDiscovery makes the fake Hermes answer the discovery routes with the real
// captured /v1/capabilities and /v1/toolsets, and source-shaped skills and model
// options (internal/hermes/testdata/README.md).
func serveDiscovery(a *app, capsBody string) {
	a.hx.mu.Lock()
	defer a.hx.mu.Unlock()
	a.hx.canned["/v1/capabilities"] = capsBody
	a.hx.canned["/v1/toolsets"] = readFixtureNoT("../../docs/hermes/fixtures/toolsets.body.json")
	a.hx.canned["/v1/skills"] = readFixtureNoT("../hermes/testdata/skills.body.json")
	a.hx.canned["/api/model/options"] = readFixtureNoT("../hermes/testdata/model-options.body.json")
}

func readFixtureNoT(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func decodeInv(t *testing.T, body string) hermes.Inventory {
	t.Helper()
	var inv hermes.Inventory
	if err := json.Unmarshal([]byte(body), &inv); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return inv
}

func capByID(inv hermes.Inventory, id string) hermes.Capability {
	for _, c := range inv.Capabilities {
		if c.ID == id {
			return c
		}
	}
	return hermes.Capability{}
}

func TestCapabilitiesReportsWhatHermesConfirmsAndNoMore(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	serveDiscovery(a, readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json"))

	w := alice.get("/api/capabilities")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	inv := decodeInv(t, w.Body.String())
	if got := capByID(inv, hermes.CapSkills); got.Status != hermes.StatusAvailable {
		t.Errorf("skills = %+v", got)
	}
	// The captured document has admin_config_rw=false: that must reach the browser as unsupported, with why.
	if got := capByID(inv, hermes.CapSettings); got.Status != hermes.StatusUnsupported || got.Reason == "" {
		t.Errorf("settings = %+v", got)
	}
	// Discovery used the caller's own profile and key, and nothing else.
	for _, r := range a.hx.requests() {
		// The one non-GET: the clarify route probe, about a run that cannot exist, on the caller's own profile.
		probe := r.Method == "POST" && r.Path == "/p/"+alice.profile+"/v1/runs/run_0000000000000000antaresprobe/clarify"
		if (r.Method != "GET" && !probe) || !strings.HasPrefix(r.Path, "/p/"+alice.profile+"/") || r.Auth != "Bearer "+alice.key {
			t.Errorf("unexpected Hermes request %s %s", r.Method, r.Path)
		}
	}
	if strings.Contains(w.Body.String(), alice.key) || strings.Contains(w.Body.String(), alice.profile) {
		t.Error("the inventory leaked the binding")
	}
}

func TestCapabilitiesUnconfiguredIsUnsupportedNotAvailable(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	// A Hermes that reports skills off, no model options, and no tool progress.
	serveDiscovery(a, `{"object":"hermes.api_server.capabilities","features":{"skills_api":false,"model_options":false,"tool_progress_events":false,"run_events_sse":true},"endpoints":{}}`)
	inv := decodeInv(t, alice.get("/api/capabilities").Body.String())
	for _, id := range []string{hermes.CapSkills, hermes.CapModels, hermes.CapModelLock, hermes.CapToolProgress, hermes.CapSettings, hermes.CapToolsets} {
		c := capByID(inv, id)
		if c.Status != hermes.StatusUnsupported || c.Reason == "" {
			t.Errorf("%s = %+v, want unsupported with a reason", id, c)
		}
	}
	if len(inv.Skills) != 0 || len(inv.Providers) != 0 || len(inv.Toolsets) != 0 {
		t.Errorf("items listed for unsupported capabilities: %+v", inv)
	}
}

func TestCapabilitiesDefaultDeny(t *testing.T) {
	a := newApp(t)
	serveDiscovery(a, readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json"))
	for who, p := range map[string]*person{
		"pending":    a.account("pend", "USER", "PENDING", "UNPROVISIONED"),
		"suspended":  a.account("susp", "USER", "SUSPENDED", "DISABLED"),
		"no binding": a.account("nobind", "USER", "APPROVED", "UNPROVISIONED"),
	} {
		if w := p.get("/api/capabilities"); w.Code < 400 || w.Code >= 500 || strings.Contains(w.Body.String(), "capabilities\":[") {
			t.Errorf("%s: %d %s", who, w.Code, w.Body.String())
		}
	}
	if n := len(a.hx.requests()); n != 0 {
		t.Errorf("%d request(s) reached Hermes for accounts that may not use it", n)
	}
	// A key Hermes refuses yields an error, not an inventory.
	alice := a.user("alice")
	a.hx.mu.Lock()
	a.hx.keys[alice.profile] = "rotated-away"
	a.hx.mu.Unlock()
	if w := alice.get("/api/capabilities"); w.Code != 409 || errCode(t, w) != "TENANT_NOT_READY" {
		t.Errorf("refused binding: %d %s", w.Code, w.Body.String())
	}
}

// The exact bytes a browser receives for an empty/unreadable catalog must be the
// golden file the web tests parse (web/src/testdata/capabilities-empty.test.json):
// one fixture crossing the server-to-client boundary from both sides.
func TestCapabilitiesEmptyCatalogsMatchTheClientGolden(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	serveDiscovery(a, `{"object":"hermes.api_server.capabilities","features":{"model_options":true,"session_model_lock":true,"tool_progress_events":true,"run_events_sse":true,"skills_api":true},"endpoints":{"model_options":{"method":"GET","path":"/api/model/options"},"toolsets":{"method":"GET","path":"/v1/toolsets"},"skills":{"method":"GET","path":"/v1/skills"}}}`)
	a.hx.mu.Lock()
	a.hx.canned["/api/model/options"] = `{"providers":[]}`
	a.hx.canned["/v1/toolsets"] = `{"object":"list","data":[]}`
	a.hx.canned["/v1/skills"] = `{"object":"list","data":[]}`
	a.hx.mu.Unlock()
	w := alice.get("/api/capabilities")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got, want any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(readFixture(t, "../../web/src/testdata/capabilities-empty.test.json")), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("server answer differs from the client golden:\n got  %s\n want %s", w.Body.String(), readFixture(t, "../../web/src/testdata/capabilities-empty.test.json"))
	}
}

// A secondary read refused after capabilities succeeded yields no inventory.
func TestCapabilitiesSecondaryRefusalIsTenantNotReady(t *testing.T) {
	for _, path := range []string{"/v1/toolsets", "/v1/skills", "/api/model/options"} {
		for _, st := range []int{401, 403} {
			a := newApp(t)
			alice := a.user("alice")
			serveDiscovery(a, readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json"))
			a.hx.mu.Lock()
			a.hx.failNext["GET "+path] = st // one-shot; each route is read once
			a.hx.mu.Unlock()
			w := alice.get("/api/capabilities")
			if w.Code != 409 || errCode(t, w) != "TENANT_NOT_READY" || strings.Contains(w.Body.String(), `"capabilities"`) {
				t.Errorf("%s %d: %d %s", path, st, w.Code, w.Body.String())
			}
		}
	}
}
