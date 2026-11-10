package auth

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"strings"

	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/modelpolicy"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Admin → Models (specs/004-admin-full-control): every model Kenari offers with an on/off switch (the block list), the default
// per purpose, and per-model usage totals. One read for the screen; the writes are PUT /api/admin/model-policy (switches),
// PUT /api/admin/model-defaults and PUT /api/admin/media-models.

// The purposes a model is chosen for.
var purposeOrder = []string{"chat", "summary", "blog_assistant", "image", "video"}

type modelRow struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	On         bool              `json:"on"`
	Purposes   []string          `json:"purposes"`
	DefaultFor []string          `json:"defaultFor"`
	Usage      *store.ModelTotal `json:"usage"`
	Free       bool              `json:"free,omitempty"`
}

type purposeView struct {
	Current   string `json:"current"`   // "" = the service default
	Effective string `json:"effective"` // what runs; "" = the service default
	Warning   string `json:"warning,omitempty"`
}

type modelsView struct {
	Catalogue string                 `json:"catalogue"` // "ok" or "unavailable"
	Models    []modelRow             `json:"models"`
	Purposes  map[string]purposeView `json:"purposes"`
}

func (a *Auth) modelsView(ctx context.Context) (modelsView, error) {
	v := modelsView{Catalogue: "unavailable", Models: []modelRow{}, Purposes: map[string]purposeView{}}
	blocked, err := store.BlockedModels(ctx, a.db)
	if err != nil {
		return v, err
	}
	defaults, err := store.ModelDefaults(ctx, a.db)
	if err != nil {
		return v, err
	}
	image, video, err := store.TenantMediaModels(ctx, a.db)
	if err != nil {
		return v, err
	}
	usage, err := store.ModelUsageTotals(ctx, a.db, a.now().Add(-30*24*time.Hour))
	if err != nil {
		return v, err
	}
	var cat []kenari.Model
	if a.catalogue != nil {
		if m, err := a.catalogue.List(ctx); err == nil {
			cat, v.Catalogue = m, "ok"
		}
	}
	now := a.now()
	current := map[string]string{"chat": defaults["chat"], "summary": defaults["summary"], "blog_assistant": defaults["blog_assistant"], "image": image, "video": video}
	// What each purpose really runs: the saved choice, else the plugin's own pick for media (so switching that one off is refused too).
	running := map[string]string{"chat": defaults["chat"], "summary": defaults["summary"], "blog_assistant": defaults["blog_assistant"],
		"image": firstNonEmpty(image, defaultImageModel), "video": firstNonEmpty(video, defaultVideoModel)}

	rows := map[string]*modelRow{}
	add := func(id, name string, free bool) *modelRow {
		if r, ok := rows[id]; ok {
			return r
		}
		if name == "" {
			name = id
		}
		r := &modelRow{ID: id, Name: name, On: !slices.Contains(blocked, id), Purposes: []string{}, DefaultFor: []string{}, Free: free}
		if u, ok := usage[id]; ok {
			t := u
			r.Usage = &t
		}
		rows[id] = r
		return r
	}
	for _, m := range cat {
		free := m.Pricing != nil && m.Pricing.Free
		if modelpolicy.ChatUsable(m, now) {
			r := add(m.ID, m.Name, free)
			r.Purposes = append(r.Purposes, "chat", "summary", "blog_assistant")
		}
		if slices.Contains(m.Endpoints, "images") && m.HasModalities && m.Modalities.AcceptsInput("text") {
			add(m.ID, m.Name, free).Purposes = append(rows[m.ID].Purposes, "image")
		}
		if slices.Contains(m.Endpoints, "videos") && m.HasModalities && m.Modalities.AcceptsInput("text") {
			add(m.ID, m.Name, free).Purposes = append(rows[m.ID].Purposes, "video")
		}
	}
	for _, id := range blocked { // a switched-off model Kenari no longer lists must still be switchable back on
		add(id, "", false)
	}
	for _, p := range purposeOrder {
		pv := purposeView{Current: current[p]}
		if cur := current[p]; cur != "" {
			pv.Effective = cur
			switch {
			case p == "image" || p == "video":
				switch {
				case slices.Contains(blocked, cur):
					pv.Warning = modelpolicy.Off
				case v.Catalogue == "ok" && !slices.ContainsFunc(cat, func(m kenari.Model) bool {
					return m.ID == cur && slices.Contains(m.Endpoints, map[string]string{"image": "images", "video": "videos"}[p])
				}):
					pv.Warning = modelpolicy.NotListed
				}
			case v.Catalogue == "ok":
				pv.Warning = modelpolicy.Reason(cat, blocked, cur, now)
			case slices.Contains(blocked, cur):
				pv.Warning = modelpolicy.Off
			}
			if pv.Warning != "" {
				pv.Effective = ""
			}
		}
		v.Purposes[p] = pv
		if id := running[p]; id != "" {
			add(id, "", false).DefaultFor = append(rows[id].DefaultFor, p)
		}
	}
	for _, r := range rows {
		v.Models = append(v.Models, *r)
	}
	sort.Slice(v.Models, func(i, j int) bool { return v.Models[i].ID < v.Models[j].ID })
	return v, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *Auth) adminModels(w http.ResponseWriter, r *http.Request) {
	v, err := a.modelsView(r.Context())
	if err != nil {
		log.Printf("auth: models view: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// refuseDefaults answers 409 MODEL_IS_DEFAULT, naming the purposes, when any of ids is the model a purpose runs. It reports whether it answered.
func (a *Auth) refuseDefaults(w http.ResponseWriter, r *http.Request, ids []string) bool {
	v, err := a.modelsView(r.Context())
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return true
	}
	hit := []string{}
	for _, m := range v.Models {
		if slices.Contains(ids, m.ID) {
			for _, p := range m.DefaultFor {
				if !slices.Contains(hit, p) {
					hit = append(hit, p)
				}
			}
		}
	}
	if len(hit) == 0 {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "MODEL_IS_DEFAULT", "message": "That model is the default for " + strings.Join(hit, ", ") + ". Choose another default first.", "requestId": newRequestID(), "purposes": hit}})
	return true
}

func validModelID(id string) bool {
	return id != "" && len(id) <= 200 && utf8.ValidString(id) && !strings.ContainsFunc(id, unicode.IsControl)
}

// adminSetModelDefaults saves the default for chat, summaries and titles, and the blog assistant. A purpose left out is unchanged,
// "" returns it to the service default; a model must be one Kenari lists for chat, not past its sunset and switched on (a
// default that is already saved may stay: Kenari may be unreadable right now, or have dropped it).
func (a *Auth) adminSetModelDefaults(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Chat          *string `json:"chat"`
		Summary       *string `json:"summary"`
		BlogAssistant *string `json:"blog_assistant"`
	}
	if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"chat\"?, \"summary\"?, \"blog_assistant\"?} with model ids (empty for the service default) and nothing else")
		return
	}
	changes := map[string]string{}
	for p, v := range map[string]*string{"chat": body.Chat, "summary": body.Summary, "blog_assistant": body.BlogAssistant} {
		if v != nil {
			if *v != "" && !validModelID(*v) {
				a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "That model id is not valid")
				return
			}
			changes[p] = *v
		}
	}
	cur, err := a.modelsView(r.Context())
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	for p, id := range changes {
		if id == "" || id == cur.Purposes[p].Current {
			continue
		}
		i := slices.IndexFunc(cur.Models, func(m modelRow) bool { return m.ID == id })
		if i < 0 || !cur.Models[i].On || !slices.Contains(cur.Models[i].Purposes, p) {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "That model is not on, or not offered for this purpose")
			return
		}
	}
	actor, _ := UserFrom(r.Context())
	if err := store.SetModelDefaults(r.Context(), a.db, actor.ID, changes, newRequestID(), a.now()); err != nil {
		a.adminError(w, r, err)
		return
	}
	a.adminModels(w, r)
}
