package blog

import (
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
