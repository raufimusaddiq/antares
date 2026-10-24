package chat

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Per-chat settings and own model visibility (PRD §29.1, LLM-ACC-002/003, issue #115).

// GET /api/chats/{chatId}/settings — what this chat saved, and what is not offered. No Hermes
// request is made: it reads the owner's row and Kenari's catalogue.
func (s *Service) getChatSettings(w http.ResponseWriter, r *http.Request) {
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
	m, err := store.GetChatModel(r.Context(), s.db, uid, chatID)
	if err != nil {
		internalError(w, "get chat model", err)
		return
	}
	out := map[string]any{"chatId": chatID, "model": choiceView(m), "source": catalogueSource,
		"unsupported": map[string]string{"serviceTier": tierUnsupported}, "state": "DEFAULT", "reasoningEfforts": []string{}}
	if m.Model != "" {
		state, row := s.savedChoiceState(r, m.Model)
		out["state"] = state
		if state == "AVAILABLE" {
			out["reasoningEfforts"] = efforts(row)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// savedChoiceState says whether the model a chat saved can still be run: AVAILABLE, BLOCKED (admin
// policy), NOT_LISTED (Kenari no longer offers it), or UNKNOWN (the catalogue cannot be read).
// A saved model is never swapped for another: BLOCKED and NOT_LISTED refuse the send.
func (s *Service) savedChoiceState(r *http.Request, model string) (string, kenari.Model) {
	blocked, err := store.BlockedModels(r.Context(), s.db)
	if err != nil {
		return "UNKNOWN", kenari.Model{}
	}
	if slices.Contains(blocked, model) {
		return "BLOCKED", kenari.Model{}
	}
	cat, err := s.catalogue(r.Context())
	if err != nil {
		return "UNKNOWN", kenari.Model{}
	}
	i := slices.IndexFunc(cat, func(m kenari.Model) bool { return m.ID == model })
	if i < 0 || !selectable(cat[i], s.now()) {
		return "NOT_LISTED", kenari.Model{}
	}
	return "AVAILABLE", cat[i]
}

// effectiveDefault is the model Hermes would run for a chat with no saved choice. The global
// block list applies to it exactly as it does to a chosen model.
func (s *Service) effectiveDefault(ctx context.Context, b hermes.Binding) (provider, model string, err error) {
	inv, err := s.hx.ModelOptions(ctx, b, false)
	if err != nil {
		return "", "", err
	}
	if inv.Model == "" {
		return "", "", errNoDefault
	}
	return inv.Provider, inv.Model, nil
}

var errNoDefault = errors.New("chat: the assistant did not report a default model")

// refuseUnrunnableChoice answers 422 MODEL_UNAVAILABLE, before a run is admitted, when the model
// the next run would use is blocked by policy or (for a saved choice) gone. That is the chat's
// saved model, else the profile default: a default-model chat is not exempt. When a block list
// exists and the default cannot be determined, the send is refused rather than assumed allowed.
// It reports whether it answered.
func (s *Service) refuseUnrunnableChoice(w http.ResponseWriter, r *http.Request, b hermes.Binding, uid, chatID string) bool {
	m, err := store.GetChatModel(r.Context(), s.db, uid, chatID)
	if err != nil {
		internalError(w, "get chat model", err)
		return true
	}
	const gone = "This chat's model is no longer available. Choose another model; nothing was sent"
	if m.Model != "" {
		if state, _ := s.savedChoiceState(r, m.Model); state == "BLOCKED" || state == "NOT_LISTED" {
			apiError(w, http.StatusUnprocessableEntity, "MODEL_UNAVAILABLE", gone)
			return true
		}
		return false
	}
	blocked, err := store.BlockedModels(r.Context(), s.db)
	if err != nil {
		internalError(w, "blocked models", err)
		return true
	}
	if len(blocked) == 0 {
		return false
	}
	_, model, err := s.effectiveDefault(r.Context(), b)
	switch {
	case errors.Is(err, errNoDefault):
		apiError(w, http.StatusUnprocessableEntity, "MODEL_UNAVAILABLE", "The assistant's default model could not be determined, so policy cannot be checked; choose a model")
		return true
	case err != nil:
		s.gateError(w, err)
		return true
	case slices.Contains(blocked, model):
		apiError(w, http.StatusUnprocessableEntity, "MODEL_UNAVAILABLE", "The default model is blocked by policy. Choose another model; nothing was sent")
		return true
	}
	return false
}

type visibilityBody struct {
	Hidden []string `json:"hidden"`
}

// modelIDOK bounds a model id the way a stored preference must be: printable, short.
func modelIDOK(id string) bool {
	if id == "" || len(id) > 200 || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// GET /api/me/model-visibility — the models the caller hid from their own picker.
func (s *Service) getModelVisibility(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	hidden, err := store.HiddenModels(r.Context(), s.db, uid)
	if err != nil {
		internalError(w, "hidden models", err)
		return
	}
	writeJSON(w, http.StatusOK, visibilityBody{Hidden: hidden})
}

// PUT /api/me/model-visibility — replaces the caller's hidden set; [] reveals everything. It
// changes only the caller's view: no Hermes call, no other user, no provider setting.
func (s *Service) putModelVisibility(w http.ResponseWriter, r *http.Request) {
	uid, _ := user(r)
	var body visibilityBody
	if !decode(w, r, &body) {
		return
	}
	if body.Hidden == nil || len(body.Hidden) > store.MaxModelIDs {
		apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "hidden must be a list of at most 500 model ids")
		return
	}
	for _, id := range body.Hidden {
		if !modelIDOK(id) {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "hidden carries an invalid model id")
			return
		}
	}
	if err := store.SetHiddenModels(r.Context(), s.db, uid, body.Hidden); err != nil {
		internalError(w, "set hidden models", err)
		return
	}
	s.getModelVisibility(w, r)
}
