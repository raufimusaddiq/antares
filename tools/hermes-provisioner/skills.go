package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// POST /skills {"slug": ...}: the skills one profile can load, as Hermes' own lister reports them (name, description,
// category). Hermes v0.22.5's GET /v1/skills calls that lister with an argument it no longer accepts
// (include_editorial) and answers 500 every time, so Antares asks here instead. Read only: the profile's
// HERMES_HOME selects its skills, nothing is written, and a profile that does not exist is a 404.

const (
	hermesPython = "/opt/hermes/.venv/bin/python"
	maxSkills    = 500
	maxSkillsOut = 512 << 10
)

// skillsScript runs inside the Hermes container as the hermes user with HERMES_HOME set to the profile. argv: none.
// Exit 3: no such profile.
const skillsScript = `
import json, os, sys
home = os.environ.get("HERMES_HOME", "")
if not home or not os.path.isdir(home) or os.path.islink(home):
    sys.exit(3)
from tools.skills_tool import find_all_skills, sort_skills
out = []
for s in sort_skills(find_all_skills(skip_disabled=False))[:500]:
    if not isinstance(s, dict) or not isinstance(s.get("name"), str):
        continue
    out.append({k: str(s.get(k) or "")[:300] for k in ("name", "description", "category")})
sys.stdout.write(json.dumps({"object": "list", "data": out}))
`

// skillsCmd sets HERMES_HOME to the profile's own directory and runs the lister from the Hermes code root.
const skillsCmd = `cd /opt/hermes && HERMES_HOME="$1/$2" exec "$3" -c "$4"`

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

func (s *server) skills(w http.ResponseWriter, r *http.Request) {
	slug, _, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := opCtx(r) // a read: no mutation lock
	defer cancel()
	out, _, err := s.h.r.Run(ctx, "", "sh", "-c", skillsCmd, "sh", profilesDir, slug, hermesPython, skillsScript)
	var ee interface{ ExitCode() int }
	switch {
	case err == nil:
	case errors.As(err, &ee) && ee.ExitCode() == 3:
		writeErr(w, http.StatusNotFound, errNoProfile.Error())
		return
	default:
		writeErr(w, http.StatusBadGateway, "listing the profile's skills failed")
		return
	}
	var d struct {
		Object string       `json:"object"`
		Data   []skillEntry `json:"data"`
	}
	if len(out) > maxSkillsOut || json.Unmarshal([]byte(out), &d) != nil || d.Object != "list" {
		writeErr(w, http.StatusBadGateway, "the skill lister answered in an unexpected shape")
		return
	}
	if len(d.Data) > maxSkills {
		d.Data = d.Data[:maxSkills]
	}
	if d.Data == nil {
		d.Data = []skillEntry{}
	}
	writeJSON(w, http.StatusOK, d)
}
