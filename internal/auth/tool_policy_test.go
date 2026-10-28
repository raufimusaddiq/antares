package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

// Admin → Assistant tools: only an admin may read or set the tenant toolset policy, only offered toolsets can be
// chosen (never the floor), the change is audited, and every ready account is sent back through verification.
func TestToolPolicyIsAdminOnlyOfferedOnlyAndReverifiesReadyAccounts(t *testing.T) {
	h := newHarness(t)
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)
	anon := h.browser()
	body := `{"allowed":["web","tts"]}`
	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": anon} {
		if w := b.send("PUT", "/api/admin/tool-policy", body, sameOriginHdr); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s PUT = %d", name, w.Code)
		}
		if w := b.get("/api/admin/tool-policy"); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
	}
	if w := admin.send("PUT", "/api/admin/tool-policy", body, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("cross-origin PUT = %d", w.Code)
	}
	for name, bad := range map[string]string{"no list": `{}`, "empty": `{"allowed":[]}`, "extra field": `{"allowed":["web"],"userId":"x"}`,
		"floor": `{"allowed":["terminal"]}`, "browser": `{"allowed":["web","browser"]}`, "not reviewed": `{"allowed":["computer_use"]}`,
		"twice": `{"allowed":["web","web"]}`, "unknown": `{"allowed":["zz"]}`} {
		if w := admin.send("PUT", "/api/admin/tool-policy", bad, sameOriginHdr); w.Code != 400 {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM tenant_tool_policy`); n != 0 {
		t.Fatalf("a refused call stored a policy")
	}
	var v struct {
		Tools       []struct{ Name, State, Reason string }
		Custom      bool
		Reverifying *int
	}
	w := admin.get("/api/admin/tool-policy")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Custom {
		t.Fatalf("default GET = %d %s", w.Code, w.Body.String())
	}
	w = admin.send("PUT", "/api/admin/tool-policy", body, sameOriginHdr)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || !v.Custom || v.Reverifying == nil || *v.Reverifying != 2 {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body.String())
	}
	states := map[string]string{}
	for _, tl := range v.Tools {
		states[tl.Name] = tl.State
		if tl.State == "locked" && tl.Reason == "" {
			t.Errorf("%s is locked without a reason", tl.Name)
		}
	}
	for name, want := range map[string]string{"web": "on", "tts": "on", "memory": "off", "terminal": "locked", "browser": "locked", "computer_use": "locked"} {
		if states[name] != want {
			t.Errorf("%s = %q, want %q", name, states[name], want)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM users WHERE tenant_status = 'CONFIGURING'`); n != 2 {
		t.Errorf("%d accounts re-verifying, want 2", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'tool_policy.set'`); n != 1 {
		t.Fatalf("%d audit rows, want 1", n)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "hermes") {
		t.Errorf("the response names the gateway: %s", w.Body.String())
	}
}
