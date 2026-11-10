package auth

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/raufimusaddiq/antares/internal/store"
	"github.com/raufimusaddiq/antares/internal/writing"
)

// Admin → Capabilities → Writing rules (specs/005-antislop-writing, contracts/admin-api.md). The administrator sees whether the
// built-in rules are on, which version, where they come from, and counts of the last 24 hours. Counts only: no account, chat, post
// or text of the rules is ever returned.

type writingRulesView struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
	Source  string `json:"source"`
	License string `json:"license"`
	Last24h struct {
		With       int64 `json:"with"`
		Without    int64 `json:"without"`
		NotApplied int64 `json:"notApplied"`
	} `json:"last24h"`
	Warning string `json:"warning,omitempty"`
}

func (a *Auth) writingRulesView(r *http.Request) (writingRulesView, error) {
	var v writingRulesView
	on, err := store.WritingRulesEnabled(r.Context(), a.db)
	if err != nil {
		return v, err
	}
	c, err := store.WritingRulesCounts(r.Context(), a.db, a.now().Add(-24*time.Hour))
	if err != nil {
		return v, err
	}
	v.Enabled, v.Version = on, writing.Version
	v.Source = "anti-slop (github.com/miqdadbadjuber/anti-slop)"
	v.License = "MIT, Copyright (c) 2026 Miqdad Badjuber (antislop)"
	v.Last24h.With, v.Last24h.Without, v.Last24h.NotApplied = c.With, c.Without, c.NotApplied
	if on && c.NotApplied > 0 {
		v.Warning = "not_applied"
	}
	return v, nil
}

func (a *Auth) adminWritingRules(w http.ResponseWriter, r *http.Request) {
	v, err := a.writingRulesView(r)
	if err != nil {
		log.Printf("auth: writing rules view: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *Auth) adminSetWritingRules(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if dec.Decode(&body) != nil || body.Enabled == nil || dec.Decode(&struct{}{}) != io.EOF {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"enabled\": true or false} and nothing else")
		return
	}
	actor, _ := UserFrom(r.Context())
	if err := store.SetWritingRules(r.Context(), a.db, actor.ID, *body.Enabled, newRequestID(), a.now()); err != nil {
		a.adminError(w, r, err)
		return
	}
	a.adminWritingRules(w, r)
}
