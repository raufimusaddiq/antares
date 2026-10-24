package chat

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/raufimusaddiq/antares/internal/hermes"
	"github.com/raufimusaddiq/antares/internal/kenari"
	"github.com/raufimusaddiq/antares/internal/store"
)

// Image input (PRD §27.3 MEDIA-EXT-001/002, issue #101).
//
// Whether a model can take images is read from the Kenari catalogue (internal/kenari), the
// provider Hermes is configured with; Hermes' own inventory publishes no modalities, so its
// silence is not evidence either way. Execution is still Hermes: the images go inline in the
// same POST /v1/runs, and the model that was checked is the model that is sent, by id.
const (
	maxImagesPerTurn = 4
	// Hermes refuses a request body over 10,000,000 bytes (api_server.py MAX_REQUEST_BYTES) and
	// base64 adds a third, so 6 MiB of image bytes plus a 32 KiB message stays under it.
	maxTurnImageBytes = 6 << 20
)

// Reasons an image cannot be sent. Nothing is ever discarded silently: the send is refused.
const (
	reasonNoCatalogue    = "CATALOGUE_NOT_CONFIGURED"
	reasonCatalogueDown  = "CATALOGUE_UNAVAILABLE"
	reasonNoModel        = "MODEL_UNKNOWN"
	reasonNotListed      = "MODEL_NOT_IN_CATALOGUE"
	reasonTextOnly       = "MODEL_TEXT_ONLY"
	codeImageUnavailable = "IMAGE_INPUT_UNAVAILABLE"
)

// imageVerdict says whether the next run of a chat can carry images, and for which exact
// provider/model. Provider and Model are what the run will name to Hermes.
type imageVerdict struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Provider  string `json:"-"`
	Model     string `json:"model,omitempty"`
	Source    string `json:"source"`
}

// imageInput resolves the model the chat's next run would use (its saved choice, else the
// profile default Hermes reports) and checks Kenari's published input modalities for that id.
func (s *Service) imageInput(ctx context.Context, b hermes.Binding, uid, chatID string) imageVerdict {
	v := imageVerdict{Source: "kenari"}
	if s.kenari == nil {
		v.Reason = reasonNoCatalogue
		return v
	}
	choice, err := store.GetChatModel(ctx, s.db, uid, chatID)
	if err != nil {
		v.Reason = reasonNoModel
		return v
	}
	v.Provider, v.Model = choice.Provider, choice.Model
	if v.Model == "" {
		cat, err := s.hx.ModelOptions(ctx, b, false)
		if err != nil || cat.Model == "" {
			v.Reason = reasonNoModel
			return v
		}
		v.Provider, v.Model = cat.Provider, cat.Model
	}
	m, found, err := s.kenari.Lookup(ctx, v.Model)
	switch {
	case errors.Is(err, kenari.ErrUnavailable) || err != nil:
		v.Reason = reasonCatalogueDown
	case !found:
		v.Reason = reasonNotListed
	case !m.AcceptsInput("image"):
		v.Reason = reasonTextOnly
	default:
		v.Available = true
	}
	return v
}

func reasonText(reason string) string {
	switch reason {
	case reasonNoCatalogue:
		return "The model catalogue is not configured, so image support cannot be confirmed"
	case reasonCatalogueDown:
		return "The model catalogue could not be read, so image support cannot be confirmed"
	case reasonNoModel:
		return "The model for this chat could not be determined"
	case reasonNotListed:
		return "The model for this chat is not in the provider's catalogue, so image support is unknown"
	case reasonTextOnly:
		return "The model for this chat does not accept images; switch to a model that does"
	}
	return "Images cannot be sent in this chat"
}

// GET /api/chats/{chatId}/input — what the next message in this chat may carry.
func (s *Service) getChatInput(w http.ResponseWriter, r *http.Request) {
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
	b, _, err := s.gate(r.Context(), uid)
	if err != nil {
		s.gateError(w, err)
		return
	}
	v := s.imageInput(r.Context(), b, uid, chatID)
	writeJSON(w, http.StatusOK, map[string]any{
		"image": map[string]any{"available": v.Available, "reason": nullIfEmpty(v.Reason), "model": nullIfEmpty(v.Model), "source": v.Source,
			"maxImages": maxImagesPerTurn, "maxTotalBytes": maxTurnImageBytes},
	})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// turnImages is what a run carries beyond its text, resolved before admission.
type turnImages struct {
	files           []store.AssetFile
	provider, model string // the checked model, pinned so Hermes cannot run a different one
}

func (t *turnImages) hermesImages() []hermes.Image {
	out := make([]hermes.Image, len(t.files))
	for i, f := range t.files {
		out[i] = hermes.Image{MediaType: f.MediaType, Data: f.Content}
	}
	return out
}

func (t *turnImages) assets() []store.Asset {
	out := make([]store.Asset, len(t.files))
	for i, f := range t.files {
		out[i] = f.Asset
	}
	return out
}

// linkRunImages marks user turns of the transcript that carried images with the owner's assets
// that went with them. Hermes renders an image part as "[image]" in the history read, so a turn's
// image count is all that is known. Turns are matched newest-first against the chat's own
// image runs and only while the counts agree: older history that was compacted or rewritten
// is left unlinked (shown as an attachment placeholder), never guessed.
func linkRunImages(all []hermes.Message, runs []store.RunImageSet) {
	k := len(runs) - 1
	for i := len(all) - 1; i >= 0 && k >= 0; i-- {
		m := &all[i]
		if m.Role != "user" {
			continue
		}
		n := strings.Count(m.Text, "[image]")
		if n == 0 {
			continue
		}
		set := runs[k]
		if len(set.Images) != n {
			return
		}
		m.Images = n
		for _, im := range set.Images {
			m.Attachments = append(m.Attachments, hermes.Attachment{AssetID: im.AssetID, MediaType: im.MediaType, Available: im.Live})
		}
		k--
	}
}

// resolveImages turns the asset ids of a send into owner-resolved image bytes, or answers the
// request. Ownership comes first (a foreign id is a 404 like an unknown one), then the budget,
// then the model: an image is never dropped to let a text-only send through.
func (s *Service) resolveImages(w http.ResponseWriter, r *http.Request, b hermes.Binding, uid, chatID string, ids []string) (*turnImages, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !idRE.MatchString(id) || seen[id] || len(ids) > maxImagesPerTurn {
			apiError(w, http.StatusBadRequest, "INVALID_REQUEST", "Send up to "+strconv.Itoa(maxImagesPerTurn)+" distinct image ids")
			return nil, false
		}
		seen[id] = true
	}
	files, err := store.ImageAssets(r.Context(), s.db, uid, ids)
	if errors.Is(err, store.ErrNoAsset) {
		notFound(w)
		return nil, false
	} else if err != nil {
		internalError(w, "load images", err)
		return nil, false
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	if total > maxTurnImageBytes {
		apiError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The images together are larger than "+strconv.Itoa(maxTurnImageBytes>>20)+" MiB")
		return nil, false
	}
	v := s.imageInput(r.Context(), b, uid, chatID)
	if !v.Available {
		apiError(w, http.StatusUnprocessableEntity, codeImageUnavailable, reasonText(v.Reason))
		return nil, false
	}
	return &turnImages{files: files, provider: v.Provider, model: v.Model}, true
}
