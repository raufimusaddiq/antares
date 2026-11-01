package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// fakeMemory keeps a memory per profile, as the helper reads it from that profile's own files.
type fakeMemory struct {
	mu    sync.Mutex
	slugs []string
	by    map[string]hermes.Memory
	err   error
}

func (f *fakeMemory) Memory(_ context.Context, slug string) (hermes.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slugs = append(f.slugs, slug)
	return f.by[slug], f.err
}

// #248: each person sees only the memory of the profile bound to their own account. Nothing the client sends picks a
// profile, accounts that may not use the assistant are refused before the helper is asked, and there is still no route
// that edits or forgets an entry (TestCrossUserThreatNotYetBuilt, "memory edits").
func TestMemoryIsTheCallersOwnOnly(t *testing.T) {
	src := &fakeMemory{by: map[string]hermes.Memory{
		"u_aliceprofile": {User: []string{"ALICE-FACT"}, Memory: []string{"alice note"}},
		"u_bobbyprofile": {User: []string{"BOB-FACT"}, Memory: []string{}},
	}}
	a := newApp(t, appOpts{memory: src})
	alice, bob := a.user("alice"), a.user("bobby")
	for _, c := range []struct {
		p          *person
		want, leak string
	}{{alice, "ALICE-FACT", "BOB-FACT"}, {bob, "BOB-FACT", "ALICE-FACT"}} {
		w := c.p.get("/api/me/memory")
		var m hermes.Memory
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil || len(m.User) != 1 || m.User[0] != c.want || strings.Contains(w.Body.String(), c.leak) {
			t.Errorf("%s: %d %s", c.p.sub, w.Code, w.Body.String())
		}
		// Naming a profile is refused outright, before the helper is asked.
		if w := c.p.get("/api/me/memory?slug=u_aliceprofile&profile=u_bobbyprofile"); w.Code != 400 || strings.Contains(w.Body.String(), c.leak) {
			t.Errorf("%s naming a profile: %d %s", c.p.sub, w.Code, w.Body.String())
		}
	}
	if got := strings.Join(src.slugs, ","); got != "u_aliceprofile,u_bobbyprofile" {
		t.Errorf("helper asked about %s", got)
	}

	src.slugs = nil
	for who, p := range map[string]*person{
		"pending":    a.account("pend", "USER", "PENDING", "UNPROVISIONED"),
		"suspended":  a.account("susp", "USER", "SUSPENDED", "DISABLED"),
		"no binding": a.account("nobind", "USER", "APPROVED", "UNPROVISIONED"),
	} {
		if w := p.get("/api/me/memory"); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: %d %s", who, w.Code, w.Body.String())
		}
	}
	if len(src.slugs) != 0 {
		t.Errorf("helper asked for accounts that may not use the assistant: %v", src.slugs)
	}

	src.err = errors.New("helper down")
	if w := alice.get("/api/me/memory"); w.Code != 503 || strings.Contains(w.Body.String(), "ALICE-FACT") {
		t.Errorf("helper failure: %d %s", w.Code, w.Body.String())
	}
	if w := newApp(t).user("carol").get("/api/me/memory"); w.Code != 501 {
		t.Errorf("no memory source: %d %s", w.Code, w.Body.String())
	}
}
