package chat

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// The model picker and per-chat model settings (PRD §26.1, §27.1 LLM-*, §29.1, issue #115).
//
// The catalogue is Kenari's (internal/kenari), the provider Hermes is configured with: it names
// the models, their modalities, context window, price and reasoning levels. Hermes' own
// inventory (GET /api/model/options) is only the transport: it names the profile's default,
// says whether the provider is usable, and reports quota. A model Hermes' inventory does not
// list is therefore not a model Kenari lacks. Hermes runs the choice (POST /v1/runs body
// provider + model); Antares calls no provider. What Kenari does not publish is null in the
// answer, and the UI renders null as unknown, never as a default or zero.

const catalogueSource = "kenari"

// tierUnsupported is why no service tier is offered: Kenari's catalogue publishes none, and
// Hermes' per-model "fast" flag describes its own providers, not a Kenari model.
const tierUnsupported = "Kenari does not publish a service tier for any model, so none can be selected"

type modelsView struct {
	Source    string `json:"source"`    // where the models and their metadata come from
	FetchedAt string `json:"fetchedAt"` // when Kenari was last read
	// Stale is true when that read is older than the freshness window because the latest read failed.
	Stale     bool           `json:"stale"`
	Default   *defaultView   `json:"default"`
	Providers []providerView `json:"providers"`
	// Unsupported names each control that is not offered, with the reason.
	Unsupported map[string]string `json:"unsupported"`
}

type defaultView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// providerView is the Hermes provider the models run through (the profile's own, chosen by
// the server), not a vendor list.
type providerView struct {
	Slug      string      `json:"slug"`
	Name      string      `json:"name"`
	Available bool        `json:"available"`
	Reason    string      `json:"reason,omitempty"`
	Models    []modelView `json:"models"`
	Quota     quotaView   `json:"quota"`
}

type modelView struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Available bool   `json:"available"`
	Hidden    bool   `json:"hidden"` // the caller's own preference
	// Reasoning is null when Kenari did not say. ReasoningEfforts are the levels Kenari publishes
	// for the model that Hermes also accepts; empty means none can be chosen.
	Reasoning        *bool           `json:"reasoning"`
	ReasoningEfforts []string        `json:"reasoningEfforts"`
	Modalities       *modalitiesView `json:"modalities"` // null: not published
	ContextWindow    *float64        `json:"contextWindow"`
	Price            *priceView      `json:"price"`
	SunsetAt         string          `json:"sunsetAt,omitempty"`
}

type modalitiesView struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// priceView is Kenari's published price, in its own currency and unit; nothing is converted.
type priceView struct {
	Source    string   `json:"source"`
	Currency  string   `json:"currency"`
	Unit      string   `json:"unit"`
	Free      bool     `json:"free"`
	Input     *float64 `json:"input"`
	Output    *float64 `json:"output"`
	CacheRead *float64 `json:"cacheRead"`
}

// quotaView: State is "AVAILABLE" with windows, or "PROVIDER_USAGE_UNAVAILABLE" (CTX-004).
type quotaView struct {
	State     string       `json:"state"`
	Windows   []windowView `json:"windows,omitempty"`
	LimitedAt string       `json:"limitedUntil,omitempty"`
}

type windowView struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
	Scope       string  `json:"scope,omitempty"`
}

// transport is what Hermes contributes to a Kenari model: the provider the profile runs
// through. It is usable only if Hermes lists it and marks it authenticated.
func transport(inv hermes.ModelCatalog) (hermes.CatalogProvider, bool) {
	i := slices.IndexFunc(inv.Providers, func(p hermes.CatalogProvider) bool { return p.Slug == inv.Provider })
	if inv.Provider == "" || i < 0 {
		return hermes.CatalogProvider{}, false
	}
	return inv.Providers[i], inv.Providers[i].Authenticated
}

// selectable is true for a model Kenari lists for chat that has not been sunset.
func selectable(m kenari.Model, now time.Time) bool {
	if !m.Chat() {
		return false
	}
	if t, err := time.Parse(time.RFC3339, m.SunsetAt); err == nil && !t.After(now) {
		return false
	}
	return true
}

// efforts are the reasoning levels offered for m: the ones Kenari publishes AND Hermes accepts
// (hermes.ReasoningEfforts). A model that does not publish levels offers none.
func efforts(m kenari.Model) []string {
	out := []string{}
	if m.Reasoning == nil || !*m.Reasoning {
		return out
	}
	for _, l := range m.ReasoningLevels {
		if slices.Contains(hermes.ReasoningEfforts, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

func buildModels(inv hermes.ModelCatalog, cat []kenari.Model, fetched time.Time, stale bool, hidden, blocked []string, now time.Time) modelsView {
	v := modelsView{Source: catalogueSource, FetchedAt: fetched.UTC().Format(time.RFC3339), Stale: stale, Providers: []providerView{},
		Unsupported: map[string]string{"serviceTier": tierUnsupported}}
	if inv.Model != "" {
		v.Default = &defaultView{Provider: inv.Provider, Model: inv.Model}
	}
	p, usable := transport(inv)
	pv := providerView{Slug: inv.Provider, Name: p.Name, Available: usable, Models: []modelView{}}
	if pv.Name == "" {
		pv.Name = inv.Provider
	}
	if !usable {
		pv.Reason = p.Warning
		if pv.Reason == "" {
			pv.Reason = "The assistant has no working provider for these models"
		}
	}
	for _, m := range cat {
		if !selectable(m, now) || slices.Contains(blocked, m.ID) { // global policy always wins
			continue
		}
		mv := modelView{ID: m.ID, Name: m.Name, Available: usable, Hidden: slices.Contains(hidden, m.ID), Reasoning: m.Reasoning,
			ReasoningEfforts: efforts(m), ContextWindow: m.ContextLength, SunsetAt: m.SunsetAt}
		if m.HasModalities {
			mv.Modalities = &modalitiesView{Input: m.Modalities.Input, Output: m.Modalities.Output}
		}
		if pr := m.Pricing; pr != nil {
			mv.Price = &priceView{Source: catalogueSource, Currency: pr.Currency, Unit: pr.Unit, Free: pr.Free, Input: pr.Input, Output: pr.Output, CacheRead: pr.CacheRead}
		}
		pv.Models = append(pv.Models, mv)
	}
	pv.Quota = quotaView{State: "PROVIDER_USAGE_UNAVAILABLE", LimitedAt: p.LimitResetsAt}
	if len(p.Quota) > 0 {
		pv.Quota.State = "AVAILABLE"
		for _, w := range p.Quota {
			pv.Quota.Windows = append(pv.Quota.Windows, windowView{Label: w.Label, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt, Scope: w.Scope})
		}
	}
	v.Providers = append(v.Providers, pv)
	return v
}

// catalogue reads Kenari's rows. A catalogue that is not configured or cannot be read is an
// explicit answer: the picker never falls back to another list.
func (s *Service) catalogue(ctx context.Context) ([]kenari.Model, error) {
	if s.kenari == nil {
		return nil, kenari.ErrUnavailable
	}
	return s.kenari.List(ctx)
}

func catalogueDown(w http.ResponseWriter) {
	apiError(w, http.StatusServiceUnavailable, "CATALOGUE_UNAVAILABLE", "The model catalogue could not be read")
}

// GET /api/models[?refresh=1]
func (s *Service) listModels(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	inv, err := s.inventory(r, uid, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		s.gateError(w, err)
		return
	}
	if s.kenari == nil {
		catalogueDown(w)
		return
	}
	cat, fetched, stale, err := s.kenari.Snapshot(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		catalogueDown(w)
		return
	}
	hidden, err := store.HiddenModels(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "hidden models", err)
		return
	}
	blocked, err := store.BlockedModels(r.Context(), s.db)
	if err != nil {
		internalError(w, "blocked models", err)
		return
	}
	writeJSON(w, http.StatusOK, buildModels(inv, cat, fetched, stale, hidden, blocked, s.now()))
}

func (s *Service) inventory(r *http.Request, uid string, refresh bool) (hermes.ModelCatalog, error) {
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		return hermes.ModelCatalog{}, err
	}
	return s.hx.ModelOptions(r.Context(), b, refresh)
}

type modelChoiceBody struct {
	Reset bool `json:"reset"`
	// Provider is accepted only as an echo of the one the server resolved; any other value is refused.
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	ServiceTier     string `json:"serviceTier"`
}

type modelChoiceView struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	ServiceTier     string `json:"serviceTier,omitempty"`
}

// choiceView is null when the chat uses the profile's default.
func choiceView(m store.ChatModel) *modelChoiceView {
	if m.Model == "" {
		return nil
	}
	return &modelChoiceView{Provider: m.Provider, Model: m.Model, ReasoningEffort: m.Reasoning, ServiceTier: m.ServiceTier}
}

// PUT /api/chats/{chatId}/settings (also /model) — saves the choice for later runs of this chat; earlier
// turns are untouched. {"reset":true} returns to the default. A choice is accepted only
// if the caller's own Hermes profile lists it as available right now.
func (s *Service) putChatModel(w http.ResponseWriter, r *http.Request) {
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
	var body modelChoiceBody
	if !decode(w, r, &body) {
		return
	}
	var choice store.ChatModel
	if body.Reset {
		if body.Provider != "" || body.Model != "" || body.ReasoningEffort != "" || body.ServiceTier != "" {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "reset cannot be combined with a choice")
			return
		}
	} else {
		inv, err := s.inventory(r, uid, true) // refresh-on-pick: the transport check must not use a stale answer
		if err != nil {
			s.gateError(w, err)
			return
		}
		cat, err := s.catalogue(r.Context())
		if err != nil {
			catalogueDown(w)
			return
		}
		blocked, err := store.BlockedModels(r.Context(), s.db)
		if err != nil {
			internalError(w, "blocked models", err)
			return
		}
		provider, code, msg := validateChoice(inv, cat, blocked, body, s.now())
		if msg != "" {
			apiError(w, http.StatusUnprocessableEntity, code, msg)
			return
		}
		choice = store.ChatModel{Provider: provider, Model: body.Model, Reasoning: body.ReasoningEffort, ServiceTier: body.ServiceTier}
	}
	if err := store.SetChatModel(r.Context(), s.db, uid, chatID, choice); errors.Is(err, store.ErrNoChat) {
		notFound(w)
		return
	} else if err != nil {
		internalError(w, "set chat model", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chatId": chatID, "model": choiceView(choice)})
}

// validateChoice checks a choice against Kenari's catalogue and the profile's transport. It
// returns the Hermes provider to run through, or an error code and why not. Nothing is coerced:
// an effort Kenari does not publish for the model, or any service tier, is refused.
func validateChoice(inv hermes.ModelCatalog, cat []kenari.Model, blocked []string, b modelChoiceBody, now time.Time) (provider, code, msg string) {
	if b.Model == "" {
		return "", "MODEL_UNAVAILABLE", "Choose a model"
	}
	i := slices.IndexFunc(cat, func(m kenari.Model) bool { return m.ID == b.Model })
	if i < 0 || !selectable(cat[i], now) || slices.Contains(blocked, b.Model) {
		return "", "MODEL_UNAVAILABLE", "That model is not available to you"
	}
	p, usable := transport(inv)
	if !usable {
		return "", "MODEL_TRANSPORT_UNAVAILABLE", "The assistant has no working provider to run this model, so it was not selected"
	}
	if b.Provider != "" && b.Provider != p.Slug {
		return "", "MODEL_UNAVAILABLE", "The provider is chosen by the server"
	}
	if b.ReasoningEffort != "" && !slices.Contains(efforts(cat[i]), b.ReasoningEffort) {
		return "", "OPTION_UNSUPPORTED", "That reasoning effort is not published for this model"
	}
	if b.ServiceTier != "" {
		return "", "OPTION_UNSUPPORTED", tierUnsupported
	}
	return p.Slug, "", ""
}
