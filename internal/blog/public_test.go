package blog

import (
	"encoding/json"
	"net/url"
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

func TestAnApprovedAuthorStaysReadableWhileTheirWorkspaceIsReverified(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	u.publish("Stays up", "stays-up", "x")
	for _, st := range []string{"CONFIGURING", "FAILED"} {
		a.db.Exec(`UPDATE users SET tenant_status = ? WHERE id = ?`, st, u.id)
		if w := a.anon("GET", "/api/public/blog/posts/stays-up"); w.Code != 200 {
			t.Errorf("workspace %s took a published post offline: %d", st, w.Code)
		}
	}
}

// publicTitles is the list of titles a public list answered with, in order.
func publicTitles(t *testing.T, a *app, query string) []string {
	t.Helper()
	w := a.anon("GET", "/api/public/blog/posts"+query)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", query, w.Code, w.Body)
	}
	var l []PublicSummary
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatalf("%s: not JSON: %s", query, w.Body)
	}
	out := []string{}
	for _, p := range l {
		out = append(out, p.Title)
	}
	return out
}

func TestPublicListLimitIsBoundedAndPagesNeverRepeatOrSkip(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	for i := 0; i < 5; i++ {
		u.publish("Post number "+string(rune('A'+i)), "post-number-"+string(rune('a'+i)), "text")
	}
	if got := publicTitles(t, a, "?limit=3"); len(got) != 3 {
		t.Errorf("limit=3 gave %d posts", len(got))
	}
	for _, bad := range []string{"", "?limit=0", "?limit=21", "?limit=-1", "?limit=x", "?limit=1e3"} {
		if got := publicTitles(t, a, bad); len(got) != 5 {
			t.Errorf("%q should fall back to the default page and show all 5, got %d", bad, len(got))
		}
	}
	seen := map[string]bool{}
	for _, q := range []string{"?limit=2&page=1", "?limit=2&page=2", "?limit=2&page=3"} {
		for _, title := range publicTitles(t, a, q) {
			if seen[title] {
				t.Errorf("%q repeated %q", q, title)
			}
			seen[title] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("paging skipped posts: saw %d of 5", len(seen))
	}
}

func TestPublicSearchMatchesTitleSummaryAndAuthorAndTreatsTheTextAsPlain(t *testing.T) {
	a := newApp(t)
	alice, bob := a.active("alice"), a.active("bob")
	alice.publish("Alpha cats", "alpha-cats", "text")
	bob.publish("Beta dogs", "beta-dogs", "text")
	bob.publish("100% sure_thing", "sure-thing", "text")
	one := func(q, want string) {
		t.Helper()
		got := publicTitles(t, a, "?q="+url.QueryEscape(q))
		if len(got) != 1 || got[0] != want {
			t.Errorf("q=%q: got %v, want [%s]", q, got, want)
		}
	}
	one("cats", "Alpha cats")
	one("BETA", "Beta dogs")               // case does not matter
	one("  alpha    cats  ", "Alpha cats") // spaces collapse
	if got := publicTitles(t, a, "?q="+url.QueryEscape("Author bob")); len(got) != 2 {
		t.Errorf("an author's name should find both their posts, got %v", got)
	}
	if got := publicTitles(t, a, "?q="+url.QueryEscape("About Beta")); len(got) != 1 || got[0] != "Beta dogs" {
		t.Errorf("the summary should be searched, got %v", got)
	}
	// % and _ are plain characters, not wildcards.
	one("100%", "100% sure_thing")
	one("sure_thing", "100% sure_thing")
	for _, q := range []string{"%", "_", `\`, "%%%", "a_a"} {
		if got := publicTitles(t, a, "?q="+url.QueryEscape(q)); len(got) > 1 {
			t.Errorf("q=%q acted as a wildcard: %v", q, got)
		}
	}
	// Blank means no filter; too long means nothing, not an error.
	if got := publicTitles(t, a, "?q=%20%20"); len(got) != 3 {
		t.Errorf("a blank search should show everything, got %v", got)
	}
	if got := publicTitles(t, a, "?q="+strings.Repeat("a", 81)); len(got) != 0 {
		t.Errorf("an over-long search should be empty, got %v", got)
	}
	if got := publicTitles(t, a, "?q=%3Cscript%3E"); len(got) != 0 {
		t.Errorf("markup should be plain text, got %v", got)
	}
}

func TestPublicListIsTheSameForEveryCallerAndReadingWritesNothing(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	u.publish("First", "first-post", "text")
	reader := a.active("carol")
	for _, path := range []string{"/api/public/blog/posts", "/api/public/blog/posts?q=first&limit=3", "/api/public/blog/posts/first-post"} {
		anon, signed := a.anon("GET", path), reader.do("GET", path, nil)
		if anon.Code != 200 || anon.Body.String() != signed.Body.String() {
			t.Errorf("%s differs for a signed-in reader:\n%s\n%s", path, anon.Body, signed.Body)
		}
	}
	rows := func() map[string]int {
		out := map[string]int{}
		names, err := a.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer names.Close()
		var tables []string
		for names.Next() {
			var n string
			_ = names.Scan(&n)
			tables = append(tables, n)
		}
		for _, n := range tables {
			var c int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&c); err != nil {
				t.Fatal(err)
			}
			out[n] = c
		}
		return out
	}
	before := rows()
	for _, path := range []string{"/api/public/blog/posts", "/api/public/blog/posts?q=first", "/api/public/blog/posts/first-post", "/b", "/b/first-post"} {
		a.anon("GET", path)
	}
	after := rows()
	for n, c := range before {
		if after[n] != c {
			t.Errorf("reading changed %s: %d -> %d", n, c, after[n])
		}
	}
}

// A pending or suspended reader can read the public blog like anyone, and gets nothing more from the author routes
// that mark a reader's own posts: those stay refused until the account is approved.
func TestAReaderWhoIsNotApprovedStillCannotUseTheAuthorRoutes(t *testing.T) {
	a := newApp(t)
	a.active("alice").publish("Open post", "open-post", "text")
	for _, state := range []string{"PENDING", "SUSPENDED", "REJECTED"} {
		p := a.user("reader-"+strings.ToLower(state), state, "READY", false)
		if w := p.do("GET", "/api/blog/posts", nil); w.Code != 401 && w.Code != 403 {
			t.Errorf("%s reader got the author list: %d %s", state, w.Code, w.Body)
		}
		if w := a.anon("GET", "/api/public/blog/posts/open-post"); w.Code != 200 {
			t.Errorf("%s: the public post should stay readable: %d", state, w.Code)
		}
	}
	if w := a.anon("GET", "/api/blog/posts"); w.Code != 401 {
		t.Errorf("a visitor got the author list: %d", w.Code)
	}
}
