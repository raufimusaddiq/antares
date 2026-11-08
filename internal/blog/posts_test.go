package blog

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func body(text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

func TestAutosaveIsCompareAndSet(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	if p.Status != "draft" || p.LatestVersion != 1 {
		t.Fatalf("new post: %+v", p)
	}
	w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "First", "doc": body("one")})
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	saved := into[Post](t, w)
	if saved.LatestVersion != 2 || saved.Title != "First" {
		t.Fatalf("saved: %+v", saved)
	}
	// A save from an older copy (another tab, another device) is refused and carries the newer post.
	w = u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Stale", "doc": body("old")})
	if w.Code != 409 {
		t.Fatalf("stale save: %d %s", w.Code, w.Body.String())
	}
	var c struct {
		Error  struct{ Code string }
		Latest Post
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if c.Latest.Title != "First" || c.Latest.LatestVersion != 2 || c.Error.Code != "CONFLICT" {
		t.Fatalf("409 body: %s", w.Body.String())
	}
	if got := into[Post](t, u.do("GET", "/api/blog/posts/"+p.ID, nil)); got.Title != "First" {
		t.Fatalf("a refused save changed the post: %+v", got)
	}
}

func TestAutosaveRefusesWhatIsNotAllowed(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	bad := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "script"}}}
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "doc": bad}); w.Code != 422 {
		t.Fatalf("script node: %d", w.Code)
	}
	for _, extra := range []string{"profile", "apiKey", "sessionId", "authorId", "status"} {
		if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, extra: "x"}); w.Code != 400 {
			t.Errorf("client-supplied %q was accepted: %d", extra, w.Code)
		}
	}
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"title": "no base"}); w.Code != 400 {
		t.Fatalf("missing baseVersion: %d", w.Code)
	}
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": strings.Repeat("x", 201)}); w.Code != 422 {
		t.Fatalf("overlong title: %d", w.Code)
	}
}

func TestPublishNeedsTitleAndAddressAndKeepsThePublishedVersion(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 422 {
		t.Fatalf("publishing an untitled post: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "enriched"}); w.Code != 501 {
		t.Fatalf("enriched before the check exists: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "x"}); w.Code != 400 {
		t.Fatalf("unknown mode: %d", w.Code)
	}
	pub := u.publish("Hello", "hello-world", "first text")
	if pub.Status != "published" || pub.PublishedVersion != pub.LatestVersion {
		t.Fatalf("published: %+v", pub)
	}
	// Editing after publishing starts a new version; readers keep the published one until the author republishes.
	w := u.do("PUT", "/api/blog/posts/"+pub.ID, map[string]any{"baseVersion": pub.LatestVersion, "title": "Hello edited", "doc": body("second text")})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	edited := into[Post](t, w)
	if edited.LatestVersion != pub.LatestVersion+1 || edited.PublishedVersion != pub.PublishedVersion || edited.Status != "published" {
		t.Fatalf("after edit: %+v", edited)
	}
	r := a.anon("GET", "/api/public/blog/posts/hello-world")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "first text") || strings.Contains(r.Body.String(), "second text") || strings.Contains(r.Body.String(), "Hello edited") {
		t.Fatalf("readers must still see the published version: %d %s", r.Code, r.Body.String())
	}
	// Another edit before republishing rewrites the unpublished head in place instead of piling up versions.
	w = u.do("PUT", "/api/blog/posts/"+pub.ID, map[string]any{"baseVersion": edited.LatestVersion, "title": "Hello again"})
	again := into[Post](t, w)
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_versions WHERE post_id = ?`, pub.ID).Scan(&n)
	if n != 2 || again.LatestVersion != edited.LatestVersion+1 {
		t.Fatalf("versions kept: %d, latest %d", n, again.LatestVersion)
	}
	if rep := into[Post](t, u.do("POST", "/api/blog/posts/"+pub.ID+"/publish", map[string]any{"mode": "original"})); rep.PublishedVersion != again.LatestVersion {
		t.Fatalf("republish: %+v", rep)
	}
}

func TestAnAddressNamesOnePublishedPost(t *testing.T) {
	a := newApp(t)
	u, v := a.active("alice"), a.active("bob")
	u.publish("Alice's", "same-name", "a")
	p := v.create()
	v.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Bob's"})
	// Drafts may hold any valid address, even one in use; it is publishing that must be unique.
	if w := v.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "same-name"}); w.Code != 200 {
		t.Fatalf("draft address: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 409 {
		t.Fatalf("publishing a taken address: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"Bad Slug", "ab", "admin", "-x-"} {
		if w := v.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": bad}); w.Code != 422 {
			t.Errorf("address %q: %d", bad, w.Code)
		}
	}
	// Unpublishing frees the address.
	list := into[[]Summary](t, u.do("GET", "/api/blog/posts", nil))
	u.do("POST", "/api/blog/posts/"+list[0].ID+"/unpublish", nil)
	v.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "same-name"})
	if w := v.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 200 {
		t.Fatalf("after unpublish: %d %s", w.Code, w.Body.String())
	}
}

func pngBytes() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}

func TestImagesAreDraftPrivateAndTypeChecked(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	up := u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes())
	if up.Code != 200 {
		t.Fatalf("upload: %d %s", up.Code, up.Body.String())
	}
	asset := into[struct{ AssetID, URL string }](t, up)
	if asset.URL != "/api/blog/posts/"+p.ID+"/assets/"+asset.AssetID {
		t.Fatalf("url: %s", asset.URL)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/assets", "<svg onload=alert(1)></svg>"); w.Code != 415 {
		t.Fatalf("svg: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/assets", []byte("<html>")); w.Code != 415 {
		t.Fatalf("html: %d", w.Code)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/assets", bytes.Repeat([]byte{0}, maxAssetBytes+1)); w.Code != 413 {
		t.Fatalf("oversize: %d", w.Code)
	}
	g := u.do("GET", asset.URL, nil)
	if g.Code != 200 || g.Header().Get("Content-Type") != "image/png" || g.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(g.Body.Bytes(), pngBytes()) {
		t.Fatalf("author read: %d %v", g.Code, g.Header())
	}
	// Before publishing, nobody else can read it, and the public route does not know it.
	if r := a.anon("GET", "/api/public/blog/posts/draft-post/assets/"+asset.AssetID); r.Code != 404 {
		t.Fatalf("public read of a draft's image: %d", r.Code)
	}
	// Publish with the image in the body and as the cover; readers get it through the public route.
	img := map[string]any{"type": "image", "attrs": map[string]any{"src": asset.URL, "alt": "a pixel"}}
	doc := map[string]any{"type": "doc", "content": []any{img}}
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "With image", "doc": doc})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "with-image", "coverAssetId": asset.AssetID})
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	pub := a.anon("GET", "/api/public/blog/posts/with-image")
	var got PublicPost
	_ = json.Unmarshal(pub.Body.Bytes(), &got)
	if got.CoverURL != "/api/public/blog/posts/with-image/assets/"+asset.AssetID {
		t.Fatalf("cover: %s", pub.Body.String())
	}
	r := a.anon("GET", got.CoverURL)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), pngBytes()) || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("public image: %d %v", r.Code, r.Header())
	}
	// A cover must be this post's own image.
	other := u.create()
	if w := u.do("PATCH", "/api/blog/posts/"+other.ID+"/settings", map[string]any{"coverAssetId": asset.AssetID}); w.Code != 422 {
		t.Fatalf("another post's image as a cover: %d", w.Code)
	}
}

func TestDuplicateAndDelete(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	asset := into[struct{ AssetID, URL string }](t, u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()))
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"src": asset.URL}}}}
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Original", "doc": doc})
	d := into[Post](t, u.do("POST", "/api/blog/posts/"+p.ID+"/duplicate", nil))
	if d.ID == p.ID || d.Title != "Original (copy)" || d.Status != "draft" || d.Slug != "" {
		t.Fatalf("copy: %+v", d)
	}
	if strings.Contains(string(d.Doc), p.ID) || !strings.Contains(string(d.Doc), d.ID) {
		t.Fatalf("the copy still points at the original's image: %s", d.Doc)
	}
	// Deleting the original leaves the copy's image alone, and frees nothing the copy needs.
	if w := u.do("DELETE", "/api/blog/posts/"+p.ID, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_versions WHERE post_id = ?`, p.ID).Scan(&n)
	var m int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_assets WHERE post_id = ?`, p.ID).Scan(&m)
	if n != 0 || m != 0 {
		t.Fatalf("delete left %d versions and %d images", n, m)
	}
	if w := u.do("GET", "/api/blog/posts/"+p.ID, nil); w.Code != 404 {
		t.Fatalf("deleted post: %d", w.Code)
	}
	var k int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_assets WHERE post_id = ?`, d.ID).Scan(&k)
	if k != 1 {
		t.Fatalf("the copy's images: %d", k)
	}
}

func TestListShowsOnlyMyPosts(t *testing.T) {
	a := newApp(t)
	u, v := a.active("alice"), a.active("bob")
	u.create()
	u.publish("Mine", "my-post", "x")
	v.create()
	l := into[[]Summary](t, u.do("GET", "/api/blog/posts", nil))
	if len(l) != 2 {
		t.Fatalf("list: %+v", l)
	}
	seen := map[string]bool{}
	for _, x := range l {
		seen[x.Status] = true
	}
	if !seen["draft"] || !seen["published"] {
		t.Fatalf("statuses: %+v", l)
	}
}

func TestCapabilitiesAreHonest(t *testing.T) {
	a := newApp(t)
	c := into[map[string]bool](t, a.active("alice").do("GET", "/api/blog/capabilities", nil))
	if !c["writing"] || c["check"] || c["webSearch"] {
		t.Fatalf("until the Hermes check exists it is not offered: %v", c)
	}
}

func TestUnpublishingADraftIsRefused(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/unpublish", nil); w.Code != 409 {
		t.Fatalf("unpublish a draft: %d", w.Code)
	}
}

func TestAnAuthorsImagesAreCappedInTotal(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	// Fill the account's allowance directly, then one more upload must be refused.
	if _, err := a.db.Exec(`INSERT INTO post_assets (id, post_id, author_id, content_type, bytes, created_at) VALUES ('fill', ?, ?, 'image/png', zeroblob(?), 'x')`, p.ID, u.id, maxAssetBytesPerAuthor-10); err != nil {
		t.Fatal(err)
	}
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()); w.Code != 422 {
		t.Fatalf("upload over the account total: %d", w.Code)
	}
	// Duplicating a post with images counts too.
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/duplicate", nil); w.Code != 422 {
		t.Fatalf("duplicate over the account total: %d", w.Code)
	}
}

func TestDuplicateOfALongTitleStillSaves(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	long := strings.Repeat("é", maxTitle)
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": long}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	d := into[Post](t, u.do("POST", "/api/blog/posts/"+p.ID+"/duplicate", nil))
	if w := u.do("PUT", "/api/blog/posts/"+d.ID, map[string]any{"baseVersion": d.LatestVersion, "title": d.Title}); w.Code != 200 {
		t.Fatalf("the copy's own title does not save: %d (%d runes)", w.Code, len([]rune(d.Title)))
	}
}

func TestUploadsAndAutosavesAreRateLimited(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	limited := 0
	for i := 0; i < 15; i++ {
		if u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()).Code == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Error("15 uploads in a minute were never limited")
	}
	saves := 0
	for i := 0; i < 400 && saves == 0; i++ {
		if u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 0, "title": "x"}).Code == 429 {
			saves++
		}
	}
	if saves == 0 {
		t.Error("400 saves in a minute were never limited")
	}
}

func TestSettingsKeepTheCoverAndUnpublishLeavesNoFrozenVersion(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	asset := into[struct{ AssetID string }](t, u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()))
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Cover"})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "cover-post", "coverAssetId": asset.AssetID})
	got := into[Post](t, u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"summary": "only the summary changes"}))
	if got.CoverURL == "" {
		t.Fatal("a settings save that did not mention the cover cleared it")
	}
	pub := into[Post](t, u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"}))
	u.do("POST", "/api/blog/posts/"+p.ID+"/unpublish", nil)
	var n int
	for i := 0; i < 3; i++ {
		cur := into[Post](t, u.do("GET", "/api/blog/posts/"+p.ID, nil))
		u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": cur.LatestVersion, "title": "edit"})
	}
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_versions WHERE post_id = ?`, p.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("after unpublishing, edits should reuse one version row, found %d (published at v%d)", n, pub.PublishedVersion)
	}
}

func TestRemoteImagesAreRefused(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"src": "https://example.org/a.png"}}}}
	if w := u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "doc": doc}); w.Code != 422 {
		t.Fatalf("a remote image can never render under the page's CSP: %d", w.Code)
	}
}

func TestBodylessPostsAcceptChunkedEmptyBodies(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.publish("Chunk", "chunk-post", "x")
	r := httptest.NewRequest("POST", testOrigin+"/api/blog/posts/"+p.ID+"/unpublish", io.NopCloser(strings.NewReader("")))
	r.ContentLength = -1 // a streamed body of unknown length, empty
	r.Header.Set("Origin", testOrigin)
	r.AddCookie(&http.Cookie{Name: "__Host-antares_session", Value: u.cookie})
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("a chunked empty body: %d %s", w.Code, w.Body)
	}
}

func TestAnOversizeJSONBodyIs413(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	big := `{"mode":"original","pad":"` + strings.Repeat("x", 5000) + `"}`
	if w := u.do("POST", "/api/blog/posts/"+p.ID+"/publish", big); w.Code != 413 {
		t.Fatalf("oversize body: %d", w.Code)
	}
}

func TestRepublishingLeavesOnlyTheLiveVersionPinned(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.publish("Twice", "twice-post", "one")
	cur := into[Post](t, u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": p.LatestVersion, "title": "Twice 2"}))
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})
	var pinned int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM post_versions WHERE post_id = ? AND pinned = 1`, p.ID).Scan(&pinned)
	if pinned != 1 || cur.LatestVersion != p.LatestVersion+1 {
		t.Fatalf("pinned versions: %d", pinned)
	}
}

func TestAHugePublicPageNumberIsNotWalked(t *testing.T) {
	a := newApp(t)
	a.active("alice").publish("One", "one-post", "x")
	w := a.anon("GET", "/api/public/blog/posts?page=1000000000")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := a.anon("GET", "/api/public/blog/posts?page=1"); !strings.Contains(w.Body.String(), "one-post") {
		t.Fatal("the first page must still list")
	}
}
