package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

type skillsRunner struct {
	args   []string
	stdout string
	err    error
}

func (r *skillsRunner) Run(_ context.Context, _ string, args ...string) (string, string, error) {
	r.args = args
	return r.stdout, "", r.err
}

// The skills of one profile: Hermes' own lister, run with HERMES_HOME set to that profile only, read back as a list.
func TestSkillsListsOneProfilesSkills(t *testing.T) {
	r := &skillsRunner{stdout: `{"object":"list","data":[{"name":"deep-research","description":"Research a topic","category":"research"}]}`}
	rec := do(t, newServer(r, testToken), "/skills", "Bearer "+testToken, `{"slug":"u_existing1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"deep-research"`) || !strings.Contains(rec.Body.String(), `"object":"list"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if len(r.args) != 8 || r.args[7] != skillsScript || r.args[0] != "sh" || r.args[4] != profilesDir || r.args[5] != "u_existing1" || r.args[6] != hermesPython {
		t.Fatalf("command %q", r.args[:min(len(r.args), 7)])
	}
	if !strings.Contains(r.args[2], `HERMES_HOME="$1/$2"`) {
		t.Fatalf("the profile is not selected through HERMES_HOME: %q", r.args[2])
	}
}

func TestSkillsRefusesWhatIsNotAProfilesList(t *testing.T) {
	for name, c := range map[string]struct {
		body, out string
		err       error
		want      int
	}{
		"no token":        {`{"slug":"u_existing1"}`, "", nil, http.StatusUnauthorized},
		"bad slug":        {`{"slug":"../etc"}`, "", nil, http.StatusBadRequest},
		"extra field":     {`{"slug":"u_existing1","path":"/etc"}`, "", nil, http.StatusBadRequest},
		"missing profile": {`{"slug":"u_existing1"}`, "", exitErr(3), http.StatusNotFound},
		"lister failed":   {`{"slug":"u_existing1"}`, "", exitErr(1), http.StatusBadGateway},
		"not a list":      {`{"slug":"u_existing1"}`, `{"object":"error"}`, nil, http.StatusBadGateway},
		"not json":        {`{"slug":"u_existing1"}`, `Traceback`, nil, http.StatusBadGateway},
	} {
		auth := "Bearer " + testToken
		if name == "no token" {
			auth = ""
		}
		rec := do(t, newServer(&skillsRunner{stdout: c.out, err: c.err}, testToken), "/skills", auth, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: got %d %s, want %d", name, rec.Code, rec.Body, c.want)
		}
	}
}
