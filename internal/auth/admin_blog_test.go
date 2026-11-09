package auth

import (
	"strings"
	"testing"
)

// Admin is blind to what users write (security-invariants §6, T070): no admin endpoint carries a post's title, summary, body,
// address or image, for a live account or a suspended one. A writer's published post, draft and image are put in the database
// with recognisable text, and everything the admin API will say about that writer is scanned for it, before and after the
// suspension.
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
		paths := []string{"/api/admin/users", "/api/admin/users/" + writer, "/api/admin/audit"}
		for _, st := range []string{"PENDING", "APPROVED", "REJECTED", "SUSPENDED"} {
			paths = append(paths, "/api/admin/users?status="+st)
		}
		for _, p := range paths {
			w := admin.get(p)
			if w.Code != 200 && w.Code != 400 {
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
