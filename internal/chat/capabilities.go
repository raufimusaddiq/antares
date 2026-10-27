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
	jobs := hermes.Capability{ID: "jobs", Label: "Scheduled tasks", Status: hermes.StatusAvailable}
	if !s.jobs {
		jobs = hermes.Capability{ID: "jobs", Label: "Scheduled tasks", Status: hermes.StatusUnsupported, Reason: "Switched off on this server until their isolation is proven on the deployed assistant"}
	}
	inv.Capabilities = append(inv.Capabilities, jobs)
	writeJSON(w, http.StatusOK, inv)
}
