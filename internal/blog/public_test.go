package blog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicShowsOnlyPublishedPostsOfApprovedAuthors(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	pub := u.publish("Live post", "live-post", "the live text")
	draft := u.create()
	u.do("PUT", "/api/blog/posts/"+draft.ID, map[string]any{"baseVersion": 1, "title": "Secret draft", "doc": body("draft-only-text")})
	un := u.publish("Taken down", "taken-down", "taken text")
	u.do("POST", "/api/blog/posts/"+un.ID+"/unpublish", nil)

	r := a.anon("GET", "/api/public/blog/posts/live-post")
	if r.Code != 200 {
		t.Fatalf("live: %d %s", r.Code, r.Body)
	}
	var got map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if got["title"] != "Live post" || got["authorName"] != "Author alice" || got["publishedAt"] == "" {
		t.Fatalf("post: %v", got)
	}
	// Nothing identifying the account, the profile or the post's internal ids.
	for _, bad := range []string{pub.ID, u.id, "@example.test", "alice@", "u_alice", "profile", "tenant", "authorId", "author_id"} {
		if strings.Contains(r.Body.String(), bad) {
			t.Errorf("the public post contains %q: %s", bad, r.Body)
		}
	}
	list := a.anon("GET", "/api/public/blog/posts")
	if list.Code != 200 || !strings.Contains(list.Body.String(), "live-post") {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	for _, bad := range []string{"taken-down", "Secret draft", "draft-only-text", u.id, "@example.test"} {
		if strings.Contains(list.Body.String(), bad) {
			t.Errorf("the public list contains %q", bad)
		}
	}

	// Draft, unpublished, unknown and malformed addresses are all the same answer.
	var ref string
	for _, slug := range []string{"taken-down", "no-such-post", "Bad_Slug", "ab", strings.Repeat("a", 90)} {
		w := a.anon("GET", "/api/public/blog/posts/"+slug)
		strip := func(s string) string { return s[:strings.Index(s, "requestId")] }
		if w.Code != 404 {
			t.Errorf("%q: %d", slug, w.Code)
		}
		if ref == "" {
			ref = strip(w.Body.String())
		} else if strip(w.Body.String()) != ref {
			t.Errorf("%q answers differently: %s", slug, w.Body)
		}
	}
	// Only reads.
	if w := a.anon("POST", "/api/public/blog/posts/live-post"); w.Code == 200 || w.Code == 201 {
		t.Errorf("a write to a public route: %d", w.Code)
	}
}

func TestSuspendingTheAuthorHidesTheirPostsAndReinstatingRestoresThem(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	u.publish("Suspend me", "suspend-me", "text")
	asset := into[struct{ AssetID string }](t, u.do("POST", "/api/blog/posts/"+into[[]Summary](t, u.do("GET", "/api/blog/posts", nil))[0].ID+"/assets", pngBytes()))
	imgPath := "/api/public/blog/posts/suspend-me/assets/" + asset.AssetID
	// Not yet published with the image as cover, but the image of a published post is readable once approved.
	if w := a.anon("GET", "/api/public/blog/posts/suspend-me"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := a.anon("GET", imgPath); w.Code != 200 {
		t.Fatalf("image of a published post: %d", w.Code)
	}
	for _, status := range []string{"SUSPENDED", "REJECTED"} {
		if _, err := a.db.Exec(`UPDATE users SET approval_status = ? WHERE id = ?`, status, u.id); err != nil {
			t.Fatal(err)
		}
		if w := a.anon("GET", "/api/public/blog/posts/suspend-me"); w.Code != 404 {
			t.Errorf("%s author's post: %d", status, w.Code)
		}
		if w := a.anon("GET", imgPath); w.Code != 404 {
			t.Errorf("%s author's image: %d", status, w.Code)
		}
		if l := a.anon("GET", "/api/public/blog/posts"); strings.Contains(l.Body.String(), "suspend-me") {
			t.Errorf("%s author is listed", status)
		}
		if w := a.anon("GET", "/b/suspend-me"); w.Code != 404 {
			t.Errorf("%s author's page: %d", status, w.Code)
		}
	}
	a.db.Exec(`UPDATE users SET approval_status = 'APPROVED' WHERE id = ?`, u.id)
	if w := a.anon("GET", "/api/public/blog/posts/suspend-me"); w.Code != 200 {
		t.Fatalf("reinstated: %d", w.Code)
	}
}

func TestBlogPagesCarryTheirOwnMetadata(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	u.publish(`Tom & "Jerry" <b>`, "tom-and-jerry", "text")
	u.publish("Costs $1M, $$ and ${2} $(x)", "dollars-post", "text")
	w := a.anon("GET", "/b/tom-and-jerry")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	h := w.Body.String()
	for _, want := range []string{`<title>Tom &amp; &#34;Jerry&#34; &lt;b&gt;</title>`, `content="About Tom &amp; &#34;Jerry&#34; &lt;b&gt;"`, `/b/tom-and-jerry"`, `<noscript><article><h1>Tom &amp;`} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %s in the page", want)
		}
	}
	if strings.Contains(h, `content="noindex"`) || w.Header().Get("X-Robots-Tag") != "" {
		t.Error("a published post must be indexable")
	}
	if strings.Contains(h, "<b>") {
		t.Error("the title was not escaped")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe") {
		t.Errorf("CSP: %q", w.Header().Get("Content-Security-Policy"))
	}
	// "$" in a title must stay as typed: it is not a reference to anything.
	d := a.anon("GET", "/b/dollars-post").Body.String()
	for _, want := range []string{"<title>Costs $1M, $$ and ${2} $(x)</title>", `content="About Costs $1M, $$ and ${2} $(x)"`} {
		if !strings.Contains(d, want) {
			t.Errorf("missing %s in %s", want, d[:min(len(d), 900)])
		}
	}
	idx := a.anon("GET", "/b")
	if idx.Code != 200 || strings.Contains(idx.Body.String(), `content="noindex"`) {
		t.Errorf("/b: %d", idx.Code)
	}
	miss := a.anon("GET", "/b/no-such-post")
	if miss.Code != 404 || miss.Header().Get("X-Robots-Tag") == "" || strings.Contains(miss.Body.String(), "Tom") {
		t.Errorf("unknown post page: %d", miss.Code)
	}
}

func TestPublicPostsCarryNoInternalIdsAndImagesUseThePublicRoute(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	asset := into[struct{ AssetID, URL string }](t, u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()))
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"src": asset.URL, "alt": "x"}}}}
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Pic", "doc": doc})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "pic-post"})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})
	r := a.anon("GET", "/api/public/blog/posts/pic-post")
	if strings.Contains(r.Body.String(), p.ID) || !strings.Contains(r.Body.String(), "/api/public/blog/posts/pic-post/assets/"+asset.AssetID) {
		t.Fatalf("public doc: %s", r.Body)
	}
	var got struct {
		Doc struct {
			Content []struct{ Attrs struct{ Src string } }
		}
	}
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if w := a.anon("GET", got.Doc.Content[0].Attrs.Src); w.Code != 200 {
		t.Fatalf("the image address in the public doc does not load: %d", w.Code)
	}
}

func TestPublicPagesAndReadsDrawFromOneAllowance(t *testing.T) {
	a := newApp(t)
	a.active("alice").publish("Busy", "busy-post", "x")
	limited := false
	for i := 0; i < blogReadsForTest+50; i++ {
		path := "/b/busy-post"
		if i%2 == 0 {
			path = "/api/public/blog/posts/busy-post"
		}
		if a.anon("GET", path).Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("pages and API reads together were never limited")
	}
}

const blogReadsForTest = 600

func TestAPageLookupFailureIsNotA404(t *testing.T) {
	a := newApp(t)
	a.active("alice").publish("Fine", "fine-post", "x")
	if w := a.anon("GET", "/b/fine-post"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	a.db.Exec(`ALTER TABLE post_versions RENAME TO post_versions_gone`)
	w := a.anon("GET", "/b/fine-post")
	if w.Code != 500 || w.Header().Get("X-Robots-Tag") != "" {
		t.Fatalf("a failing database must not look like a missing post (and must not deindex it): %d %v", w.Code, w.Header())
	}
}

func TestATrailingSlashRedirectsToTheCanonicalAddress(t *testing.T) {
	a := newApp(t)
	a.active("alice").publish("Slash", "slash-post", "x")
	w := a.anon("GET", "/b/slash-post/")
	if w.Code != 301 || w.Header().Get("Location") != "/b/slash-post" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}
