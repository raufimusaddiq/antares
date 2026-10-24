package auth

import (
	"strings"
	"testing"
)

// LLM-ACC-003: the global model block list is the admin's alone, is audited, and holds no user content.
func TestModelPolicyIsAdminOnlyAndAudited(t *testing.T) {
	h := newHarness(t)
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)
	anon := h.browser()
	body := `{"blocked":["some-model"]}`
	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": anon} {
		if w := b.send("PUT", "/api/admin/model-policy", body, sameOriginHdr); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s PUT = %d", name, w.Code)
		}
		if w := b.get("/api/admin/model-policy"); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM model_blocked`); n != 0 {
		t.Fatalf("a refused call blocked %d models", n)
	}
	if w := admin.send("PUT", "/api/admin/model-policy", body, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}); w.Code != 403 {
		t.Errorf("cross-origin PUT = %d", w.Code)
	}
	for name, bad := range map[string]string{"no list": `{}`, "extra field": `{"blocked":[],"userId":"x"}`, "empty id": `{"blocked":[""]}`, "control char": `{"blocked":["a\nb"]}`} {
		if w := admin.send("PUT", "/api/admin/model-policy", bad, sameOriginHdr); w.Code != 400 {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if w := admin.send("PUT", "/api/admin/model-policy", body, sameOriginHdr); w.Code != 200 || !strings.Contains(w.Body.String(), "some-model") {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body.String())
	}
	if w := admin.get("/api/admin/model-policy"); w.Code != 200 || !strings.Contains(w.Body.String(), `"blocked":["some-model"]`) {
		t.Fatalf("admin GET = %d %s", w.Code, w.Body.String())
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'model_policy.set'`); n != 1 {
		t.Fatalf("%d audit rows, want 1", n)
	}
}
