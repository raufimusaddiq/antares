package chat

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/store"
)

var (
	audioHdr = map[string]string{"Content-Type": "audio/webm;codecs=opus"}
	webmBody = "\x1a\x45\xdf\xa3 alice-voice-bytes"
)

func assetRows(t *testing.T, a *app) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAssetOwnerOnly(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	w := alice.do("POST", "/api/assets", audioHdr, webmBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody[struct{ ID string }](t, w).ID
	if got := alice.get("/api/assets/" + id); got.Code != 200 || got.Body.String() != webmBody || got.Header().Get("Content-Type") != "audio/webm" {
		t.Fatalf("owner read: %d %q %q", got.Code, got.Body.String(), got.Header().Get("Content-Type"))
	}
	missing := bob.get("/api/assets/" + store.NewID())
	for _, m := range []string{"GET", "DELETE"} {
		got := bob.do(m, "/api/assets/"+id, nil, nil)
		if got.Code != 404 || !sameAnswer(got, bob.do(m, "/api/assets/"+store.NewID(), nil, nil)) || strings.Contains(got.Body.String(), "alice-voice") {
			t.Fatalf("B %s on A's asset: %d %s (unknown id gives %d)", m, got.Code, got.Body.String(), missing.Code)
		}
	}
	if alice.get("/api/assets/"+id).Code != 200 {
		t.Fatal("B's delete removed A's asset")
	}
	if alice.do("DELETE", "/api/assets/"+id, nil, nil).Code != 204 || alice.get("/api/assets/"+id).Code != 404 {
		t.Fatal("owner delete did not remove the asset")
	}
	if n := len(a.hx.requests()); n != 0 {
		t.Fatalf("staging a recording sent %d requests to Hermes", n)
	}
}

// A refused upload says why and leaves no row behind.
func TestRejectedUploadLeavesNothing(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	big := "\x1a\x45\xdf\xa3" + strings.Repeat("x", maxAudioBytes)
	cases := []struct {
		name, ctype, body string
		status            int
		code              string
	}{
		{"not audio", "text/html", "<script>", 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"no type", "", webmBody, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"empty", "audio/webm", "", 400, "INVALID_REQUEST"},
		{"mislabelled", "audio/webm", "<html>not audio</html>", 400, "INVALID_REQUEST"},
		{"wav that is webm", "audio/wav", webmBody, 400, "INVALID_REQUEST"},
		{"too large", "audio/webm", big, 413, "PAYLOAD_TOO_LARGE"},
	}
	for _, c := range cases {
		w := alice.do("POST", "/api/assets", map[string]string{"Content-Type": c.ctype}, c.body)
		if w.Code != c.status || errCode(t, w) != c.code || !strings.Contains(w.Body.String(), `"message"`) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if n := assetRows(t, a); n != 0 {
		t.Fatalf("%d asset rows left by refused uploads", n)
	}
}

func TestUploadQuotaRefusesWithReasonAndKeepsExisting(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	chunk := "\x1a\x45\xdf\xa3" + strings.Repeat("x", maxAudioBytes-4)
	for i := 0; i < maxUserAudioBytes/maxAudioBytes; i++ {
		if w := alice.do("POST", "/api/assets", map[string]string{"Content-Type": "audio/webm"}, chunk); w.Code != 201 {
			t.Fatalf("upload %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	before := assetRows(t, a)
	w := alice.do("POST", "/api/assets", audioHdr, webmBody)
	if w.Code != 413 || errCode(t, w) != "STORAGE_QUOTA" || !strings.Contains(w.Body.String(), "delete some first") {
		t.Fatalf("over quota: %d %s", w.Code, w.Body.String())
	}
	if assetRows(t, a) != before {
		t.Fatal("refused upload left a row")
	}
	if w := bob.do("POST", "/api/assets", audioHdr, webmBody); w.Code != 201 {
		t.Fatalf("A's usage counted against B: %d", w.Code)
	}
}

// pngBytes returns a real w×h PNG.
func pngBytes(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// MEDIA-EXT-001/003: an image is validated by decoding its header, held per owner, and
// never reachable by another user; nothing is sent to Hermes.
func TestImageStagingOwnerOnlyAndValidated(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bobby")
	img := pngBytes(t, 4, 3)
	w := alice.do("POST", "/api/assets", map[string]string{"Content-Type": "image/png"}, img)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody[struct{ ID string }](t, w).ID
	got := alice.get("/api/assets/" + id)
	if got.Code != 200 || got.Body.String() != img || got.Header().Get("Content-Type") != "image/png" ||
		got.Header().Get("Cache-Control") != "private, no-store" || got.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("owner read: %d %v", got.Code, got.Header())
	}
	for _, m := range []string{"GET", "DELETE"} {
		g := bob.do(m, "/api/assets/"+id, nil, nil)
		if g.Code != 404 || !sameAnswer(g, bob.do(m, "/api/assets/"+store.NewID(), nil, nil)) {
			t.Fatalf("B %s on A's image: %d %s", m, g.Code, g.Body.String())
		}
	}
	if alice.get("/api/assets/"+id).Code != 200 {
		t.Fatal("B's delete removed A's image")
	}
	if n := len(a.hx.requests()); n != 0 {
		t.Fatalf("staging an image sent %d requests to Hermes", n)
	}
}

func TestImageUploadRefusals(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	cases := []struct {
		name, ctype, body string
		status            int
	}{
		{"html labelled png", "image/png", "<script>alert(1)</script>", 400},
		{"png labelled jpeg", "image/jpeg", pngBytes(t, 2, 2), 400},
		{"svg", "image/svg+xml", "<svg/>", 415},
		{"webp", "image/webp", "RIFF....WEBP", 415},
		{"over pixel budget", "image/png", pngBytes(t, 6000, 5000), 413},
		{"over byte budget", "image/png", pngBytes(t, 2, 2) + strings.Repeat("x", maxImageBytes), 413},
	}
	for _, c := range cases {
		w := alice.do("POST", "/api/assets", map[string]string{"Content-Type": c.ctype}, c.body)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if n := assetRows(t, a); n != 0 {
		t.Fatalf("%d rows left by refused uploads", n)
	}
}
