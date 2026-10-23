package chat

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Price list. Taken 2026-10-23 from https://kenari.id/v1/models, the provider
// Hermes is configured with (base_url https://kenari.id/v1, model
// deepseek-v4-1-flash). The provider quotes IDR in micro-IDR per 1M tokens and
// publishes no cache-write price. Only models whose price was read there are
// listed: any other model is shown as unpriced, never guessed. Re-read the URL
// and change this table together with its date when the provider changes.
const (
	priceListDate = "2026-10-23"
	priceCurrency = "IDR"
)

type price struct {
	inMicro, outMicro, cacheReadMicro float64 // micro-IDR per 1M tokens
	window                            int64   // context_length, tokens
}

var prices = map[string]price{
	"deepseek-v4-1-flash": {inMicro: 2_750_000_000, outMicro: 5_500_000_000, cacheReadMicro: 65_000_000, window: 1_000_000},
}

// Token budget. There is none unless the user sets one (PUT /api/me/budget): no
// default, no constant. The limit is per chat and counts reported input+output
// tokens; Hermes' input already includes cache reads, so it caps the size of one
// conversation, not spend. The user can raise or clear it at any time to continue.
const (
	minTokenBudget int64 = 10_000
	maxTokenBudget int64 = 1_000_000_000
	budgetBasis          = "input + output tokens reported for this chat; input includes cache reads"
)

// estimateIDR prices one model's usage. Hermes' input_tokens includes cache
// reads and writes, so cache reads are priced at the cache rate and everything
// else (cache writes included, as no write price is published) at the input rate.
func estimateIDR(p price, u store.RunUsage) float64 {
	fresh := u.Input - u.CacheRead
	if fresh < 0 {
		fresh = 0
	}
	return (float64(fresh)*p.inMicro + float64(u.Output)*p.outMicro + float64(u.CacheRead)*p.cacheReadMicro) / 1e6 / 1e6
}

type usageView struct {
	ChatID string `json:"chatId"`
	// Tokens is null when no finished run reported any. Hermes reports per-run
	// totals summed over its model calls, not the size of what the model carries.
	Tokens *tokensView `json:"tokens"`
	Runs   struct {
		Finished int `json:"finished"`
		Reported int `json:"reported"`
	} `json:"runs"`
	Context struct {
		WindowTokens  *int64 `json:"windowTokens"`  // the provider's published size for the last model; null if unknown
		CarriedTokens *int64 `json:"carriedTokens"` // always null: Hermes does not report it
	} `json:"context"`
	Cost struct {
		Estimated     bool     `json:"estimated"`
		Currency      string   `json:"currency"`
		Amount        *float64 `json:"amount"` // null when no reported run could be priced
		UnpricedRuns  int      `json:"unpricedRuns"`
		PriceListDate string   `json:"priceListDate"`
	} `json:"cost"`
	// Budget: LimitTokens is null when the user set none. UsedTokens is null while no
	// finished reply reported usage: unknown, not zero. Enforced is true only when a
	// limit is set and usage is known; otherwise the limit, if any, is not being applied.
	Budget struct {
		LimitTokens *int64 `json:"limitTokens"`
		UsedTokens  *int64 `json:"usedTokens"`
		UsageKnown  bool   `json:"usageKnown"`
		Enforced    bool   `json:"enforced"`
		Reached     bool   `json:"reached"`
		Basis       string `json:"basis"`
		AtLimit     string `json:"atLimit"`
	} `json:"budget"`
}

type tokensView struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

const atLimit = "new messages are refused until the limit is raised or removed; a reply already running finishes"

func buildUsage(chatID string, cu store.ChatUsage, limit int64) usageView {
	var v usageView
	v.ChatID = chatID
	v.Runs.Finished, v.Runs.Reported = cu.Finished, cu.Reported
	v.Cost.Estimated, v.Cost.Currency = true, priceCurrency
	v.Cost.PriceListDate = priceListDate
	v.Budget.Basis, v.Budget.AtLimit = budgetBasis, atLimit
	if limit > 0 {
		v.Budget.LimitTokens = &limit
	}
	if p, ok := prices[cu.LastModel]; ok {
		w := p.window
		v.Context.WindowTokens = &w
	}
	if cu.Reported == 0 {
		return v
	}
	var t tokensView
	var sum float64
	priced := false
	for _, m := range cu.ByModel {
		t.Input += m.Input
		t.Output += m.Output
		t.CacheRead += m.CacheRead
		t.CacheWrite += m.CacheWrite
		if p, ok := prices[m.Model]; ok {
			sum += estimateIDR(p, m.RunUsage)
			priced = true
		} else {
			v.Cost.UnpricedRuns += m.Runs
		}
	}
	t.Total = t.Input + t.Output
	v.Tokens = &t
	used := t.Total
	v.Budget.UsedTokens, v.Budget.UsageKnown = &used, true
	if limit > 0 {
		v.Budget.Enforced = true
		v.Budget.Reached = used >= limit
	}
	if priced {
		v.Cost.Amount = &sum
	}
	return v
}

func (s *Service) chatUsage(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	chatID, ok := pathID(r, "chatId")
	if !ok {
		notFound(w)
		return
	}
	if _, err := store.GetChat(r.Context(), s.db, uid, chatID); errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "get chat", err)
		return
	}
	cu, err := store.GetChatUsage(r.Context(), s.db, uid, chatID)
	if err != nil {
		internalError(w, "chat usage", err)
		return
	}
	limit, err := store.TokenBudget(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "token budget", err)
		return
	}
	writeJSON(w, http.StatusOK, buildUsage(chatID, cu, limit))
}

// recordUsage stores what Hermes reported for a finished run. Nothing reported
// stores nothing: the run stays "unknown", it is not recorded as zero.
func (s *Service) recordUsage(userID, runID string, u *hermes.Usage) {
	if u == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.base), 10*time.Second)
	defer cancel()
	if err := store.RecordRunUsage(ctx, s.db, userID, runID, store.RunUsage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Model: u.Model}); err != nil {
		log.Printf("chat: run %s: could not record usage: %v", runID, err)
	}
}

type budgetView struct {
	LimitTokens *int64 `json:"limitTokens"` // null: no limit (the default)
	Scope       string `json:"scope"`
	Basis       string `json:"basis"`
	Min         int64  `json:"minTokens"`
	Max         int64  `json:"maxTokens"`
}

func budgetOf(limit int64) budgetView {
	v := budgetView{Scope: "per chat", Basis: budgetBasis, Min: minTokenBudget, Max: maxTokenBudget}
	if limit > 0 {
		v.LimitTokens = &limit
	}
	return v
}

func (s *Service) getBudget(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	limit, err := store.TokenBudget(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "token budget", err)
		return
	}
	writeJSON(w, http.StatusOK, budgetOf(limit))
}

// putBudget sets or clears the caller's own limit. Only that: the account is the
// session's, and a body naming anything else is refused by decode.
func (s *Service) putBudget(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var in struct {
		LimitTokens *int64 `json:"limitTokens"`
	}
	if !decode(w, r, &in) {
		return
	}
	var limit int64
	if in.LimitTokens != nil {
		limit = *in.LimitTokens
		if limit < minTokenBudget || limit > maxTokenBudget {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "limitTokens must be null (no limit) or between 10,000 and 1,000,000,000")
			return
		}
	}
	if err := store.SetTokenBudget(r.Context(), s.db, uid, limit, s.now()); err != nil {
		internalError(w, "set token budget", err)
		return
	}
	writeJSON(w, http.StatusOK, budgetOf(limit))
}
