package chat

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
)

// Spending limits (PRD §21.7, UXP-009, CTX-005, UXP-ACC-011, issue #152). Beside the token limit, a user may set a daily
// and a monthly soft warning and a daily hard limit, in the price list's currency (IDR). Spend is the estimate of priced
// replies started in the current UTC day or month; a reply on a model with no known price is counted apart and never
// as zero cost. A soft limit only warns. The hard limit is a run admission gate: a free chat at or over it takes no new
// message; a reply already running finishes, and its cost may take the day past the limit (no provider reservation
// exists, so no exact ceiling is promised).

const maxSpendingIDR = 1e12

type windowSpend struct {
	Spent        *float64 `json:"spent"` // null when no reply in the window could be priced
	UnpricedRuns int      `json:"unpricedRuns"`
}

type spendingView struct {
	Currency    string      `json:"currency"`
	Day         windowSpend `json:"day"`   // the current UTC day
	Month       windowSpend `json:"month"` // the current UTC month
	DailySoft   *float64    `json:"dailySoft"`
	MonthlySoft *float64    `json:"monthlySoft"`
	DailyHard   *float64    `json:"dailyHard"`
	// Warnings are the soft limits reached ("daily", "monthly"); they never block. HardReached blocks new messages.
	Warnings    []string `json:"warnings"`
	HardReached bool     `json:"hardReached"`
}

func dayStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func monthStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// spendSince estimates the user's spend on priced replies started at or after since.
func (s *Service) spendSince(ctx context.Context, q store.Querier, uid string, since time.Time) (windowSpend, error) {
	usage, err := store.UserModelUsage(ctx, q, uid, since)
	if err != nil {
		return windowSpend{}, err
	}
	var w windowSpend
	var sum float64
	priced := false
	for _, m := range usage {
		if p, ok := prices[m.Model]; ok {
			sum += estimateIDR(p, m.RunUsage)
			priced = true
		} else {
			w.UnpricedRuns += m.Runs
		}
	}
	if priced {
		w.Spent = &sum
	}
	return w, nil
}

// spending reads through q: s.db for a view, the admission transaction for the hard-limit gate.
func (s *Service) spending(ctx context.Context, q store.Querier, uid string) (spendingView, error) {
	lim, err := store.GetSpendingLimits(ctx, q, uid)
	if err != nil {
		return spendingView{}, err
	}
	now := s.now()
	v := spendingView{Currency: priceCurrency, Warnings: []string{}}
	if v.Day, err = s.spendSince(ctx, q, uid, dayStart(now)); err != nil {
		return spendingView{}, err
	}
	if v.Month, err = s.spendSince(ctx, q, uid, monthStart(now)); err != nil {
		return spendingView{}, err
	}
	set := func(f float64) *float64 {
		if f > 0 {
			return &f
		}
		return nil
	}
	v.DailySoft, v.MonthlySoft, v.DailyHard = set(lim.DailySoft), set(lim.MonthlySoft), set(lim.DailyHard)
	reached := func(w windowSpend, limit float64) bool { return limit > 0 && w.Spent != nil && *w.Spent >= limit }
	if reached(v.Day, lim.DailySoft) {
		v.Warnings = append(v.Warnings, "daily")
	}
	if reached(v.Month, lim.MonthlySoft) {
		v.Warnings = append(v.Warnings, "monthly")
	}
	v.HardReached = reached(v.Day, lim.DailyHard)
	return v, nil
}

// errSpendLimit is the hard-limit gate's refusal, decided inside the admission transaction.
var errSpendLimit = errors.New("chat: daily hard spending limit reached")

// hardLimitGate refuses a new run while the user's spend today is at or over their daily hard limit.
func (s *Service) hardLimitGate(uid string) func(context.Context, store.Querier) error {
	return func(ctx context.Context, q store.Querier) error {
		sp, err := s.spending(ctx, q, uid)
		if err != nil {
			return err
		}
		if sp.HardReached {
			return errSpendLimit
		}
		return nil
	}
}

func (s *Service) getSpending(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	v, err := s.spending(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "spending", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// putSpending replaces the caller's own three limits. Each member must be present: null clears it, a whole positive
// rupiah amount up to 10^12 sets it (whole, so the form shows and resends exactly what is stored); anything else is refused and nothing changes. Repeating a request is harmless.
func (s *Service) putSpending(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var in struct {
		DailySoft   json.RawMessage `json:"dailySoft"`
		MonthlySoft json.RawMessage `json:"monthlySoft"`
		DailyHard   json.RawMessage `json:"dailyHard"`
	}
	if !decode(w, r, &in) {
		return
	}
	var l store.SpendingLimits
	for _, f := range []struct {
		raw json.RawMessage
		dst *float64
	}{{in.DailySoft, &l.DailySoft}, {in.MonthlySoft, &l.MonthlySoft}, {in.DailyHard, &l.DailyHard}} {
		if len(f.raw) == 0 {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "dailySoft, monthlySoft and dailyHard are required: a positive amount, or null for none")
			return
		}
		if string(f.raw) == "null" {
			continue
		}
		var n float64
		if json.Unmarshal(f.raw, &n) != nil || math.IsNaN(n) || n <= 0 || n > maxSpendingIDR || n != math.Trunc(n) {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "A limit must be null (none) or a whole positive rupiah amount up to 1,000,000,000,000")
			return
		}
		*f.dst = n
	}
	if err := store.SetSpendingLimits(r.Context(), s.db, uid, l, s.now()); err != nil {
		internalError(w, "set spending limits", err)
		return
	}
	s.getSpending(w, r)
}
