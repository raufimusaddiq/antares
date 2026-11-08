package blog

import (
	"strings"
	"testing"
)

// B can do nothing to A's post: every route answers exactly as it does for a post that does not exist.
func TestAnotherUsersPostIsA404(t *testing.T) {
	a := newApp(t)
	alice, bob := a.active("alice"), a.active("bob")
	p := alice.publish("Alice", "alice-post", "private-draft-text")
	asset := into[struct{ AssetID string }](t, alice.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()))
	missing := "00000000000000000000000000000000"

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/blog/posts/%s", nil},
		{"PUT", "/api/blog/posts/%s", map[string]any{"baseVersion": 1, "title": "hijack"}},
		{"PATCH", "/api/blog/posts/%s/settings", map[string]any{"slug": "hijacked"}},
		{"DELETE", "/api/blog/posts/%s", nil},
		{"POST", "/api/blog/posts/%s/publish", map[string]any{"mode": "original"}},
		{"POST", "/api/blog/posts/%s/unpublish", nil},
		{"POST", "/api/blog/posts/%s/duplicate", nil},
		{"POST", "/api/blog/posts/%s/assets", pngBytes()},
		{"GET", "/api/blog/posts/%s/assets/" + asset.AssetID, nil},
	}
	for _, r := range routes {
		foreign := bob.do(r.method, strings.Replace(r.path, "%s", p.ID, 1), r.body)
		absent := bob.do(r.method, strings.Replace(r.path, "%s", missing, 1), r.body)
		if foreign.Code != 404 || foreign.Code != absent.Code {
			t.Errorf("%s %s: foreign %d, missing %d", r.method, r.path, foreign.Code, absent.Code)
		}
		// Same body apart from the request id.
		strip := func(s string) string { i := strings.Index(s, "requestId"); return s[:i] }
		if strip(foreign.Body.String()) != strip(absent.Body.String()) {
			t.Errorf("%s %s tells a foreign id from a missing one:\n%s\n%s", r.method, r.path, foreign.Body, absent.Body)
		}
	}
	// A cover and an image from A's post are not B's to use.
	bp := bob.create()
	if w := bob.do("PATCH", "/api/blog/posts/"+bp.ID+"/settings", map[string]any{"coverAssetId": asset.AssetID}); w.Code != 422 {
		t.Errorf("A's image as B's cover: %d", w.Code)
	}
	// Nothing B did changed A's post.
	got := into[Post](t, alice.do("GET", "/api/blog/posts/"+p.ID, nil))
	if got.Title != "Alice" || got.Slug != "alice-post" || got.Status != "published" {
		t.Fatalf("A's post was changed: %+v", got)
	}
	if l := into[[]Summary](t, bob.do("GET", "/api/blog/posts", nil)); len(l) != 1 || l[0].ID != bp.ID {
		t.Fatalf("B's list: %+v", l)
	}
	// A malformed id is the same 404.
	if w := bob.do("GET", "/api/blog/posts/not-an-id", nil); w.Code != 404 {
		t.Fatalf("malformed id: %d", w.Code)
	}
}
