package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
)

// Admin → Models (specs/004-admin-full-control): one read for the screen, defaults per purpose, switches that refuse to leave a
// purpose without a model. Each test is one thing an administrator or a user can see.

func modelsHarness(t *testing.T) (*harness, *browser, *browser) {
	t.Helper()
	h := newHarness(t)
	h.a.catalogue = fakeCatalogue{models: []kenari.Model{
		{ID: "gpt-oss-20b", Name: "GPT OSS 20B", Endpoints: []string{"chat"}}, {ID: "deepseek", Name: "DeepSeek", Endpoints: []string{"chat"}},
		{ID: "glm", Name: "GLM", Endpoints: []string{"chat"}}, {ID: "old", Name: "Old", Endpoints: []string{"chat"}, SunsetAt: "2020-01-01T00:00:00Z"},
		row("agnes-image-2-5-flash:free", "images", "text"), row("veo-3.1-lite", "videos", "text"),
	}}
	h.a.now = func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }
	return h, h.userIn("adm", "APPROVED", "READY", "ADMIN", true), h.userIn("usr", "APPROVED", "READY", "USER", true)
}

type modelsJSON struct {
	Catalogue string
	Models    []struct {
		ID         string
		On         bool
		Purposes   []string
		DefaultFor []string
		Usage      *struct{ Calls, Tokens int64 }
	}
	Purposes map[string]struct{ Current, Effective, Warning string }
}

func readModels(t *testing.T, b *browser) modelsJSON {
	t.Helper()
	w := b.get("/api/admin/models")
	if w.Code != 200 {
		t.Fatalf("GET models: %d %s", w.Code, w.Body)
	}
	var v modelsJSON
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (m modelsJSON) model(id string) (on bool, defaultFor []string, found bool) {
	for _, x := range m.Models {
		if x.ID == id {
			return x.On, x.DefaultFor, true
		}
	}
	return false, nil, false
}

func TestOnlyAnAdministratorReadsOrSetsModels(t *testing.T) {
	h, admin, user := modelsHarness(t)
	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": h.browser()} {
		if w := b.get("/api/admin/models"); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
		if w := b.send("PUT", "/api/admin/model-defaults", `{"chat":"deepseek"}`, sameOriginHdr); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s PUT = %d", name, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM model_defaults`); n != 0 {
		t.Fatalf("a refused call stored %d rows", n)
	}
	v := readModels(t, admin)
	if v.Catalogue != "ok" {
		t.Errorf("catalogue: %q", v.Catalogue)
	}
}

func TestTheModelsReadListsEveryModelWithItsPurposesAndNoAccountField(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	h.exec(`INSERT INTO model_blocked (model_id, blocked_by, created_at) VALUES ('glm', ?, 'x')`, h.userID("adm"))
	user := h.userID("usr")
	var binding string
	if err := h.db.QueryRow(`SELECT id FROM tenant_bindings WHERE user_id = ?`, user).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	h.exec(`INSERT INTO chats (id, user_id, hermes_binding_id, hermes_session_id, status, created_at, updated_at) VALUES ('c1', ?, ?, 's1', 'ACTIVE', 'x', 'x')`, user, binding)
	h.exec(`INSERT INTO runs (id, chat_id, user_id, idempotency_key, status, input_tokens, output_tokens, usage_model, created_at, updated_at) VALUES ('r1','c1',?, 'k1', 'COMPLETED', 100, 50, 'deepseek', '2026-11-09T00:00:00.000000000Z','x')`, user)
	w := admin.get("/api/admin/models")
	body := w.Body.String()
	for _, banned := range []string{user, "usr@", "userId", "c1", "r1"} {
		if strings.Contains(body, banned) {
			t.Errorf("the models read names %q: %s", banned, body)
		}
	}
	v := readModels(t, admin)
	if on, _, ok := v.model("glm"); !ok || on {
		t.Errorf("glm should be listed and off")
	}
	if _, _, ok := v.model("old"); ok {
		t.Errorf("a model past its sunset is offered")
	}
	for _, m := range v.Models {
		if m.ID == "deepseek" && (m.Usage == nil || m.Usage.Calls != 1 || m.Usage.Tokens != 150) {
			t.Errorf("usage totals: %+v", m.Usage)
		}
	}
	if _, df, _ := v.model("agnes-image-2-5-flash:free"); strings.Join(df, ",") != "image" {
		t.Errorf("the plugin's own image default counts as a default: %v", df)
	}
}

func TestDefaultsPerPurposeAreSavedAuditedAndOnlyFittingModelsAreAccepted(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	put := func(b string) int { return admin.send("PUT", "/api/admin/model-defaults", b, sameOriginHdr).Code }
	for name, bad := range map[string]string{"unknown model": `{"chat":"nope"}`, "past sunset": `{"chat":"old"}`, "an image model for chat": `{"chat":"veo-3.1-lite"}`,
		"extra field": `{"chat":"deepseek","userId":"x"}`, "control char": `{"summary":"a\nb"}`, "switched off": ``} {
		if bad == "" {
			h.exec(`INSERT INTO model_blocked (model_id, blocked_by, created_at) VALUES ('glm', ?, 'x')`, h.userID("adm"))
			bad = `{"blog_assistant":"glm"}`
		}
		if c := put(bad); c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM model_defaults`); n != 0 {
		t.Fatalf("refused calls stored %d rows", n)
	}
	if c := put(`{"chat":"deepseek","summary":"gpt-oss-20b"}`); c != 200 {
		t.Fatalf("save: %d", c)
	}
	v := readModels(t, admin)
	if v.Purposes["chat"].Current != "deepseek" || v.Purposes["chat"].Effective != "deepseek" || v.Purposes["summary"].Current != "gpt-oss-20b" || v.Purposes["blog_assistant"].Current != "" {
		t.Errorf("purposes: %+v", v.Purposes)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'model_defaults.set'`); n != 2 {
		t.Errorf("%d audit rows, want 2", n)
	}
	// Omitted is unchanged; "" returns a purpose to the service default.
	if c := put(`{"chat":""}`); c != 200 {
		t.Fatal(c)
	}
	v = readModels(t, admin)
	if v.Purposes["chat"].Current != "" || v.Purposes["summary"].Current != "gpt-oss-20b" {
		t.Errorf("after clearing chat: %+v", v.Purposes)
	}
}

func TestAModelThatIsADefaultCannotBeSwitchedOffAndNothingChanges(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	admin.send("PUT", "/api/admin/model-defaults", `{"chat":"deepseek"}`, sameOriginHdr)
	w := admin.send("PUT", "/api/admin/model-policy", `{"blocked":["deepseek","glm"]}`, sameOriginHdr)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "MODEL_IS_DEFAULT") || !strings.Contains(w.Body.String(), `"purposes":["chat"]`) {
		t.Fatalf("switching off a default: %d %s", w.Code, w.Body)
	}
	if n := h.count(`SELECT COUNT(*) FROM model_blocked`); n != 0 {
		t.Errorf("a refused switch-off stored %d rows (glm included)", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'model_policy.set'`); n != 0 {
		t.Errorf("a refusal wrote an audit row")
	}
	// The plugin's own media default counts too, and so does a media default the admin chose.
	if w := admin.send("PUT", "/api/admin/model-policy", `{"blocked":["veo-3.1-lite"]}`, sameOriginHdr); w.Code != 409 || !strings.Contains(w.Body.String(), "video") {
		t.Errorf("the plugin's video default: %d %s", w.Code, w.Body)
	}
	// A model that is not a default can be switched off, and then it cannot become one.
	if w := admin.send("PUT", "/api/admin/model-policy", `{"blocked":["glm"]}`, sameOriginHdr); w.Code != 200 {
		t.Fatalf("switching off glm: %d %s", w.Code, w.Body)
	}
	if c := admin.send("PUT", "/api/admin/model-defaults", `{"summary":"glm"}`, sameOriginHdr).Code; c != 400 {
		t.Errorf("an off model became a default: %d", c)
	}
	if c := admin.send("PUT", "/api/admin/media-models", `{"image":"","video":""}`, sameOriginHdr).Code; c != 200 {
		t.Errorf("media: %d", c)
	}
	h.exec(`UPDATE users SET tenant_status = 'READY'`) // the re-verification that save started has finished
	h.exec(`UPDATE tenant_bindings SET status = 'READY'`)
	// Switched back on, it is a choice again; a default already off before this change does not block saving the list.
	if w := admin.send("PUT", "/api/admin/model-policy", `{"blocked":[]}`, sameOriginHdr); w.Code != 200 {
		t.Errorf("switching everything on: %d %s", w.Code, w.Body)
	}
}

func TestADefaultThatStoppedWorkingIsWarnedAboutAndTheServiceDefaultIsInEffect(t *testing.T) {
	h, admin, _ := modelsHarness(t)
	admin.send("PUT", "/api/admin/model-defaults", `{"chat":"deepseek","blog_assistant":"gpt-oss-20b"}`, sameOriginHdr)
	// Kenari drops deepseek; gpt-oss-20b is later switched off by another route.
	h.a.catalogue = fakeCatalogue{models: []kenari.Model{{ID: "gpt-oss-20b", Name: "x", Endpoints: []string{"chat"}}}}
	h.exec(`INSERT INTO model_blocked (model_id, blocked_by, created_at) VALUES ('gpt-oss-20b', ?, 'x')`, h.userID("adm"))
	v := readModels(t, admin)
	if p := v.Purposes["chat"]; p.Warning != "not_listed" || p.Effective != "" || p.Current != "deepseek" {
		t.Errorf("chat: %+v", p)
	}
	if p := v.Purposes["blog_assistant"]; p.Warning != "off" || p.Effective != "" {
		t.Errorf("blog assistant: %+v", p)
	}
	// An unreadable catalogue keeps what is saved and says so; it never claims a model is gone.
	h.a.catalogue = fakeCatalogue{err: kenari.ErrUnavailable}
	v = readModels(t, admin)
	if v.Catalogue != "unavailable" || v.Purposes["chat"].Warning != "" || v.Purposes["chat"].Current != "deepseek" {
		t.Errorf("catalogue down: %+v", v)
	}
}
