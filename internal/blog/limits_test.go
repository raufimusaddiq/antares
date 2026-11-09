package blog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The author is told the limits before publishing (spec edge case), and what they are told is what is enforced.
func TestTheLimitsTheAuthorIsToldAreTheEnforcedOnes(t *testing.T) {
	a := newApp(t)
	got := into[Limits](t, a.active("alice").do("GET", "/api/blog/limits", nil))
	want := Limits{CheckBytes: maxCheckInput, DocBytes: maxDocBytes, ImagesPerPost: maxAssetsPerPost, ImageBytes: maxAssetBytes, BlocksPerCheck: maxBlocks, SourcesPerBlock: maxSourcesPer, ChecksPer24h: maxChecksPer24h}
	if got != want {
		t.Fatalf("limits: %+v, want %+v", got, want)
	}
	for name, v := range map[string]int{"checkBytes": got.CheckBytes, "docBytes": got.DocBytes, "imagesPerPost": got.ImagesPerPost, "imageBytes": got.ImageBytes, "blocksPerCheck": got.BlocksPerCheck, "sourcesPerBlock": got.SourcesPerBlock, "checksPer24h": got.ChecksPer24h} {
		if v <= 0 {
			t.Fatalf("%s is %d: a limit the author is told must be a real number", name, v)
		}
	}
	// Signed out, nothing is answered.
	if w := a.anon("GET", "/api/blog/limits"); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", w.Code)
	}
}

func textDoc(paragraphs int, each string) json.RawMessage {
	content := make([]any, 0, paragraphs)
	for i := 0; i < paragraphs; i++ {
		content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": each}}})
	}
	raw, _ := json.Marshal(map[string]any{"type": "doc", "content": content})
	return raw
}

// A draft is turned into the text the assistant reads; past the limit that is refused as itself, not as "invalid".
func TestADraftLongerThanACheckCanReadIsRefusedAsTooLong(t *testing.T) {
	para := strings.Repeat("word ", 200) // 1000 bytes
	if _, _, err := checkInput("T", textDoc(100, para)); err != nil {
		t.Fatalf("about 100 KB reads fine: %v", err)
	}
	if _, _, err := checkInput("T", textDoc(130, para)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("about 130 KB: %v, want ErrTooLong", err)
	}
	if _, _, err := checkInput("T", textDoc(0, para)); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrTooLong) {
		t.Fatalf("an empty draft is still plainly invalid: %v", err)
	}
}

func TestStartingACheckOnATooLongPostSaysSoAndSendsNothing(t *testing.T) {
	fh := newFake()
	a := newAppWithHermes(t, fh)
	u := a.active("alice")
	p := u.create()
	body := map[string]any{"baseVersion": p.LatestVersion, "title": "Long", "doc": textDoc(130, strings.Repeat("word ", 200))}
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, body); w.Code != http.StatusOK {
		t.Fatalf("a post this size saves: %d %s", w.Code, w.Body)
	}
	w := u.do("POST", "/api/blog/posts/"+p.ID+"/check", nil)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "TOO_LONG_TO_CHECK") {
		t.Fatalf("check on a too-long post: %d %s", w.Code, w.Body)
	}
	if s, r, _, _ := fh.snapshot(); s != 0 || len(r) != 0 {
		t.Fatal("something was sent to Hermes for a post too long to check")
	}
	// Publishing the original is still possible: the limit is the check's, not the post's.
	if w := u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "long-post", "summary": "s"}); w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}); w.Code != http.StatusOK {
		t.Fatalf("publishing the original of a long post: %d %s", w.Code, w.Body)
	}
}
