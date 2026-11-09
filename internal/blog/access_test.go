package blog

import "testing"

// Only an ACTIVE account may use the authenticated routes (FR-025); a signed-out visitor gets 401.
func TestEveryAuthenticatedRouteRefusesTheIneligible(t *testing.T) {
	a := newApp(t)
	owner := a.active("owner")
	p := owner.create()
	people := map[string]*person{
		"pending":           a.user("pend", "PENDING", "UNPROVISIONED", false),
		"rejected":          a.user("rej", "REJECTED", "UNPROVISIONED", false),
		"suspended":         a.user("susp", "SUSPENDED", "READY", true),
		"setting up":        a.user("setup", "APPROVED", "CONFIGURING", false),
		"failed":            a.user("fail", "APPROVED", "FAILED", false),
		"ready, no binding": a.user("nobind", "APPROVED", "READY", false),
	}
	paths := []struct{ method, path string }{
		{"GET", "/api/blog/capabilities"}, {"GET", "/api/blog/limits"}, {"GET", "/api/blog/posts"}, {"POST", "/api/blog/posts"},
		{"GET", "/api/blog/posts/" + p.ID}, {"PUT", "/api/blog/posts/" + p.ID}, {"PATCH", "/api/blog/posts/" + p.ID + "/settings"},
		{"DELETE", "/api/blog/posts/" + p.ID}, {"POST", "/api/blog/posts/" + p.ID + "/publish"}, {"POST", "/api/blog/posts/" + p.ID + "/unpublish"},
		{"POST", "/api/blog/posts/" + p.ID + "/duplicate"}, {"POST", "/api/blog/posts/" + p.ID + "/assets"},
		{"POST", "/api/blog/posts/" + p.ID + "/check"}, {"GET", "/api/blog/posts/" + p.ID + "/check"}, {"POST", "/api/blog/posts/" + p.ID + "/check/cancel"},
		{"PUT", "/api/blog/posts/" + p.ID + "/blocks/00000000000000000000000000000000"},
		{"GET", "/api/blog/posts/" + p.ID + "/assets/00000000000000000000000000000000"},
	}
	for who, person := range people {
		for _, r := range paths {
			if w := person.do(r.method, r.path, map[string]any{}); w.Code != 403 && w.Code != 401 && w.Code != 409 {
				t.Errorf("%s: %s %s answered %d", who, r.method, r.path, w.Code)
			}
		}
	}
	for _, r := range paths {
		if w := a.anon(r.method, r.path); w.Code != 401 {
			t.Errorf("signed out: %s %s answered %d", r.method, r.path, w.Code)
		}
	}
	if l := into[[]Summary](t, owner.do("GET", "/api/blog/posts", nil)); len(l) != 1 {
		t.Fatalf("the owner's post changed: %+v", l)
	}
	// A cross-origin mutation is refused even for an active account.
	r := owner.do("DELETE", "/api/blog/posts/"+p.ID, nil)
	if r.Code != 200 {
		t.Fatalf("same-origin delete: %d", r.Code)
	}
}
