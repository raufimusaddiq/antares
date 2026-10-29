package chat

import (
	"net/http"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// capabilities answers GET /api/capabilities: what the deployed Hermes can do for
// this account, discovered live under the caller's own binding. Nothing is cached
// or defaulted here, so an account without a healthy binding gets no inventory
// (default deny) and a capability Hermes did not confirm is reported unsupported.
func (s *Service) capabilities(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		upstreamError(w, err)
		return
	}
	inv, err := s.hx.Discover(r.Context(), b)
	if err != nil {
		upstreamError(w, err)
		return
	}
	s.fillSkills(r, b, &inv)
	jobs := hermes.Capability{ID: "jobs", Label: "Scheduled tasks", Status: hermes.StatusAvailable}
	if !s.jobs {
		jobs = hermes.Capability{ID: "jobs", Label: "Scheduled tasks", Status: hermes.StatusUnsupported, Reason: "Switched off on this server until their isolation is proven on the deployed assistant"}
	}
	inv.Capabilities = append(inv.Capabilities, jobs)
	writeJSON(w, http.StatusOK, inv)
}

// fillSkills covers Hermes v0.22.5's skills route, which fails on every call: when Hermes did not report the skills,
// the provisioning helper lists the caller's own profile (never another) with Hermes' own lister. A failure there
// leaves Hermes' reason in place, so nothing is reported available that was not read.
func (s *Service) fillSkills(r *http.Request, b hermes.Binding, inv *hermes.Inventory) {
	if s.skills == nil {
		return
	}
	for i, c := range inv.Capabilities {
		if c.ID != hermes.CapSkills || c.Status == hermes.StatusAvailable {
			continue
		}
		list, err := s.skills.Skills(r.Context(), b.Profile)
		if err != nil || len(list) == 0 {
			return
		}
		inv.Skills = list
		inv.Capabilities[i] = hermes.Capability{ID: hermes.CapSkills, Label: c.Label, Status: hermes.StatusAvailable}
		return
	}
}
