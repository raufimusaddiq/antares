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

// capsNegTTL is how long an inventory whose skills helper errored is reused. A failure is not an answer, so it
// is retried soon, but not on every call: a persistently failing helper costs one exec per capsNegTTL.
const capsNegTTL = 5 * time.Second

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
	// The skills fallback runs a helper exec on every call because Hermes' skills route always fails, so it
	// belongs inside the cached answer, not after it.
	at := s.now()
	if s.fillSkills(ctx, b, &inv) {
		at = at.Add(-(capsTTL - capsNegTTL)) // expires after capsNegTTL, not capsTTL
	}
	s.capsMu.Lock()
	if s.caps == nil {
		s.caps = map[string]capsEntry{}
	}
	s.caps[b.Profile] = capsEntry{inv: inv, at: at}
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
// leaves Hermes' reason in place, so nothing is reported available that was not read. It reports whether the helper
// errored: "found none" is a valid answer, an error is not and must not be cached for long.
func (s *Service) fillSkills(ctx context.Context, b hermes.Binding, inv *hermes.Inventory) (failed bool) {
	if s.skills == nil {
		return false
	}
	for i, c := range inv.Capabilities {
		if c.ID != hermes.CapSkills || c.Status == hermes.StatusAvailable {
			continue
		}
		list, err := s.skills.Skills(ctx, b.Profile)
		if err != nil {
			return true
		}
		if len(list) == 0 {
			return false
		}
		inv.Skills = list
		inv.Capabilities[i] = hermes.Capability{ID: hermes.CapSkills, Label: c.Label, Status: hermes.StatusAvailable}
		return false
	}
	return false
}
