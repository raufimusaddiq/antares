package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/kenari"
)

// Image input and previews (issue #101, PRD §27.3 MEDIA-EXT-001/002/003).
//
// Model capabilities come from the Kenari catalogue: ../kenari/testdata/models.json is a
// trimmed copy of the real https://kenari.id/v1/models answer. Hermes' model inventory is a
// fake that only names which model the profile runs.

const (
	visionModel   = "deepseek-v4-1-flash" // Kenari: input text,image,file
	textOnlyModel = "beam-1-0:free"       // Kenari: input text,file
)

func sceneFixture(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/scene-320x240.png")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSceneFixtureIsARealImage(t *testing.T) {
	b := sceneFixture(t)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "png" || cfg.Width != 320 || cfg.Height != 240 {
		t.Fatalf("fixture: %v %q %+v", err, format, cfg)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.At(10, 10) == img.At(10, 230) { // sky and ground differ: a scene, not a flat fill
		t.Error("fixture is not a varied picture")
	}
}

// mediaApp is an app whose Hermes profile default model is def and whose catalogue is the
// Kenari fixture served over HTTP.
func mediaApp(t *testing.T, def string) *app {
	t.Helper()
	a := newModelsApp(t)
	body, err := os.ReadFile("../kenari/testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("catalogue request carried a credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	a.svc.kenari = kenari.New(srv.URL, nil)
	a.setDefaultModel(def)
	return a
}

// setDefaultModel makes every profile's Hermes inventory list the three models and default to def.
func (a *app) setDefaultModel(def string) {
	a.hx.mu.Lock()
	defer a.hx.mu.Unlock()
	body := `{"model":"` + def + `","provider":"custom","providers":[{"slug":"custom","name":"Custom","is_current":true,
	  "models":["` + visionModel + `","` + textOnlyModel + `","not-in-kenari"],
	  "capabilities":{"` + visionModel + `":{"fast":false,"reasoning":false},"` + textOnlyModel + `":{"fast":false,"reasoning":false},"not-in-kenari":{"fast":false,"reasoning":false}}}]}`
	a.hx.canned["/api/model/options"] = body
}

func (p *person) stagePNG(b []byte) string {
	p.a.t.Helper()
	w := p.do("POST", "/api/assets", map[string]string{"Content-Type": "image/png"}, string(b))
	if w.Code != http.StatusCreated {
		p.a.t.Fatalf("stage image: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[struct{ ID string }](p.a.t, w).ID
}

func (p *person) sendImages(chatID, text string, ids ...string) *httptest.ResponseRecorder {
	return p.do("POST", "/api/chats/"+chatID+"/messages", map[string]string{"Idempotency-Key": nextIdem()},
		map[string]any{"input": text, "imageAssetIds": ids})
}

// runPosts returns the decoded bodies of the POST /v1/runs requests Hermes received.
func (a *app) runPosts(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range a.hx.requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/v1/runs") {
			var b map[string]any
			if err := json.Unmarshal([]byte(r.Body), &b); err != nil {
				t.Fatal(err)
			}
			out = append(out, b)
		}
	}
	return out
}

func runImageRows(t *testing.T, a *app) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM run_images`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// MEDIA-EXT-002: a vision model per Kenari receives the owner's real image inline, and the model
// named to Hermes is the model that was checked.
func TestImageSendUsesCheckedModelAndInlineBytes(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	chat := alice.newChat()
	png := sceneFixture(t)
	id := alice.stagePNG(png)

	in := decodeBody[struct {
		Image struct {
			Available bool
			Model     string
			Source    string
			Reason    *string
		}
	}](t, alice.get("/api/chats/"+chat+"/input"))
	if !in.Image.Available || in.Image.Model != visionModel || in.Image.Source != "kenari" || in.Image.Reason != nil {
		t.Fatalf("input capability: %+v", in.Image)
	}

	w := alice.sendImages(chat, "what is in this picture?", id)
	if w.Code != 202 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var posts []map[string]any
	eventually(t, func() bool { posts = a.runPosts(t); return len(posts) == 1 }, "run to reach Hermes")
	p := posts[0]
	if p["model"] != visionModel || p["provider"] != "custom" {
		t.Errorf("run named %v/%v, checked %s/custom", p["provider"], p["model"], visionModel)
	}
	msgs, _ := p["input"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("input = %v", p["input"])
	}
	m := msgs[0].(map[string]any)
	parts := m["content"].([]any)
	if m["role"] != "user" || len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
	if t0 := parts[0].(map[string]any); t0["type"] != "text" || t0["text"] != "what is in this picture?" {
		t.Errorf("text part = %v", t0)
	}
	url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(raw, png) || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("image part is not the owner's fixture bytes (err %v)", err)
	}
	if strings.Contains(a.hx.requests()[len(a.hx.requests())-1].Body, alice.key) {
		t.Error("a credential reached a request body")
	}
	if runImageRows(t, a) != 1 {
		t.Error("the run's image was not recorded")
	}
}

// A chat whose saved choice differs from the profile default is checked, and run, as that choice.
func TestImageSendChecksTheChatsChosenModelNotTheDefault(t *testing.T) {
	a := mediaApp(t, textOnlyModel) // default cannot see...
	alice := a.user("alice")
	chat := alice.newChat()
	id := alice.stagePNG(sceneFixture(t))
	if w := alice.sendImages(chat, "look", id); w.Code != 422 || errCode(t, w) != codeImageUnavailable {
		t.Fatalf("default text-only model: %d %s", w.Code, w.Body.String())
	}
	if c, _ := alice.putModel(chat, map[string]any{"provider": "custom", "model": visionModel}); c != 200 { // ...the chat's choice can
		t.Fatalf("choose model: %d", c)
	}
	if w := alice.sendImages(chat, "look", id); w.Code != 202 {
		t.Fatalf("chosen vision model: %d %s", w.Code, w.Body.String())
	}
	var posts []map[string]any
	eventually(t, func() bool { posts = a.runPosts(t); return len(posts) == 1 }, "run to reach Hermes")
	if posts[0]["model"] != visionModel {
		t.Errorf("ran %v, user chose %s", posts[0]["model"], visionModel)
	}
}

// MEDIA-EXT-002: never silently discard. Every case that cannot be confirmed is refused with
// the stable code, nothing is sent to Hermes and nothing is recorded.
func TestImageSendRefusedWhenVisionIsNotConfirmed(t *testing.T) {
	cases := []struct {
		name   string
		def    string
		mutate func(*app)
		reason string
	}{
		{"text-only per Kenari", textOnlyModel, nil, reasonTextOnly},
		{"model absent from Kenari", "not-in-kenari", nil, reasonNotListed},
		{"no catalogue configured", visionModel, func(a *app) { a.svc.kenari = nil }, reasonNoCatalogue},
		{"catalogue unreachable", visionModel, func(a *app) { a.svc.kenari = kenari.New("http://127.0.0.1:1/v1/models", nil) }, reasonCatalogueDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := mediaApp(t, c.def)
			if c.mutate != nil {
				c.mutate(a)
			}
			alice := a.user("alice")
			chat := alice.newChat()
			id := alice.stagePNG(sceneFixture(t))
			in := decodeBody[struct {
				Image struct {
					Available bool
					Reason    string
				}
			}](t, alice.get("/api/chats/"+chat+"/input"))
			if in.Image.Available || in.Image.Reason != c.reason {
				t.Errorf("capability = %+v, want %s", in.Image, c.reason)
			}
			w := alice.sendImages(chat, "look", id)
			if w.Code != 422 || errCode(t, w) != codeImageUnavailable {
				t.Fatalf("send: %d %s", w.Code, w.Body.String())
			}
			if n := len(a.runPosts(t)); n != 0 || runImageRows(t, a) != 0 {
				t.Errorf("%d runs reached Hermes, %d image rows", n, runImageRows(t, a))
			}
			// The same chat still takes plain text.
			if w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()}, map[string]any{"input": "hello"}); w.Code != 202 {
				t.Errorf("text send: %d", w.Code)
			}
		})
	}
}

// Absent Hermes inventory modalities is not absent vision: the fake inventory says nothing about
// modalities for any model, and the vision model is still accepted (covered above); the inventory
// does not decide even when it lists a model Kenari calls text-only.
func TestHermesInventoryDoesNotDecideVision(t *testing.T) {
	a := mediaApp(t, textOnlyModel)
	alice := a.user("alice")
	chat := alice.newChat()
	in := decodeBody[struct{ Image struct{ Available bool } }](t, alice.get("/api/chats/"+chat+"/input"))
	if in.Image.Available {
		t.Error("a model Kenari lists as text-only was offered image input")
	}
}

// MEDIA-EXT-003 / ownership: B cannot send, preview or delete A's image, and learns nothing.
func TestImageCrossOwnerDenied(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice, bob := a.user("alice"), a.user("bobby")
	id := alice.stagePNG(sceneFixture(t))
	bchat := bob.newChat()

	foreign := bob.sendImages(bchat, "steal", id)
	unknown := bob.sendImages(bchat, "steal", "00000000000000000000000000000000")
	if foreign.Code != 404 || !sameAnswer(foreign, unknown) {
		t.Fatalf("B sending A's image: %d %s (unknown id: %d)", foreign.Code, foreign.Body.String(), unknown.Code)
	}
	if n := len(a.runPosts(t)); n != 0 || runImageRows(t, a) != 0 {
		t.Fatalf("a refused send reached Hermes (%d) or recorded images (%d)", n, runImageRows(t, a))
	}
	for _, path := range []string{"/api/assets/" + id, "/api/assets/" + id + "?inline=1"} {
		if w := bob.get(path); w.Code != 404 || bytes.Contains(w.Body.Bytes(), []byte("PNG")) {
			t.Errorf("B GET %s: %d", path, w.Code)
		}
	}
	if w := bob.do("DELETE", "/api/assets/"+id, nil, nil); w.Code != 404 {
		t.Errorf("B DELETE: %d", w.Code)
	}
	if w := alice.get("/api/assets/" + id + "?inline=1"); w.Code != 200 {
		t.Errorf("owner lost access: %d", w.Code)
	}
}

func TestImageSendValidation(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	chat := alice.newChat()
	id := alice.stagePNG(sceneFixture(t))
	audio := decodeBody[struct{ ID string }](t, alice.do("POST", "/api/assets", audioHdr, webmBody)).ID
	var ids []string
	for i := 0; i < maxImagesPerTurn+1; i++ {
		ids = append(ids, alice.stagePNG(sceneFixture(t)))
	}
	for name, c := range map[string]struct {
		ids  []string
		code int
	}{
		"duplicate": {[]string{id, id}, 400},
		"too many":  {ids, 400},
		"not an id": {[]string{"../../etc/passwd"}, 400},
		"audio id":  {[]string{audio}, 404}, // an owned asset that is not an image is not an image input
	} {
		if w := alice.sendImages(chat, "x", c.ids...); w.Code != c.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// A body cannot name the model, provider or profile.
	w := alice.do("POST", "/api/chats/"+chat+"/messages", map[string]string{"Idempotency-Key": nextIdem()},
		map[string]any{"input": "x", "imageAssetIds": []string{id}, "model": "other", "profile": "u_bobbyprofile"})
	if w.Code != 400 {
		t.Errorf("extra fields: %d", w.Code)
	}
	if n := len(a.runPosts(t)); n != 0 {
		t.Errorf("%d refused sends reached Hermes", n)
	}
}

// The per-turn byte budget keeps the encoded body under Hermes' 10 MB request cap.
func TestImageTurnOverBudgetRefused(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	chat := alice.newChat()
	rng := rand.New(rand.NewSource(1))
	im := image.NewNRGBA(image.Rect(0, 0, 1500, 1500))
	for i := range im.Pix {
		im.Pix[i] = byte(rng.Intn(256))
	}
	for i := 3; i < len(im.Pix); i += 4 {
		im.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil || buf.Len() <= maxTurnImageBytes || buf.Len() > maxImageBytes {
		t.Fatalf("test image %d bytes: %v", buf.Len(), err)
	}
	id := alice.stagePNG(buf.Bytes())
	if w := alice.sendImages(chat, "big", id); w.Code != 413 || errCode(t, w) != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(a.runPosts(t)) != 0 {
		t.Error("an over-budget turn reached Hermes")
	}
}

// MEDIA-EXT-001/003/007: safe inline preview of owned media, download by default, Range for audio.
func TestAssetPreviewHeadersAndRange(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	png := sceneFixture(t)
	id := alice.stagePNG(png)

	dl := alice.get("/api/assets/" + id)
	if dl.Code != 200 || dl.Header().Get("Content-Disposition") != "attachment" || !bytes.Equal(dl.Body.Bytes(), png) {
		t.Fatalf("download: %d %q", dl.Code, dl.Header().Get("Content-Disposition"))
	}
	pv := alice.get("/api/assets/" + id + "?inline=1")
	h := pv.Header()
	if pv.Code != 200 || h.Get("Content-Disposition") != "inline" || h.Get("Content-Type") != "image/png" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(pv.Body.Bytes(), png) {
		t.Fatalf("preview: %d %v", pv.Code, h)
	}

	aud := decodeBody[struct{ ID string }](t, alice.do("POST", "/api/assets", audioHdr, webmBody)).ID
	r := alice.newRequest("GET", "/api/assets/"+aud+"?inline=1", map[string]string{"Range": "bytes=0-3"}, nil)
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != webmBody[:4] || w.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("range: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}

// finishAccepted waits for Hermes to accept the run, then drives it to completed.
func finishAccepted(t *testing.T, a *app, p *person, runID string) {
	t.Helper()
	var hid string
	eventually(t, func() bool {
		_ = a.db.QueryRow(`SELECT COALESCE(hermes_run_id,'') FROM runs WHERE id = ? AND user_id = ?`, runID, p.id).Scan(&hid)
		return hid != ""
	}, "Hermes to accept the run")
	fr := a.hx.run(hid)
	s := p.open(runID, "")
	recv(t, fr.opens, "stream")
	a.hx.push(fr, "run.completed", `{}`)
	s.waitFor(t, "run.completed")
}

func (p *person) sendOK(chat, text string, ids ...string) string {
	p.a.t.Helper()
	w := p.sendImages(chat, text, ids...)
	if w.Code != 202 {
		p.a.t.Fatalf("send %q: %d %s", text, w.Code, w.Body.String())
	}
	return decodeBody[struct{ RunID string }](p.a.t, w).RunID
}

func setTranscript(a *app, p *person, chat string, msgs ...map[string]any) {
	var sid string
	if err := a.db.QueryRow(`SELECT hermes_session_id FROM chats WHERE id = ? AND user_id = ?`, chat, p.id).Scan(&sid); err != nil {
		a.t.Fatal(err)
	}
	a.hx.mu.Lock()
	a.hx.msgs[sid] = msgs
	a.hx.mu.Unlock()
}

func um(id, text string) map[string]any {
	return map[string]any{"id": id, "role": "user", "content": text}
}
func am(id string) map[string]any {
	return map[string]any{"id": id, "role": "assistant", "content": "ok"}
}

type histBody struct {
	Messages []struct {
		Role, Text  string
		Images      int
		Attachments []struct {
			AssetID, MediaType string
			Available          bool
		}
	}
}

func (p *person) history(chat string) histBody {
	return decodeBody[histBody](p.a.t, p.get("/api/chats/"+chat+"/messages"))
}

// A thumbnail is attached only to a transcript turn that verifies: the run's position among the chat's
// Hermes-accepted runs, its own text, and its image placeholders. Deletion is reported, not hidden.
func TestHistoryLinksOnlyAVerifiedTurn(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice, bob := a.user("alice"), a.user("bobby")
	chat := alice.newChat()
	id := alice.stagePNG(sceneFixture(t))
	finishAccepted(t, a, alice, alice.sendOK(chat, "what is this?", id))
	setTranscript(a, alice, chat, um("m1", "what is this?\n[image]"), am("m2"))

	h := alice.history(chat)
	if at := h.Messages[0].Attachments; h.Messages[0].Images != 1 || len(at) != 1 || at[0].AssetID != id || !at[0].Available || at[0].MediaType != "image/png" {
		t.Fatalf("history: %+v", h.Messages[0])
	}
	if len(h.Messages[1].Attachments) != 0 {
		t.Error("an assistant turn got attachments")
	}
	if alice.do("DELETE", "/api/assets/"+id, nil, nil).Code != 204 {
		t.Fatal("delete")
	}
	if at := alice.history(chat).Messages[0].Attachments; len(at) != 1 || at[0].Available {
		t.Errorf("deleted asset still reported available: %+v", at)
	}
	if bob.get("/api/chats/"+chat+"/messages").Code != 404 {
		t.Error("B read A's history")
	}
}

// Regression (review round 1, BLOCKER): image B sent in a run that Hermes never accepted must not take
// image A's turn, even though both carry one image.
func TestFailedAdmissionDoesNotStealAnEarlierTurnsImages(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	chat := alice.newChat()
	idA, idB := alice.stagePNG(sceneFixture(t)), alice.stagePNG(sceneFixture(t))
	finishAccepted(t, a, alice, alice.sendOK(chat, "first look", idA))

	a.hx.mu.Lock()
	a.hx.failNext["POST /v1/runs"] = 500 // Hermes refuses the second run: it fails before admission
	a.hx.mu.Unlock()
	failed := alice.sendOK(chat, "second look", idB)
	eventually(t, func() bool { return alice.runStatus(failed) == "FAILED" }, "the second run to fail")
	if runImageRows(t, a) != 2 {
		t.Fatalf("expected both sends recorded, got %d rows", runImageRows(t, a))
	}
	setTranscript(a, alice, chat, um("m1", "first look\n[image]"), am("m2"))

	h := alice.history(chat)
	if at := h.Messages[0].Attachments; len(at) != 1 || at[0].AssetID != idA {
		t.Fatalf("turn 1 attachments = %+v, want only image A", at)
	}
}

// Regression: literal "[image]" text, in a later plain message or in the run's own text, links nothing.
func TestLiteralImageMarkersLinkNothing(t *testing.T) {
	a := mediaApp(t, visionModel)
	alice := a.user("alice")
	chat := alice.newChat()
	id := alice.stagePNG(sceneFixture(t))
	finishAccepted(t, a, alice, alice.sendOK(chat, "see [image] here", id))
	finishAccepted(t, a, alice, alice.sendOK(chat, "I typed [image] myself"))
	setTranscript(a, alice, chat,
		um("m1", "see [image] here\n[image]"), am("m2"),
		um("m3", "I typed [image] myself"), am("m4"))

	h := alice.history(chat)
	if len(h.Messages[0].Attachments) != 1 || h.Messages[0].Attachments[0].AssetID != id {
		t.Fatalf("the image turn lost its thumbnail: %+v", h.Messages[0])
	}
	if len(h.Messages[2].Attachments) != 0 || h.Messages[2].Images != 0 {
		t.Errorf("a literal [image] in plain text took an image set: %+v", h.Messages[2])
	}
}

// A transcript that no longer lines up (rewritten text, an extra turn, wrong placeholder count) is left unlinked.
func TestUncertainTurnsStayUnlinked(t *testing.T) {
	for name, msgs := range map[string][]map[string]any{
		"text rewritten":      {um("m1", "something else\n[image]"), am("m2")},
		"extra turn before":   {um("m0", "steered"), am("m0a"), um("m1", "look\n[image]"), am("m2")},
		"placeholder count":   {um("m1", "look\n[image]\n[image]"), am("m2")},
		"no placeholder":      {um("m1", "look"), am("m2")},
		"transcript is empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			a := mediaApp(t, visionModel)
			alice := a.user("alice")
			chat := alice.newChat()
			finishAccepted(t, a, alice, alice.sendOK(chat, "look", alice.stagePNG(sceneFixture(t))))
			setTranscript(a, alice, chat, msgs...)
			for i, m := range alice.history(chat).Messages {
				if len(m.Attachments) != 0 {
					t.Errorf("message %d was linked: %+v", i, m)
				}
			}
		})
	}
}
