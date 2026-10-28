package chat

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// fakeMedia stands in for the provisioning helper: it serves one file of one profile, and records who asked.
type fakeMedia struct {
	mu     sync.Mutex
	asked  []string
	owner  string
	path   string
	broken bool
}

func (f *fakeMedia) Media(_ context.Context, slug, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, slug+" "+path)
	if f.broken {
		return nil, "", errors.New("helper down")
	}
	if slug != f.owner || path != f.path {
		return nil, "", hermes.ErrMediaNotFound
	}
	return []byte("\x89PNG-0123456789"), "image/png", nil
}

var sealedTag = regexp.MustCompile(`MEDIA:(image-[A-Za-z0-9_-]+)`)

func TestMediaTagsAreSealedAndNeverCarryThePath(t *testing.T) {
	a := newApp(t)
	alice := a.user("alice")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	out := a.svc.sealMediaTags(alice.id, "Here.\nMEDIA:"+path+". And MEDIA:/opt/data/profiles/x/notes.txt done")
	if strings.Contains(out, "/opt/data") || strings.Contains(out, alice.profile) || strings.Contains(out, "notes.txt") {
		t.Fatalf("a path survived: %q", out)
	}
	if !sealedTag.MatchString(out) || !strings.HasPrefix(out, "Here.\nMEDIA:image-") || !strings.Contains(out, ". And  done") {
		t.Fatalf("sealed text: %q", out)
	}
}

func TestGeneratedMediaIsServedOnlyToTheUserItWasIssuedTo(t *testing.T) {
	a := newApp(t)
	alice, bob := a.user("alice"), a.user("bob")
	path := "/opt/data/profiles/" + alice.profile + "/cache/generated/images/a.png"
	m := &fakeMedia{owner: alice.profile, path: path}
	a.svc.media = m
	id := sealedTag.FindStringSubmatch(a.svc.sealMediaTags(alice.id, "MEDIA:"+path))[1]
	q := "/api/media?id=" + url.QueryEscape(id)

	r := alice.get(q)
	if r.Code != http.StatusOK || r.Body.String() != "\x89PNG-0123456789" || r.Header().Get("Content-Type") != "image/png" ||
		r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "sandbox") ||
		r.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("owner: %d %q %v", r.Code, r.Body, r.Header())
	}
	if r := alice.do("GET", q, map[string]string{"Range": "bytes=0-3"}, nil); r.Code != http.StatusPartialContent || r.Body.String() != "\x89PNG" {
		t.Fatalf("range: %d %q", r.Code, r.Body)
	}
	// Bob holding Alice's id: it does not open for him, and the helper is never asked.
	before := len(m.asked)
	if r := bob.get(q); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "PNG") {
		t.Fatalf("other user: %d", r.Code)
	}
	for _, bad := range []string{"", "image-", "image-AAAA", "image-" + strings.Repeat("A", 80)} {
		if r := alice.get("/api/media?id=" + url.QueryEscape(bad)); r.Code != http.StatusNotFound {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	if len(m.asked) != before {
		t.Error("an id that does not open for the caller reached the helper")
	}
	// A helper that cannot answer is a visible failure, not "no media".
	m.broken = true
	if r := alice.get(q); r.Code != http.StatusBadGateway {
		t.Errorf("broken helper: %d", r.Code)
	}
	a.svc.media = nil
	if r := alice.get(q); r.Code != http.StatusNotFound {
		t.Errorf("no helper configured: %d", r.Code)
	}
}
