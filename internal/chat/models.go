package chat

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// The model picker and provider quotas (PRD §27.1 LLM-*, §27.2 CTX-004). Everything
// here is read from Hermes' GET /api/model/options for the caller's own profile; the
// browser never names a profile, a route or a key. What Hermes does not report is null
// in the answer, and the UI renders null as unavailable, never as a default or zero.

type modelsView struct {
	Source    string         `json:"source"`
	FetchedAt string         `json:"fetchedAt"`
	Default   *defaultView   `json:"default"`
	Providers []providerView `json:"providers"`
	// ReasoningEfforts is the ladder Hermes accepts; per-model support is not reported.
	ReasoningEfforts []string `json:"reasoningEfforts"`
	// ContextWindow is not in Hermes' inventory: always "unavailable" here.
	ContextWindow string `json:"contextWindow"`
}

type defaultView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type providerView struct {
	Slug      string      `json:"slug"`
	Name      string      `json:"name"`
	Available bool        `json:"available"`
	Reason    string      `json:"reason,omitempty"` // Hermes' own warning text, for an unavailable provider
	Models    []modelView `json:"models"`
	Quota     quotaView   `json:"quota"`
}

type modelView struct {
	ID        string     `json:"id"`
	Available bool       `json:"available"`
	Reasoning *bool      `json:"reasoning"`   // null: Hermes did not say
	Priority  *bool      `json:"serviceTier"` // priority tier supported; null: Hermes did not say
	Price     *priceView `json:"price"`
}

type priceView struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	Cache  string `json:"cache,omitempty"`
	Free   bool   `json:"free"`
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

func buildModels(inv hermes.Inventory, now time.Time) modelsView {
	v := modelsView{Source: "GET /api/model/options", FetchedAt: now.UTC().Format(time.RFC3339),
		Providers: []providerView{}, ReasoningEfforts: hermes.ReasoningEfforts, ContextWindow: "unavailable"}
	if inv.Model != "" {
		v.Default = &defaultView{Provider: inv.Provider, Model: inv.Model}
	}
	for _, p := range inv.Providers {
		pv := providerView{Slug: p.Slug, Name: p.Name, Available: p.Authenticated, Models: []modelView{}}
		if !p.Authenticated {
			pv.Reason = p.Warning
		}
		for _, id := range p.Models {
			mv := modelView{ID: id, Available: p.Authenticated}
			if c, ok := p.Caps[id]; ok {
				mv.Reasoning, mv.Priority = c.Reasoning, c.Fast
			}
			if pr, ok := p.Pricing[id]; ok {
				mv.Price = &priceView{Input: pr.Input, Output: pr.Output, Cache: pr.Cache, Free: pr.Free}
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
	}
	return v
}

// GET /api/models[?refresh=1]
func (s *Service) listModels(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	inv, err := s.inventory(r, uid, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		s.gateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildModels(inv, s.now()))
}

func (s *Service) inventory(r *http.Request, uid string, refresh bool) (hermes.Inventory, error) {
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		return hermes.Inventory{}, err
	}
	return s.hx.ModelOptions(r.Context(), b, refresh)
}

type modelChoiceBody struct {
	Reset           bool   `json:"reset"`
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

// PUT /api/chats/{chatId}/model — saves the choice for later runs of this chat; earlier
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
		inv, err := s.inventory(r, uid, true) // refresh-on-pick: the check must not use a stale catalog
		if err != nil {
			s.gateError(w, err)
			return
		}
		if msg := validateChoice(inv, body); msg != "" {
			apiError(w, http.StatusUnprocessableEntity, "MODEL_UNAVAILABLE", msg)
			return
		}
		choice = store.ChatModel{Provider: body.Provider, Model: body.Model, Reasoning: body.ReasoningEffort, ServiceTier: body.ServiceTier}
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

// validateChoice returns "" if Hermes lists the provider as available, the model under
// it, and (when set) the option as supported for that model; otherwise why not.
func validateChoice(inv hermes.Inventory, b modelChoiceBody) string {
	if b.Provider == "" || b.Model == "" {
		return "Choose a provider and a model"
	}
	i := slices.IndexFunc(inv.Providers, func(p hermes.InventoryProvider) bool { return p.Slug == b.Provider })
	if i < 0 {
		return "That provider is not offered to your workspace"
	}
	p := inv.Providers[i]
	if !p.Authenticated {
		return "That provider is not configured"
	}
	if !slices.Contains(p.Models, b.Model) {
		return "That model is not available from this provider"
	}
	caps := p.Caps[b.Model]
	if b.ReasoningEffort != "" {
		if !slices.Contains(hermes.ReasoningEfforts, b.ReasoningEffort) {
			return "That reasoning effort is not one the assistant accepts"
		}
		if caps.Reasoning == nil || !*caps.Reasoning {
			return "This model does not list reasoning support"
		}
	}
	if b.ServiceTier != "" {
		if b.ServiceTier != "priority" {
			return "That service tier is not one the assistant accepts"
		}
		if caps.Fast == nil || !*caps.Fast {
			return "This model does not list a priority tier"
		}
	}
	return ""
}
