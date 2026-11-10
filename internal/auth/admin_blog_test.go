package auth

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Admin is blind to what users write (security-invariants §6, T070): no admin endpoint carries a post's title, summary, body,
// address or image, for a live account or a suspended one. A writer's published post, draft and image are put in the database
// with recognisable text, and everything the admin API will say about that writer is scanned for it, before and after the
// suspension.
// scanned is every GET endpoint the admin API has, with the status filters of the user list. A user's page takes the writer's id.
func scanned(writer string) []string {
	paths := []string{
		"/api/admin/users", "/api/admin/users/" + writer, "/api/admin/audit",
		"/api/admin/model-policy", "/api/admin/models", "/api/admin/tool-policy", "/api/admin/media-models", "/api/admin/kenari-usage", "/api/admin/metrics",
	}
	for _, st := range []string{"PENDING", "APPROVED", "REJECTED", "SUSPENDED"} {
		paths = append(paths, "/api/admin/users?status="+st)
	}
	return paths
}

// takedownRoutes are the administrator's post routes (specs/004-admin-full-control): published content and takedown state only.
// They need the blog mounted, so the auth harness cannot answer them; the blog package proves what they return
// (TestAdminBlogPostRoutesReturnOnlyPublishedFields) and this list is the only place they are allowed (below).
var takedownRoutes = map[string]bool{
	"GET /api/admin/blog-posts":                     true,
	"GET /api/admin/blog-posts/{postId}":            true,
	"POST /api/admin/blog-posts/{postId}/take-down": true,
	"POST /api/admin/blog-posts/{postId}/restore":   true,
}

// "Every admin endpoint" stays literal: a GET route added to admin.go that scanned() does not list fails here, so a new admin
// view cannot go in without being checked for post and draft content.
func TestEveryAdminGetRouteIsScannedForPostContent(t *testing.T) {
	src, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range scanned("WRITER") {
		have[strings.SplitN(strings.Replace(p, "WRITER", "{userId}", 1), "?", 2)[0]] = true
	}
	found := 0
	for _, m := range regexp.MustCompile(`mux\.Handle\("GET (/api/admin/[^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		found++
		if takedownRoutes["GET "+m[1]] {
			continue
		}
		if !have[m[1]] {
			t.Errorf("GET %s is an admin route and is not scanned for post content (add it to scanned)", m[1])
		}
	}
	if found < 8 {
		t.Fatalf("found only %d admin GET routes in admin.go: the pattern no longer matches how they are registered", found)
	}
}

func TestAdminSeesNoPostOrDraftContent(t *testing.T) {
	h := newHarness(t)
	admin, _, _ := h.adminWithKicks()
	h.userIn("writer", "APPROVED", "READY", "USER", true)
	writer := h.userID("writer")

	const (
		title   = "ADMINBLIND-title-7c1e"
		summary = "ADMINBLIND-summary-5a40"
		body    = "ADMINBLIND-body-3d9a"
		slug    = "adminblind-slug-8b2f"
		draft   = "ADMINBLIND-draft-91ce"
		image   = "ADMINBLIND-image-bytes"
	)
	now := "2026-11-09T00:00:00.000000000Z"
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + body + `"}]}]}`
	h.exec(`INSERT INTO posts (id, author_id, slug, title, summary, status, latest_version, published_version, published_at, created_at, updated_at)
		VALUES ('pub1', ?, ?, ?, ?, 'published', 1, 1, ?, ?, ?)`, writer, slug, title, summary, now, now, now)
	h.exec(`INSERT INTO post_versions (post_id, version, title, summary, doc, pinned, created_at) VALUES ('pub1', 1, ?, ?, ?, 1, ?)`, title, summary, doc, now)
	h.exec(`INSERT INTO posts (id, author_id, title, status, latest_version, created_at, updated_at) VALUES ('dra1', ?, ?, 'draft', 1, ?, ?)`, writer, draft, now, now)
	h.exec(`INSERT INTO post_versions (post_id, version, title, summary, doc, pinned, created_at) VALUES ('dra1', 1, ?, '', ?, 0, ?)`, draft, strings.Replace(doc, body, draft, 1), now)
	h.exec(`INSERT INTO post_assets (id, post_id, author_id, content_type, bytes, created_at) VALUES ('img1', 'pub1', ?, 'image/png', ?, ?)`, writer, []byte(image), now)

	scan := func(when string) {
		t.Helper()
		for _, p := range scanned(writer) {
			w := admin.get(p)
			if w.Code != 200 { // an endpoint that did not answer has not been scanned
				t.Errorf("%s: GET %s: %d", when, p, w.Code)
			}
			for _, bad := range []string{title, summary, body, slug, draft, image, "dra1", "pub1", "img1"} {
				if strings.Contains(w.Body.String(), bad) {
					t.Errorf("%s: GET %s tells the admin %q:\n%s", when, p, bad, w.Body)
				}
			}
		}
	}
	scan("a live writer")

	if w := admin.act(writer, "suspend", `{"reason":"a reason the admin typed"}`); w.Code != 200 {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	scan("a suspended writer")
}

// Reading the public blog leaves no record an admin can see (blog discovery FR-014, FR-015): the admin API has no route that
// names posts, readers or read counts, so there is nothing to list who read what.
func TestNoAdminRouteNamesPostsReadersOrReads(t *testing.T) {
	src, err := os.ReadFile("admin.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`mux\.Handle\("([A-Z]+) (/api/admin/[^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		key := m[1] + " " + m[2]
		lower := strings.ToLower(m[2])
		for _, word := range []string{"reader", "reads", "views"} { // never allowed, takedown routes included
			if strings.Contains(lower, word) {
				t.Errorf("admin route %s names %q: an admin must not see what people read", key, word)
			}
		}
		if takedownRoutes[key] {
			seen[key] = true
			continue
		}
		for _, word := range []string{"blog", "post"} {
			if strings.Contains(lower, word) {
				t.Errorf("admin route %s names %q: only the takedown routes may (takedownRoutes)", key, word)
			}
		}
	}
	for k := range takedownRoutes {
		if !seen[k] {
			t.Errorf("takedownRoutes lists %s, which admin.go does not register", k)
		}
	}
}
