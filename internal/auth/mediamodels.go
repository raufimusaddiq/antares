package auth

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Admin → Assistant media: the default image and video generation models every account's assistant uses
// (docs/runbooks/media-models.md). The choices are Kenari's own catalogue: the models Kenari serves on its images and
// videos endpoints that take a text prompt. Saving re-verifies every ready account, which writes the choice into each
// profile's config.

// ModelCatalogue is the Kenari model list the media choices come from.
type ModelCatalogue interface {
	List(ctx context.Context) ([]kenari.Model, error)
}

// The plugins' own defaults, shown so "default" says which model that is. Keep in step with deploy/hermes-plugins.
const (
	defaultImageModel = "agnes-image-2-5-flash:free"
	defaultVideoModel = "veo-3.1-lite"
)

type mediaOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Free bool   `json:"free,omitempty"`
}

type mediaChoice struct {
	Current string        `json:"current"` // "" = the default
	Default string        `json:"default"`
	Options []mediaOption `json:"options"`
}

type mediaModelsView struct {
	Image       mediaChoice `json:"image"`
	Video       mediaChoice `json:"video"`
	Catalogue   string      `json:"catalogue"` // "ok" or "unavailable": the lists are empty when Kenari could not be read
	Reverifying *int        `json:"reverifying,omitempty"`
}

// textToMedia lists the catalogue rows served on endpoint that accept a text prompt.
func textToMedia(models []kenari.Model, endpoint string) []mediaOption {
	out := []mediaOption{}
	for _, m := range models {
		if slices.Contains(m.Endpoints, endpoint) && m.HasModalities && m.Modalities.AcceptsInput("text") {
			o := mediaOption{ID: m.ID, Name: m.Name, Free: m.Pricing != nil && m.Pricing.Free}
			if o.Name == "" {
				o.Name = m.ID
			}
			out = append(out, o)
		}
	}
	return out
}

func (a *Auth) mediaModelsView(r *http.Request) (mediaModelsView, error) {
	image, video, err := store.TenantMediaModels(r.Context(), a.db)
	if err != nil {
		return mediaModelsView{}, err
	}
	v := mediaModelsView{
		Image:     mediaChoice{Current: image, Default: defaultImageModel, Options: []mediaOption{}},
		Video:     mediaChoice{Current: video, Default: defaultVideoModel, Options: []mediaOption{}},
		Catalogue: "unavailable",
	}
	if a.catalogue != nil {
		if models, err := a.catalogue.List(r.Context()); err == nil {
			v.Catalogue = "ok"
			v.Image.Options, v.Video.Options = textToMedia(models, "images"), textToMedia(models, "videos")
		}
	}
	return v, nil
}

func (a *Auth) adminMediaModels(w http.ResponseWriter, r *http.Request) {
	v, err := a.mediaModelsView(r)
	if err != nil {
		log.Printf("auth: media models: %v", err)
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *Auth) adminSetMediaModels(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Image *string `json:"image"`
		Video *string `json:"video"`
	}
	if dec.Decode(&body) != nil || dec.Decode(&struct{}{}) != io.EOF || body.Image == nil || body.Video == nil ||
		!hermes.ValidMediaModel(*body.Image) || !hermes.ValidMediaModel(*body.Video) {
		a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Body must be {\"image\": id, \"video\": id} (empty for the default) and nothing else")
		return
	}
	cur, err := a.mediaModelsView(r)
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	// A model must be one Kenari serves for that kind. A value that is already saved may stay (Kenari may be unreadable
	// right now, or have dropped the model); anything new must be in the list.
	for _, c := range []struct {
		want   string
		choice mediaChoice
	}{{*body.Image, cur.Image}, {*body.Video, cur.Video}} {
		if c.want != "" && c.want != c.choice.Current && !slices.ContainsFunc(c.choice.Options, func(o mediaOption) bool { return o.ID == c.want }) {
			a.apiError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "That model is not one Kenari offers for this")
			return
		}
	}
	actor, _ := UserFrom(r.Context())
	ids, err := store.SetTenantMediaModels(r.Context(), a.db, actor.ID, *body.Image, *body.Video, newRequestID(), a.now())
	if err != nil {
		a.adminError(w, r, err)
		return
	}
	if a.tenants != nil {
		for _, id := range ids {
			a.tenants.Kick(id)
		}
	}
	v, err := a.mediaModelsView(r)
	if err != nil {
		a.apiError(w, r, http.StatusInternalServerError, "INTERNAL", "Something went wrong")
		return
	}
	n := len(ids)
	v.Reverifying = &n
	writeJSON(w, http.StatusOK, v)
}
