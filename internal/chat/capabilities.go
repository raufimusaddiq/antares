package chat

import "net/http"

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
	writeJSON(w, http.StatusOK, inv)
}
