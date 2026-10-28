package auth

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Admin → Assistant tools: which assistant toolsets every account has (docs/runbooks/tenant-toolsets.md). An admin
// chooses among hermes.OfferedToolsets; the floor (hermes.DeniedToolsets) and the toolsets not reviewed for shared
// accounts are listed, locked, with the reason. Saving re-verifies every ready account, which applies the change.

type toolCopy struct{ label, description string }

var toolText = map[string]toolCopy{
	"web":       {"Web search", "Search the web and read pages."},
	"vision":    {"Image understanding", "Look at images people attach."},
	"image_gen": {"Image generation", "Make pictures."},
	"video_gen": {"Video generation", "Make short videos. Needs a video provider set up on the assistant server."},
	"video":     {"Video understanding", "Watch and describe videos."},
	"tts":       {"Text to speech", "Turn text into spoken audio. Needs a speech provider on the assistant server."},
	"stt":       {"Speech to text", "Transcribe audio."},
	"x_search":  {"X search", "Search posts on X, using the server's X account."},
	"skills":    {"Skills", "Save and reuse step-by-step skills in the person's own profile."},
	"memory":    {"Memory", "Remember facts across chats, separately for each person."},
	"todo":      {"To-do list", "Keep a task list while working on something."},
	"clarify":   {"Ask back", "Ask the person a question before going on."},
}

// Why a toolset can never be turned on for accounts (the floor), in plain words.
var lockedWhy = map[string]string{
	"terminal":       "Could read other people's data on the shared server.",
	"file":           "Could read other people's data on the shared server.",
	"code_execution": "Could read other people's data on the shared server.",
	"delegation":     "Starts work with every tool on, which would undo these settings.",
	"cronjob":        "Starts work with every tool on, which would undo these settings.",
	"session_search": "Searches everyone's conversations.",
	"connections":    "Uses the owner's linked accounts.",
	"browser":        "A full browser with logged-in sessions.",
}

const notReviewed = "Not reviewed for shared accounts."

type toolView struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"` // on, off, or locked
	Reason      string `json:"reason,omitempty"`
}

type toolPolicyView struct {
	Tools       []toolView `json:"tools"`
	Custom      bool       `json:"custom"` // false: the server's configured default applies
	Reverifying *int       `json:"reverifying,omitempty"`
}

func (a *Auth) toolPolicyView(r *http.Request) (toolPolicyView, error) {
	names, _, err := store.TenantToolPolicy(r.Context(), a.db)
	if err != nil {
		return toolPolicyView{}, err
	}
	v := toolPolicyView{Custom: names != nil}
	if names == nil {
		names = a.tools
	}
	for _, n := range hermes.OfferedToolsets {
		state := "off"
		if slices.Contains(names, n) {
			state = "on"
		}
		v.Tools = append(v.Tools, toolView{Name: n, Label: toolText[n].label, Description: toolText[n].description, State: state})
	}
	for _, n := range hermes.KnownToolsets {
		if slices.Contains(hermes.OfferedToolsets, n) {
			continue
		}
		why, floor := lockedWhy[n]
		if !floor {
			why = notReviewed
		}
		// A default that still enables an unoffered toolset says so: it is on until an admin saves a policy.
		state := "locked"
		if !v.Custom && slices.Contains(names, n) {
			state = "on"
		}
		v.Tools = append(v.Tools, toolView{Name: n, Label: n, State: state, Reason: why})
	}
	return v, nil
}

func (a *Auth) adminToolPolicy(w http.ResponseWriter, r *http.Request) {
	v, err := a.toolPolicyView(r)
	if err != nil {
		log.Printf("auth: tool policy: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *Auth) adminSetToolPolicy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Allowed []string `json:"allowed"`
	}
	if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || body.Allowed == nil {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"allowed\": [tool names]} and nothing else")
		return
	}
	if err := hermes.ValidateOfferedToolsets(body.Allowed); err != nil {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose at least one tool, each from the offered list, once")
		return
	}
	actor, _ := UserFrom(r.Context())
	ids, err := store.SetTenantToolPolicy(r.Context(), a.db, actor.ID, body.Allowed, newRequestID(), a.now())
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	if a.tenants != nil {
		for _, id := range ids {
			a.tenants.Kick(id)
		}
	}
	v, err := a.toolPolicyView(r)
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	n := len(ids)
	v.Reverifying = &n
	writeJSON(w, http.StatusOK, v)
}
