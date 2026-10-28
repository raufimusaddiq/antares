package chat

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// fakeMedia stands in for the provisioning helper: it serves one file of one profile, and records who asked.
type fakeMedia struct {
	mu    sync.Mutex
	asked []string
	owner string
	path  string
}

func (f *fakeMedia) Media(_ context.Context, slug, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, slug+" "+path)
	if slug != f.owner || path != f.path {
		return nil, "", hermes.ErrMediaNotFound
	}
	return []byte("\x89PNG"), "image/png", nil
}

func TestGeneratedMediaIsServedFromTheCallersOwnProfileOnly(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	m := &fakeMedia{owner: alice.profile, path: path}
	a.svc.media = m
	q := "/api/media?path=" + url.QueryEscape(path)

	r := alice.get(q)
	if r.Code != http.StatusOK || r.Body.String() != "\x89PNG" || r.Header().Get("Content-Type") != "image/png" ||
		r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("owner: %d %q %v", r.Code, r.Body, r.Header())
	}
	// Bob asking for Alice's path: the helper is asked with Bob's own profile, so it refuses.
	if r := bob.get(q); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "PNG") {
		t.Fatalf("other user: %d", r.Code)
	}
	if got := m.asked[len(m.asked)-1]; !strings.HasPrefix(got, bob.profile+" ") {
		t.Fatalf("the helper was asked for %q, not with the caller's profile", got)
	}
	before := len(m.asked)
	for _, bad := range []string{"relative/a.png", "/opt/data/profiles/x/cache/../.env", ""} {
		if r := alice.get("/api/media?path=" + url.QueryEscape(bad)); r.Code != http.StatusNotFound {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	if len(m.asked) != before {
		t.Error("a malformed path reached the helper")
	}
	a.svc.media = nil
	if r := alice.get(q); r.Code != http.StatusNotFound {
		t.Errorf("no helper configured: %d", r.Code)
	}
}
