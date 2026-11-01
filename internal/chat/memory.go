package chat

import (
	"net/http"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// getMemory answers GET /api/me/memory: what the assistant remembers in the caller's own workspace, read only (#248).
// Hermes v0.22.5 has no memory route, so the provisioning helper reads the profile bound to this account on the
// server; nothing the client sends names a profile.
func (s *Service) getMemory(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if s.memory == nil {
		upstreamError(w, &hermes.Error{Code: hermes.CodeCapabilityNA})
		return
	}
	m, err := s.memory.Memory(r.Context(), b.Profile)
	if err != nil {
		upstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
