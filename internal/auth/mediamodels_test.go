package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/raufimusaddiq/antares/internal/kenari"
)

type fakeCatalogue struct {
	models []kenari.Model
	err    error
}

func (f fakeCatalogue) List(context.Context) ([]kenari.Model, error) { return f.models, f.err }

func row(id string, endpoint string, in ...string) kenari.Model {
	return kenari.Model{ID: id, Name: id, Endpoints: []string{endpoint}, HasModalities: true, Modalities: kenari.Modalities{Input: in}}
}

// Admin → Assistant media: only an admin reads or sets the default models, a model must be one Kenari serves for that
// kind and takes a text prompt, the change is audited, and every ready account goes back through verification.
func TestMediaModelsAreAdminOnlyFromTheCatalogueAndReverifyReadyAccounts(t *testing.T) {
	h := newHarness(t)
	h.a.catalogue = fakeCatalogue{models: []kenari.Model{
		row("gpt-image-2", "images", "text", "image"), row("edit-only", "images", "image"),
		row("veo-3.1-lite", "videos", "text", "image"), row("clip-only", "videos", "image", "video"), row("chat-model", "chat", "text"),
	}}
	admin := h.userIn("adm", "APPROVED", "READY", "ADMIN", true)
	user := h.userIn("usr", "APPROVED", "READY", "USER", true)
	body := `{"image":"gpt-image-2","video":"veo-3.1-lite"}`
	for name, b := range map[string]*browser{"ordinary user": user, "anonymous": h.browser()} {
		if w := b.send("PUT", "/api/admin/media-models", body, sameOriginHdr); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s PUT = %d", name, w.Code)
		}
		if w := b.get("/api/admin/media-models"); w.Code != 403 && w.Code != 401 {
			t.Errorf("%s GET = %d", name, w.Code)
		}
	}
	for name, bad := range map[string]string{"missing video": `{"image":"gpt-image-2"}`, "extra field": `{"image":"","video":"","userId":"x"}`,
		"a chat model": `{"image":"chat-model","video":""}`, "needs an image input": `{"image":"edit-only","video":""}`,
		"a video model as image": `{"image":"veo-3.1-lite","video":""}`, "no text prompt": `{"image":"","video":"clip-only"}`,
		"unknown": `{"image":"nope","video":""}`, "shell": `{"image":"x;rm -rf","video":""}`} {
		if w := admin.send("PUT", "/api/admin/media-models", bad, sameOriginHdr); w.Code != 400 {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if n := h.count(`SELECT COUNT(*) FROM tenant_tool_policy`); n != 0 {
		t.Fatal("a refused call stored a row")
	}
	var v mediaModelsView
	w := admin.get("/api/admin/media-models")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Catalogue != "ok" || len(v.Image.Options) != 1 || len(v.Video.Options) != 1 ||
		v.Image.Current != "" || v.Image.Default == "" || v.Video.Default == "" {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	w = admin.send("PUT", "/api/admin/media-models", body, sameOriginHdr)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Image.Current != "gpt-image-2" || v.Video.Current != "veo-3.1-lite" || v.Reverifying == nil || *v.Reverifying != 2 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	if n := h.count(`SELECT COUNT(*) FROM users WHERE tenant_status = 'CONFIGURING'`); n != 2 {
		t.Errorf("%d accounts re-verifying, want 2", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'media_models.set'`); n != 1 {
		t.Errorf("%d audit rows, want 1", n)
	}
	// Setting only models leaves the tool policy on its default, and a later tool policy keeps the models.
	var tp struct{ Custom bool }
	if w := admin.get("/api/admin/tool-policy"); json.Unmarshal(w.Body.Bytes(), &tp) != nil || tp.Custom {
		t.Errorf("saving models made a custom tool policy: %s", w.Body.String())
	}
	h.db.Exec(`UPDATE users SET tenant_status = 'READY'`)
	h.db.Exec(`UPDATE tenant_bindings SET status = 'READY'`) // the re-verification the first save started has finished
	if w := admin.send("PUT", "/api/admin/tool-policy", `{"allowed":["web"]}`, sameOriginHdr); w.Code != 200 {
		t.Fatalf("tool policy PUT = %d %s", w.Code, w.Body.String())
	}
	h.db.Exec(`UPDATE users SET tenant_status = 'READY'`)
	h.db.Exec(`UPDATE tenant_bindings SET status = 'READY'`)
	if w := admin.get("/api/admin/media-models"); json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Image.Current != "gpt-image-2" {
		t.Errorf("a tool policy save dropped the models: %s", w.Body.String())
	}
	// With Kenari unreadable the lists are empty and said so; the saved choice may stay, a new one may not, and the default can be restored.
	h.a.catalogue = fakeCatalogue{err: errors.New("down")}
	if w := admin.get("/api/admin/media-models"); json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Catalogue != "unavailable" || len(v.Image.Options) != 0 {
		t.Errorf("unreadable catalogue GET: %s", w.Body.String())
	}
	if w := admin.send("PUT", "/api/admin/media-models", `{"image":"gpt-image-2","video":"other"}`, sameOriginHdr); w.Code != 400 {
		t.Errorf("a new model with Kenari down = %d", w.Code)
	}
	if w := admin.send("PUT", "/api/admin/media-models", `{"image":"","video":""}`, sameOriginHdr); w.Code != 200 {
		t.Errorf("restoring the defaults = %d", w.Code)
	}
}
