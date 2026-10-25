package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/kenari"
)

// kenariUsageView is the shared provider account as Kenari reports it. Scope is always "shared-account":
// one key serves every Antares account, so nothing here is a user's, a chat's or a run's figure. State is
// NOT_CONFIGURED (no key), UNAVAILABLE (never read, or no read for over an hour) or OK; with OK, each
// section carries its own state and a section Kenari refused or could not answer has no numbers.
type kenariUsageView struct {
	State     string                          `json:"state"`
	Source    string                          `json:"source"`
	Scope     string                          `json:"scope"`
	FetchedAt string                          `json:"fetchedAt,omitempty"`
	Stale     bool                            `json:"stale"`
	Balance   *kenari.Section[kenari.Balance] `json:"balance,omitempty"`
	Quota     *kenari.Section[kenari.Quota]   `json:"quota,omitempty"`
	Usage     *kenari.Section[kenari.Usage]   `json:"usage,omitempty"`
}

const (
	usageSource = "kenari.id: GET /v1/account/balance, /v1/account/quota, /v1/usage (30 days)"
	usageScope  = "shared-account"
)

// GET /api/admin/kenari-usage[?refresh=1]. Admin only: the account is shared by every user, so its
// balance and spend are the owner's business, not a per-user figure and not something another account
// may read.
func (a *Auth) adminKenariUsage(w http.ResponseWriter, r *http.Request) {
	v := kenariUsageView{Source: usageSource, Scope: usageScope}
	if a.usage == nil {
		v.State = "NOT_CONFIGURED"
		writeJSON(w, http.StatusOK, v)
		return
	}
	s, err := a.usage.Snapshot(r.Context(), r.URL.Query().Get("refresh") == "1")
	switch {
	case errors.Is(err, kenari.ErrNotConfigured):
		v.State = "NOT_CONFIGURED"
	case err != nil:
		v.State = "UNAVAILABLE"
	default:
		v.State, v.Stale = "OK", s.Stale
		v.FetchedAt = s.FetchedAt.UTC().Format(time.RFC3339)
		v.Balance, v.Quota, v.Usage = &s.Balance, &s.Quota, &s.Usage
	}
	writeJSON(w, http.StatusOK, v)
}
