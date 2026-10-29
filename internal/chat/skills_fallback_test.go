package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

type fakeSkills struct {
	mu    sync.Mutex
	slugs []string
	list  []hermes.Skill
	err   error
}

func (f *fakeSkills) Skills(_ context.Context, slug string) ([]hermes.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slugs = append(f.slugs, slug)
	return f.list, f.err
}

// Hermes v0.22.5's own skills route fails on every call. The capability then comes from the provisioning helper,
// for the caller's own profile only; without the helper, or when it fails too, Hermes' reason stands.
func TestSkillsComeFromTheHelperWhenHermesCannotListThem(t *testing.T) {
	for name, c := range map[string]struct {
		src       *fakeSkills
		available bool
	}{
		"helper lists them": {&fakeSkills{list: []hermes.Skill{{Name: "deep-research", Description: "Research a topic", Category: "research"}}}, true},
		"helper fails":      {&fakeSkills{err: errors.New("down")}, false},
		"helper finds none": {&fakeSkills{}, false},
		"no helper":         {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			opt := appOpts{}
			if c.src != nil {
				opt.skills = c.src
			}
			a := newApp(t, opt)
			alice := a.user("alice")
			serveDiscovery(a, readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json"))
			a.hx.mu.Lock()
			delete(a.hx.canned, "/v1/skills") // the route fails, as on the deployed build
			a.hx.mu.Unlock()
			w := alice.get("/api/capabilities")
			body := w.Body.String()
			if w.Code != 200 {
				t.Fatalf("GET = %d %s", w.Code, body)
			}
			has := strings.Contains(body, `"id":"skills","label":"Skills","status":"available"`)
			if has != c.available || strings.Contains(body, "deep-research") != c.available {
				t.Fatalf("skills available = %v, want %v: %s", has, c.available, body)
			}
			if c.src != nil {
				c.src.mu.Lock()
				defer c.src.mu.Unlock()
				if len(c.src.slugs) != 1 || c.src.slugs[0] != "u_aliceprofile" {
					t.Fatalf("helper asked about %v, want only the caller's own profile", c.src.slugs)
				}
			}
		})
	}
}

// When Hermes lists the skills itself, the helper is not asked.
func TestSkillsHelperIsNotAskedWhenHermesListsThem(t *testing.T) {
	src := &fakeSkills{list: []hermes.Skill{{Name: "from-helper"}}}
	a := newApp(t, appOpts{skills: src})
	alice := a.user("alice")
	serveDiscovery(a, readFixture(t, "../../docs/hermes/fixtures/capabilities.body.json"))
	w := alice.get("/api/capabilities")
	if strings.Contains(w.Body.String(), "from-helper") || len(src.slugs) != 0 {
		t.Fatalf("helper used although Hermes answered: %s", w.Body.String())
	}
}
