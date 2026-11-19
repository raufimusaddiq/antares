package blog

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func metaOf(html, key string) string {
	m := regexp.MustCompile(`<meta (?:property|name)="` + regexp.QuoteMeta(key) + `" content="([^"]*)"`).FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	return m[1]
}

// A post written in Indonesian says so, and its page is an article with its publication time and author. A post with
// no summary still has a description: the first words of its body.
func TestAPostsPageSaysItsLanguageAndIsAnArticle(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Menjalankan asisten sendiri", "doc": body("Ini adalah catatan tentang cara menjalankan asisten pribadi dengan aman dan tidak mahal.")})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "asisten-sendiri", "summary": ""})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	h := a.anon("GET", "/b/asisten-sendiri").Body.String()
	if !strings.Contains(h, `<html lang="id">`) {
		t.Errorf("an Indonesian post does not say so:\n%s", h)
	}
	for key, want := range map[string]string{
		"og:type":        "article",
		"article:author": "Author alice",
		"description":    "Ini adalah catatan tentang cara menjalankan asisten pribadi dengan aman dan tidak mahal.",
		"og:description": "Ini adalah catatan tentang cara menjalankan asisten pribadi dengan aman dan tidak mahal.",
	} {
		if got := metaOf(h, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if metaOf(h, "article:published_time") == "" {
		t.Errorf("an article has no publication time:\n%s", h)
	}
}

// A card is never a title over empty space: a post with no summary shows the first words of its body.
func TestAPostWithoutASummaryStillHasACardExcerpt(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "No summary", "doc": body("The first words of the post are the card.")})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "no-summary", "summary": ""})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	var l []PublicSummary
	if err := json.Unmarshal(a.anon("GET", "/api/public/blog/posts").Body.Bytes(), &l); err != nil || len(l) != 1 {
		t.Fatalf("list: %v %+v", err, l)
	}
	if l[0].Summary != "The first words of the post are the card." {
		t.Errorf("card excerpt = %q", l[0].Summary)
	}
}

// A summary is the author's own text: previews and cards show it whole, however long it is.
func TestAnAuthorsSummaryIsNeverCut(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	summary := strings.TrimSpace(strings.Repeat("A sentence the author wrote. ", 8)) // well past the 120-rune body cut
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Summarised", "doc": body("The body is not what a reader is shown.")})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "summarised", "summary": summary})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	h := a.anon("GET", "/b/summarised").Body.String()
	for _, key := range []string{"description", "og:description", "twitter:description"} {
		if got := metaOf(h, key); got != summary {
			t.Errorf("%s = %q, want the summary whole", key, got)
		}
	}
	var l []PublicSummary
	if err := json.Unmarshal(a.anon("GET", "/api/public/blog/posts").Body.Bytes(), &l); err != nil || len(l) != 1 {
		t.Fatalf("list: %v %+v", err, l)
	}
	if l[0].Summary != summary {
		t.Errorf("card summary = %q, want the summary whole", l[0].Summary)
	}
}

// The body fallback is the one that is cut: a long post becomes about three lines and says so with an ellipsis.
func TestALongBodyIsCutToAboutThreeLines(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Long", "doc": body(strings.Repeat("word ", 40))})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "long", "summary": ""})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	d := metaOf(a.anon("GET", "/b/long").Body.String(), "description")
	if n := len([]rune(d)); n > 121 || !strings.HasSuffix(d, "…") || !strings.HasPrefix(d, "word word") {
		t.Errorf("body excerpt = %q (%d runes)", d, n)
	}
}

// Words in neither language are still a sensible page: it says English, not nothing.
func TestAPostInNeitherLanguageSaysEnglish(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "2026 11 19", "doc": body("1234 5678 90")})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "numbers", "summary": ""})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	if h := a.anon("GET", "/b/numbers").Body.String(); !strings.Contains(h, `<html lang="en">`) {
		t.Errorf("a page with no recognisable words does not default to English:\n%s", h)
	}
}

// The page of a published post carries its own title, description, address and cover for search engines and link previews;
// the page of anything a reader may not see carries none of it.
func TestAPublishedPostsPageCarriesItsOwnOpenGraphAndCanonical(t *testing.T) {
	a := newApp(t)
	u := a.active("alice")
	p := u.create()
	asset := into[struct{ AssetID string }](t, u.do("POST", "/api/blog/posts/"+p.ID+"/assets", pngBytes()))
	u.do("PUT", "/api/blog/posts/"+p.ID, map[string]any{"baseVersion": 1, "title": "Meta & more", "doc": body("text")})
	u.do("PATCH", "/api/blog/posts/"+p.ID+"/settings", map[string]any{"slug": "meta-post", "summary": "A summary for previews", "coverAssetId": asset.AssetID})
	u.do("POST", "/api/blog/posts/"+p.ID+"/publish", map[string]any{"mode": "original"})

	h := a.anon("GET", "/b/meta-post").Body.String()
	canonical := regexp.MustCompile(`<link rel="canonical" href="([^"]*)"`).FindStringSubmatch(h)
	if canonical == nil || !strings.HasSuffix(canonical[1], "/b/meta-post") {
		t.Fatalf("canonical: %v", canonical)
	}
	origin := strings.TrimSuffix(canonical[1], "/b/meta-post")
	for key, want := range map[string]string{
		"og:title": "Meta &amp; more", "twitter:title": "Meta &amp; more",
		"og:description": "A summary for previews", "twitter:description": "A summary for previews", "description": "A summary for previews",
		"og:url":   canonical[1],
		"og:image": origin + "/api/public/blog/posts/meta-post/assets/" + asset.AssetID, "twitter:image": origin + "/api/public/blog/posts/meta-post/assets/" + asset.AssetID,
	} {
		if got := metaOf(h, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// Unpublished: the same address answers as a post that does not exist, and its page says nothing of the post.
	u.do("POST", "/api/blog/posts/"+p.ID+"/unpublish", nil)
	w := a.anon("GET", "/b/meta-post")
	if w.Code != 404 || w.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("an unpublished post's page: %d %v", w.Code, w.Header())
	}
	for _, leak := range []string{"Meta &amp; more", "A summary for previews", "meta-post"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("an unpublished post's page still carries %q", leak)
		}
	}
}
