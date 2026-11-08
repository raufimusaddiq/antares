package blog

import (
	"sort"
	"strings"
	"testing"

	"github.com/raufimusaddiq/antares/internal/auth"
)

// The kept route inventory (specs/001-blog-cms-openui-publish/contracts/http-api.md): the routes served without a
// session are the shared chat and exactly these three blog reads (plus the HTML pages /b and /b/{slug}). Adding any
// other fails here, and so does a blog route that answers a signed-out request.
func TestOnlyTheDocumentedRoutesAnswerWithoutASession(t *testing.T) {
	keys := func(rs []auth.Route) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Method+" "+r.Path)
		}
		sort.Strings(out)
		return out
	}
	equal := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got, want := keys(auth.PublicRoutes), []string{"GET /api/shared/{token}"}; !equal(got, want) {
		t.Errorf("public routes: %v", got)
	}
	want := []string{"GET /api/public/blog/posts", "GET /api/public/blog/posts/{slug}", "GET /api/public/blog/posts/{slug}/assets/{assetId}"}
	if got := keys(auth.PublicBlogRoutes); !equal(got, want) {
		t.Errorf("public blog routes: %v", got)
	}

	a := newApp(t)
	for _, r := range auth.BlogRoutes {
		path := r.Path
		for _, p := range [][2]string{{"{id}", "00000000000000000000000000000000"}, {"{assetId}", "00000000000000000000000000000000"}} {
			path = strings.ReplaceAll(path, p[0], p[1])
		}
		if w := a.anon(r.Method, path); w.Code != 401 {
			t.Errorf("%s %s answers a signed-out request with %d", r.Method, r.Path, w.Code)
		}
	}
	// Every handler the blog provides is a route auth knows: none is left unmounted or mounted unguarded by accident.
	known := map[string]bool{}
	for _, r := range auth.BlogRoutes {
		known[r.Method+" "+r.Path] = true
	}
	for k := range a.svc.Handlers() {
		if !known[k] {
			t.Errorf("handler %q has no entry in auth.BlogRoutes, so it would never be mounted", k)
		}
	}
	pub := map[string]bool{}
	for _, r := range auth.PublicBlogRoutes {
		pub[r.Method+" "+r.Path] = true
	}
	for k := range a.svc.PublicHandlers() {
		if !pub[k] {
			t.Errorf("public handler %q is not in auth.PublicBlogRoutes", k)
		}
	}
}
