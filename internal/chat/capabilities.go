package chat

import (
	"context"
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
)

// capabilities answers GET /api/capabilities: what the deployed Hermes can do for
// this account, discovered live under the caller's own binding. Nothing is defaulted here, so an
// account without a healthy binding gets no inventory (default deny) and a capability Hermes did not
// confirm is reported unsupported.
func (s *Service) capabilities(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		upstreamError(w, err)
		return
	}
	inv, err := s.discover(r.Context(), b)
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

// capsTTL is how long a discovered inventory may be reused for the same profile. Discovery is several
// round trips to Hermes and its answer changes only when the deployment or its credentials do, so a
// short reuse removes the cost from every page load. The key is the backend-selected profile, never
// anything a request supplies, and the gate runs before the cache: a revoked, suspended or unbound
// account never reaches it, so a cached inventory can never outlive the right to ask for one.
const capsTTL = 30 * time.Second

type capsEntry struct {
	inv hermes.Inventory
	at  time.Time
}

// discover returns the binding's inventory, reusing one discovered within capsTTL. The caller gets its
// own copy: fillSkills and the jobs capability edit it, and the cached one must not change.
func (s *Service) discover(ctx context.Context, b hermes.Binding) (hermes.Inventory, error) {
	s.capsMu.Lock()
	e, ok := s.caps[b.Profile]
	s.capsMu.Unlock()
	if ok && s.now().Sub(e.at) < capsTTL {
		return cloneInv(e.inv), nil
	}
	inv, err := s.hx.Discover(ctx, b)
	if err != nil {
		return inv, err
	}
	s.capsMu.Lock()
	if s.caps == nil {
		s.caps = map[string]capsEntry{}
	}
	s.caps[b.Profile] = capsEntry{inv: inv, at: s.now()}
	s.capsMu.Unlock()
	return cloneInv(inv), nil
}

// cloneInv copies every slice a caller may edit, nested ones included. Empty slices stay non-nil: the
// browser's parser and the golden fixture both expect [] rather than null.
func cloneInv(inv hermes.Inventory) hermes.Inventory {
	out := hermes.Inventory{
		Capabilities: append([]hermes.Capability{}, inv.Capabilities...),
		Toolsets:     append([]hermes.Toolset{}, inv.Toolsets...),
		Providers:    append([]hermes.Provider{}, inv.Providers...),
		Skills:       append([]hermes.Skill{}, inv.Skills...),
	}
	for i := range out.Toolsets {
		out.Toolsets[i].Tools = append([]string{}, inv.Toolsets[i].Tools...)
	}
	for i := range out.Providers {
		out.Providers[i].Models = append([]string{}, inv.Providers[i].Models...)
	}
	return out
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
