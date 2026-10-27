package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
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
// default, no constant. The limit is per user: admission sums the reported
// input+output tokens of all the user's chats, so a new chat does not reset it (the
// ticket and PRD §24 model the budget per user). It is a lifetime total with no reset
// window; Hermes' input already includes cache reads, so it caps tokens, not spend.
// The user can raise or clear it at any time to continue.
const (
	minTokenBudget int64 = 10_000
	maxTokenBudget int64 = 1_000_000_000
	budgetBasis          = "input + output tokens reported across all your chats; input includes cache reads"
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
	// Context keeps three things apart (issue #165): Tokens above is lifetime use summed over replies;
	// WindowTokens is the size the provider publishes for the last model; occupancy is how much of that
	// window the conversation fills now. Hermes reports the first two and not the third (its sessions and
	// run terminals carry cumulative input/output only), so Occupancy is UNKNOWN and CarriedTokens null:
	// never 0, never estimated from the lifetime total.
	Context struct {
		WindowTokens  *int64 `json:"windowTokens"`  // the provider's published size for the last model; null if unknown
		Occupancy     string `json:"occupancy"`     // UNKNOWN; REPORTED only when an upstream field says how full the window is
		CarriedTokens *int64 `json:"carriedTokens"` // set only when Occupancy is REPORTED
	} `json:"context"`
	Cost struct {
		Estimated    bool     `json:"estimated"`
		Currency     string   `json:"currency"`
		Amount       *float64 `json:"amount"` // null when no reported run could be priced
		UnpricedRuns int      `json:"unpricedRuns"`
		// UnknownModelRuns are reported replies whose model Hermes did not say (a reply
		// recovered by polling): they cannot be priced, and are also in UnpricedRuns.
		UnknownModelRuns int    `json:"unknownModelRuns"`
		PriceListDate    string `json:"priceListDate"`
	} `json:"cost"`
	// Budget: LimitTokens is null when the user set none. UsedTokens is null while no
	// finished reply reported usage: unknown, not zero. Enforced is true only when a
	// limit is set and usage is known; otherwise the limit, if any, is not being applied.
	Budget struct {
		Scope       string `json:"scope"` // what the limit and usedTokens count: all of the user's chats
		LimitTokens *int64 `json:"limitTokens"`
		UsedTokens  *int64 `json:"usedTokens"`
		UsageKnown  bool   `json:"usageKnown"`
		Enforced    bool   `json:"enforced"`
		Reached     bool   `json:"reached"`
		Basis       string `json:"basis"`
		AtLimit     string `json:"atLimit"`
	} `json:"budget"`
	// Turns is per-reply usage for the chat's newest finished runs, oldest first.
	// Tokens is null for a reply that reported none.
	Turns          []turnView `json:"turns"`
	TurnsTruncated bool       `json:"turnsTruncated"`
}

type turnView struct {
	RunID  string      `json:"runId"`
	Status string      `json:"status"`
	Model  string      `json:"model,omitempty"`
	Tokens *tokensView `json:"tokens"`
}

type tokensView struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

const atLimit = "new messages are refused until the limit is raised or removed; a reply already running finishes"

func buildUsage(chatID string, cu store.ChatUsage, limit, userUsed int64, userKnown bool) usageView {
	var v usageView
	v.Context.Occupancy = "UNKNOWN"
	v.ChatID = chatID
	v.Runs.Finished, v.Runs.Reported = cu.Finished, cu.Reported
	v.Cost.Estimated, v.Cost.Currency = true, priceCurrency
	v.Cost.PriceListDate = priceListDate
	v.Budget.Basis, v.Budget.AtLimit, v.Budget.Scope = budgetBasis, atLimit, "all your chats"
	if limit > 0 {
		v.Budget.LimitTokens = &limit
	}
	// The budget is the user's, across chats; it does not depend on this chat having usage.
	if userKnown {
		v.Budget.UsedTokens, v.Budget.UsageKnown = &userUsed, true
		if limit > 0 {
			v.Budget.Enforced = true
			v.Budget.Reached = userUsed >= limit
		}
	}
	v.Turns, v.TurnsTruncated = []turnView{}, cu.TurnsTruncated
	for _, t := range cu.Turns {
		tv := turnView{RunID: t.RunID, Status: t.Status, Model: t.Model}
		if t.Reported {
			tv.Tokens = &tokensView{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Total: t.Input + t.Output}
		}
		v.Turns = append(v.Turns, tv)
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
			if m.Model == "" {
				v.Cost.UnknownModelRuns += m.Runs
			}
		}
	}
	t.Total = t.Input + t.Output
	v.Tokens = &t
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
	used, known, err := store.UserReportedTokens(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "user usage", err)
		return
	}
	writeJSON(w, http.StatusOK, buildUsage(chatID, cu, limit, used, known))
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
		return
	}
	// The model is an upstream string, so it is not logged: it is stored with the run and shown to its owner.
	log.Printf("run usage run=%s input_tokens=%d output_tokens=%d cache_read_tokens=%d", runID, u.Input, u.Output, u.CacheRead)
}

type budgetView struct {
	LimitTokens *int64 `json:"limitTokens"` // null: no limit (the default)
	Scope       string `json:"scope"`
	Basis       string `json:"basis"`
	Min         int64  `json:"minTokens"`
	Max         int64  `json:"maxTokens"`
}

func budgetOf(limit int64) budgetView {
	v := budgetView{Scope: "all your chats", Basis: budgetBasis, Min: minTokenBudget, Max: maxTokenBudget}
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
	// limitTokens must be present: null clears the limit, an integer in range sets it,
	// anything else (absent, a string, a fraction, out of range) is refused and changes nothing.
	var in struct {
		LimitTokens json.RawMessage `json:"limitTokens"`
	}
	if !decode(w, r, &in) {
		return
	}
	var limit int64
	switch raw := strings.TrimSpace(string(in.LimitTokens)); {
	case raw == "null":
	case raw == "":
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "limitTokens is required: a whole number of tokens, or null for no limit")
		return
	default:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < minTokenBudget || n > maxTokenBudget {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "limitTokens must be null (no limit) or a whole number between 10,000 and 1,000,000,000")
			return
		}
		limit = n
	}
	if err := store.SetTokenBudget(r.Context(), s.db, uid, limit, s.now()); err != nil {
		internalError(w, "set token budget", err)
		return
	}
	writeJSON(w, http.StatusOK, budgetOf(limit))
}
