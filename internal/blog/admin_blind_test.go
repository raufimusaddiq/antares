package blog

import (
	"strings"
	"testing"
)

// An admin is one more signed-in account to the blog (T070, security-invariants §6): another author's draft, version, image
// and check are as absent to them as to anyone, and a suspended author's published post is gone for every reader. Being an
// admin gets no way in.
func TestAnAdminCannotReachAnotherAuthorsPostsAndASuspendedAuthorsAreHidden(t *testing.T) {
	a := newAppWithHermes(t, newFake()) // with an assistant, so the check routes reach their ownership test
	author, admin := a.active("alice"), a.active("root")
	if _, err := a.db.Exec(`UPDATE users SET role = 'ADMIN' WHERE id = ?`, admin.id); err != nil {
		t.Fatal(err)
	}
	pub := author.publish("Alice published", "alice-published", "published-body-text")
	draft := author.create()
	if w := author.do("PUT", "/api/blog/posts/"+draft.ID, map[string]any{"baseVersion": draft.LatestVersion, "title": "Alice private draft", "doc": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "private-draft-text"}}}}}}); w.Code != 200 {
		t.Fatalf("save draft: %d %s", w.Code, w.Body)
	}
	asset := into[struct{ AssetID string }](t, author.do("POST", "/api/blog/posts/"+draft.ID+"/assets", pngBytes()))
	missing := "00000000000000000000000000000000"

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/blog/posts/%s", nil},
		{"PUT", "/api/blog/posts/%s", map[string]any{"baseVersion": 1, "title": "admin was here"}},
		{"PATCH", "/api/blog/posts/%s/settings", map[string]any{"slug": "admin-slug"}},
		{"DELETE", "/api/blog/posts/%s", nil},
		{"POST", "/api/blog/posts/%s/publish", map[string]any{"mode": "original"}},
		{"POST", "/api/blog/posts/%s/unpublish", nil},
		{"POST", "/api/blog/posts/%s/duplicate", nil},
		{"POST", "/api/blog/posts/%s/check", nil},
		{"GET", "/api/blog/posts/%s/check", nil},
		{"POST", "/api/blog/posts/%s/check/cancel", nil},
		{"PUT", "/api/blog/posts/%s/blocks/" + missing, map[string]any{"decision": "accepted"}},
		{"POST", "/api/blog/posts/%s/assets", pngBytes()},
		{"GET", "/api/blog/posts/%s/assets/" + asset.AssetID, nil},
	}
	strip := func(s string) string { i := strings.Index(s, "requestId"); return s[:max(i, 0)] }
	for _, id := range []string{draft.ID, pub.ID} {
		for _, r := range routes {
			foreign := admin.do(r.method, strings.Replace(r.path, "%s", id, 1), r.body)
			absent := admin.do(r.method, strings.Replace(r.path, "%s", missing, 1), r.body)
			if absent.Code != 404 || foreign.Code != 404 {
				t.Errorf("admin %s %s: foreign %d, missing %d", r.method, r.path, foreign.Code, absent.Code)
			}
			if strip(foreign.Body.String()) != strip(absent.Body.String()) {
				t.Errorf("admin %s %s tells a foreign id from a missing one:\n%s\n%s", r.method, r.path, foreign.Body, absent.Body)
			}
		}
	}
	// The admin's own list has only their own posts, and nothing the admin did changed the author's.
	if l := into[[]Summary](t, admin.do("GET", "/api/blog/posts", nil)); len(l) != 0 {
		t.Fatalf("the admin's list holds someone else's posts: %+v", l)
	}
	if got := into[Post](t, author.do("GET", "/api/blog/posts/"+draft.ID, nil)); got.Title != "Alice private draft" {
		t.Fatalf("the author's draft was changed: %+v", got)
	}

	// Readers, the admin among them, see a draft nowhere, and a published post only while its author is approved.
	if w := a.anon("GET", "/api/public/blog/posts/alice-published"); w.Code != 200 {
		t.Fatalf("the published post is readable: %d", w.Code)
	}
	for _, c := range []*person{admin, author} {
		if w := c.do("GET", "/api/public/blog/posts/alice-published", nil); w.Code != 200 {
			t.Fatalf("signed in, the published post is readable: %d", w.Code)
		}
	}
	if _, err := a.db.Exec(`UPDATE users SET approval_status = 'SUSPENDED' WHERE id = ?`, author.id); err != nil {
		t.Fatal(err)
	}
	for who, get := range map[string]func(string) int{
		"a reader":  func(p string) int { return a.anon("GET", p).Code },
		"the admin": func(p string) int { return admin.do("GET", p, nil).Code },
	} {
		if c := get("/api/public/blog/posts/alice-published"); c != 404 {
			t.Errorf("%s reads a suspended author's published post: %d", who, c)
		}
	}
	if body := a.anon("GET", "/api/public/blog/posts").Body.String(); strings.Contains(body, "alice-published") || strings.Contains(body, "Alice published") {
		t.Errorf("a suspended author's post is in the public index: %s", body)
	}
}
